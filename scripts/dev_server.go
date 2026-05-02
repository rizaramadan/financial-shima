//go:build ignore

// dev_server boots the same handler tree cmd/server uses, but with two
// shims that make end-to-end browser tests possible:
//
//   - The OTP assistant is a [assistant.Recorder] (in-process); a
//     `/dev/last-otp?identifier=…` route reads the most recent code
//     out of it so a Playwright driver can complete the OTP flow
//     without ever needing real Telegram delivery.
//   - The user list is UUID-resolved from the live DB (same as
//     production) so notifications fire against the correct user_id.
//
// Run:
//
//	export DATABASE_URL=postgres://postgres@localhost:5432/financial_shima?sslmode=disable
//	go run ./scripts/dev_server.go    # listens on :8081
//
// This server is for local browser tests ONLY — never deploy.
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

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
	"github.com/rizaramadan/financial-shima/web/template"
)

const addr = ":8081"

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// UUID-resolve seeded users so ledger.Service.Insert writes
	// notification rows against real users.id values.
	users := user.Seeded()
	q := dbq.New(pool)
	for i, u := range users {
		row, err := q.GetUserByTelegramIdentifier(ctx, u.TelegramIdentifier)
		if err != nil {
			log.Fatalf("resolve user %s: %v", u.TelegramIdentifier, err)
		}
		users[i].ID = uuidString(row.ID.Bytes)
	}

	rec := &assistant.Recorder{}
	a := auth.New(users, clock.System{}, rand.Reader, idgen.Crypto{})
	h := handler.New(a, rec, pool)

	e := echo.New()
	e.Renderer = template.New()
	e.Use(mw.Session(a))

	// Production routes — same as cmd/server.
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
	e.GET("/pos/new", h.PosNewGet)
	e.POST("/pos", h.PosNewPost)
	e.GET("/pos/:id", h.PosGet)
	e.GET("/spending", h.SpendingGet)
	e.GET("/income-templates", h.IncomeTemplatesGet)
	e.GET("/income-templates/new", h.IncomeTemplateNewGet)
	e.POST("/income-templates", h.IncomeTemplateNewPost)
	e.GET("/income-templates/:id", h.IncomeTemplateGet)
	e.POST("/income-templates/:id/preview", h.IncomeTemplatePreviewPost)
	e.POST("/income-templates/:id/apply", h.IncomeTemplateApplyPost)

	// Dev shim: read the most recently recorded OTP for a given
	// identifier. Returns the bare 6-digit string or 404 if none.
	// Never wired in cmd/server (this file lives under scripts/ with
	// a build-ignore tag).
	// Dev shim: resolve a Pos by exact name (currency=idr) → uuid.
	// Lets browser-driven UAT scripts navigate to /pos/:id without
	// needing a UI link.
	e.GET("/dev/pos-id", func(c echo.Context) error {
		name := strings.TrimSpace(c.QueryParam("name"))
		currency := strings.TrimSpace(c.QueryParam("currency"))
		if currency == "" {
			currency = "idr"
		}
		var idStr string
		if err := pool.QueryRow(c.Request().Context(),
			`SELECT id::text FROM pos WHERE name = $1 AND currency = $2`,
			name, currency,
		).Scan(&idStr); err != nil {
			return c.String(http.StatusNotFound, "not found")
		}
		return c.String(http.StatusOK, idStr)
	})

	e.GET("/dev/last-otp", func(c echo.Context) error {
		identifier := strings.TrimSpace(c.QueryParam("identifier"))
		if identifier == "" {
			return c.String(http.StatusBadRequest, "identifier required")
		}
		// Resolve identifier → display name (Recorder keys on display name).
		u, ok := user.Find(identifier, a.Users)
		if !ok {
			return c.String(http.StatusNotFound, "no user")
		}
		// Walk the recorded SentMessages (newest last) for a match.
		for i := len(rec.Sent) - 1; i >= 0; i-- {
			if rec.Sent[i].DisplayName == u.DisplayName {
				return c.String(http.StatusOK, rec.Sent[i].Code)
			}
		}
		return c.String(http.StatusNotFound, "no OTP recorded")
	})

	fmt.Println("dev_server listening on", addr)
	fmt.Println("  /dev/last-otp?identifier=@riza_ramadan  → bare 6-digit code")
	if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

func uuidString(b [16]byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 36)
	idx := 0
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[idx] = '-'
			idx++
		}
		out[idx] = hex[v>>4]
		out[idx+1] = hex[v&0x0f]
		idx += 2
	}
	return string(out)
}
