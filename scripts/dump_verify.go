//go:build ignore

// dump_verify renders the /verify page via the handler chain and writes
// HTML to stdout. Mirrors scripts/dump_login.go.
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
	a := auth.New(user.Seeded(), clock.System{}, bytes.NewReader(make([]byte, 4)),
		idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{})
	e := echo.New()
	e.Renderer = template.New()
	e.GET("/verify", h.VerifyGet)

	req := httptest.NewRequest(http.MethodGet, "/verify?id=%40shima", nil)
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
