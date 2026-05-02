//go:build ignore

// dump_login_error renders /login with an inline error (S2: unknown
// identifier) so the alert/error state appears in screenshots.
//
//	go run ./scripts/dump_login_error.go > out.html
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"

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
	a := auth.New(user.Seeded(), clock.System{}, strings.NewReader("                "), idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, nil)

	e := echo.New()
	e.Renderer = template.New()
	e.GET("/login", h.LoginGet)
	e.POST("/login", h.LoginPost)

	form := url.Values{"identifier": {"@nobody_seeded"}}
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
