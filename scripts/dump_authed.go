//go:build ignore

// dump_authed renders the authenticated pages (home, notifications,
// transactions, spending) by injecting a signed-in user via a fake middleware.
// The DB pool is nil — handlers fall back to placeholder/empty rendering, so
// this exercises the chrome + empty-state branches Ive will review.
//
//	go run ./scripts/dump_authed.go home > out_home.html
//	go run ./scripts/dump_authed.go notifications > out_notifications.html
//	go run ./scripts/dump_authed.go transactions > out_transactions.html
//	go run ./scripts/dump_authed.go spending > out_spending.html
package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

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
		fmt.Fprintln(os.Stderr, "usage: dump_authed {home|notifications|transactions|spending}")
		os.Exit(2)
	}
	page := os.Args[1]

	a := auth.New(user.Seeded(), clock.System{}, bytes.NewReader(make([]byte, 64)),
		idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, nil)

	e := echo.New()
	e.Renderer = template.New()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, signedIn)
			return next(c)
		}
	})
	e.GET("/", h.HomeGet)
	e.GET("/notifications", h.NotificationsGet)
	e.GET("/transactions", h.TransactionsGet)
	e.GET("/spending", h.SpendingGet)

	url := map[string]string{
		"home":          "/",
		"notifications": "/notifications",
		"transactions":  "/transactions",
		"spending":      "/spending",
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
