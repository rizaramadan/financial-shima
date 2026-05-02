//go:build ignore

// dump_pos_new renders the /pos/new form with a signed-in stub user
// so the form chrome appears in screenshots.
package main

import (
	"crypto/rand"
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
	a := auth.New(user.Seeded(), clock.System{}, rand.Reader, idgen.Crypto{})
	h := handler.New(a, &assistant.Recorder{}, nil)

	e := echo.New()
	e.Renderer = template.New()
	signedIn := user.User{ID: "u-1", DisplayName: "Riza"}
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, signedIn)
			return next(c)
		}
	})
	e.GET("/pos/new", h.PosNewGet)

	req := httptest.NewRequest(http.MethodGet, "/pos/new", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		fmt.Fprintf(os.Stderr, "unexpected status: %d\nbody:\n%s\n", rec.Code, rec.Body.String())
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(rec.Body.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}
