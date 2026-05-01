//go:build ignore

// dump_login renders the /login page via the handler chain and writes the
// HTML to stdout. Used to feed reviewers the actual rendered output without
// booting the HTTP server.
//
//	go run ./scripts/dump_login.go > out.html
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
	"github.com/rizaramadan/financial-shima/web/template"
)

func main() {
	a := auth.New(user.Seeded(), clock.System{}, bytes.NewReader(make([]byte, 4)), idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, nil)
	e := echo.New()
	e.Renderer = template.New()
	e.GET("/login", h.LoginGet)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
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
