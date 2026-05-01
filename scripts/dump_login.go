//go:build ignore

// dump_login renders the /login page via the in-memory handler and writes the
// HTML to stdout. Used to feed reviewers (e.g. Jony Ive) the actual rendered
// output without booting the HTTP server.
//
//	go run ./scripts/dump_login.go > out.html
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/web/handler"
)

func main() {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handler.LoginGet(c); err != nil {
		fmt.Fprintf(os.Stderr, "handler error: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(rec.Body.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "write error: %v\n", err)
		os.Exit(1)
	}
}
