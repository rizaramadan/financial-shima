//go:build ignore

// dump_real renders authenticated pages by going through the real
// handler→DB→template path (NOT a synthesised mockup). Reads
// DATABASE_URL, connects to Postgres, mounts the same handler tree
// cmd/server uses, and injects a session for the user identified by
// USER_TELEGRAM (defaults to @riza_ramadan) via fake middleware.
//
//	export DATABASE_URL=postgres://postgres@localhost:5432/financial_shima?sslmode=disable
//	go run ./scripts/dump_real.go home > home.html
//	go run ./scripts/dump_real.go transactions > transactions.html
//	go run ./scripts/dump_real.go spending > spending.html
//	go run ./scripts/dump_real.go notifications > notifications.html
//	go run ./scripts/dump_real.go pos      > pos.html   (first IDR pos)
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	"github.com/rizaramadan/financial-shima/web/handler"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dump_real {home|transactions|spending|notifications|pos}")
		os.Exit(2)
	}
	page := os.Args[1]

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL not set")
		os.Exit(2)
	}
	telegram := os.Getenv("USER_TELEGRAM")
	if telegram == "" {
		telegram = "@riza_ramadan"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pool: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Resolve real user_id + display_name from the DB; we inject these into
	// the request context so the bell-count query (per-user) hits real rows.
	var userID, displayName string
	if err := pool.QueryRow(ctx,
		`SELECT id::text, display_name FROM users WHERE telegram_identifier = $1`, telegram,
	).Scan(&userID, &displayName); err != nil {
		fmt.Fprintf(os.Stderr, "lookup user %q: %v\n", telegram, err)
		os.Exit(1)
	}

	// Pick a Pos that has obligations + multiple transactions for the
	// richest /pos/:id render. Falls back to first IDR pos if none found.
	var posID string
	if page == "pos" {
		_ = pool.QueryRow(ctx, `
			SELECT p.id::text FROM pos p
			JOIN pos_obligation o ON o.debtor_pos_id = p.id OR o.creditor_pos_id = p.id
			WHERE p.archived = false AND p.currency = 'idr'
			ORDER BY p.name LIMIT 1`).Scan(&posID)
		if posID == "" {
			if err := pool.QueryRow(ctx,
				`SELECT id::text FROM pos WHERE archived = false AND currency = 'idr' ORDER BY name LIMIT 1`,
			).Scan(&posID); err != nil {
				fmt.Fprintf(os.Stderr, "lookup first idr pos: %v\n", err)
				os.Exit(1)
			}
		}
	}

	a := auth.New(user.Seeded(), clock.System{}, rand.Reader, idgen.Crypto{})
	h := handler.New(a, &assistant.Recorder{}, pool)

	e := echo.New()
	e.Renderer = template.New()
	signedIn := user.User{ID: userID, DisplayName: displayName}
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, signedIn)
			return next(c)
		}
	})
	e.GET("/", h.HomeGet)
	e.GET("/transactions", h.TransactionsGet)
	e.GET("/spending", h.SpendingGet)
	e.GET("/notifications", h.NotificationsGet)
	e.GET("/pos/:id", h.PosGet)

	url := map[string]string{
		"home":          "/",
		"transactions":  "/transactions",
		"spending":      "/spending?from=2025-11-01&to=2026-04-30",
		"notifications": "/notifications",
		"pos":           "/pos/" + posID,
	}[page]
	if url == "" {
		fmt.Fprintf(os.Stderr, "unknown page: %s\n", page)
		os.Exit(2)
	}

	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		fmt.Fprintf(os.Stderr, "unexpected status: %d\n", rec.Code)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(rec.Body.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "write error: %v\n", err)
		os.Exit(1)
	}
}
