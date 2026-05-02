package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	"github.com/rizaramadan/financial-shima/web/handler"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/setup"
	"github.com/rizaramadan/financial-shima/web/template"
)

const defaultAddr = ":8080"

const shutdownGraceDuration = setup.WriteTimeout + 1*time.Second

// resolveUserIDs replaces each seeded user's string ID with the matching
// DB uuid (looked up by telegram_identifier) so handlers that key on
// uuid.Parse(u.ID) — notifications feed, bell badge — actually fire their
// queries instead of silently returning empty. When db is nil or a row is
// missing, the seeded ID is preserved so the no-DB boot path still works.
func resolveUserIDs(db *pgxpool.Pool, seeded []user.User) []user.User {
	if db == nil {
		return seeded
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := dbq.New(db)
	out := make([]user.User, 0, len(seeded))
	for _, u := range seeded {
		row, err := q.GetUserByTelegramIdentifier(ctx, u.TelegramIdentifier)
		if err != nil {
			log.Printf("resolveUserIDs: lookup %s: %v", u.TelegramIdentifier, err)
			out = append(out, u)
			continue
		}
		u.ID = uuid.UUID(row.ID.Bytes).String()
		out = append(out, u)
	}
	return out
}

// newAssistant returns the production HTTP-backed client when both env vars
// are set, otherwise a Recorder fake that logs the would-be sends. The Phase
// 2 spec scopes "stubbed assistant" — operators set OTP_ASSISTANT_URL +
// OTP_ASSISTANT_API_KEY to flip to live delivery.
func newAssistant() assistant.Client {
	url := os.Getenv("OTP_ASSISTANT_URL")
	key := os.Getenv("OTP_ASSISTANT_API_KEY")
	if url == "" || key == "" {
		log.Print("OTP_ASSISTANT_URL / OTP_ASSISTANT_API_KEY not set; using in-memory recorder (codes printed via stderr by handler)")
		return &assistant.Recorder{}
	}
	return assistant.NewHTTPClient(url, key)
}

func newServer() *echo.Echo {
	db := newDBPool() // may be nil when DATABASE_URL unset
	users := resolveUserIDs(db, user.Seeded())
	a := auth.New(users, clock.System{}, rand.Reader, idgen.Crypto{})
	ac := newAssistant()
	return newServerWithDeps(a, ac, db)
}

// newDBPool returns a pgxpool.Pool when DATABASE_URL is set; otherwise nil.
// Handlers tolerate a nil pool by falling back to placeholder renders, so
// the binary still boots in dev without a Postgres on disk.
func newDBPool() *pgxpool.Pool {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Print("DATABASE_URL not set; running without DB (home view shows placeholder)")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		log.Fatalf("connect DATABASE_URL: %v", err)
	}
	return pool
}

// newServerWithDeps is the variant tests use to inject deterministic
// dependencies. Pass db=nil to test without Postgres.
func newServerWithDeps(a *auth.Auth, ac assistant.Client, db *pgxpool.Pool) *echo.Echo {
	e := echo.New()
	setup.Apply(e)
	e.Renderer = template.New()
	e.Use(mw.Session(a))

	h := handler.New(a, ac, db)
	e.GET("/", h.HomeGet)
	e.GET("/login", h.LoginGet)
	e.POST("/login", h.LoginPost)
	e.GET("/verify", h.VerifyGet)
	e.POST("/verify", h.VerifyPost)
	e.POST("/logout", h.LogoutPost)
	e.GET("/notifications", h.NotificationsGet)
	e.POST("/notifications/:id/read", h.NotificationMarkRead)
	e.POST("/notifications/mark-all-read", h.NotificationsMarkAllRead)
	e.GET("/transactions", h.TransactionsGet)
	e.GET("/pos/:id", h.PosGet)
	e.GET("/spending", h.SpendingGet)
	return e
}

// validateAddr — syntactic only, no I/O.
func validateAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("port %q is not numeric: %w", port, err)
	}
	if p < 1 || p > 65535 {
		return fmt.Errorf("port %d outside 1-65535", p)
	}
	return nil
}

func isBenignServerErr(err error) bool {
	return errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed)
}

func run(ctx context.Context, e *echo.Echo, addr string) error {
	serverErr := make(chan error, 1)
	go func() {
		defer close(serverErr)
		log.Printf("listening on %s", addr)
		if err := e.Start(addr); err != nil && !isBenignServerErr(err) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Print("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("server start: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceDuration)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		if startErr := <-serverErr; startErr != nil {
			return fmt.Errorf("graceful shutdown: %w (also: server: %v)", err, startErr)
		}
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serverErr; err != nil {
		return fmt.Errorf("server after shutdown: %w", err)
	}
	return nil
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	if err := validateAddr(addr); err != nil {
		log.Fatalf("invalid ADDR %q: %v", addr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, newServer(), addr); err != nil {
		log.Fatal(err)
	}
}
