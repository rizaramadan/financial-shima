package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

// debtsTestServer wires DebtsGet with a nil DB pool so the page tests
// don't need a live PG (placeholder render path).
func debtsTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
	t.Helper()
	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(user.Seeded(), clock.Fixed{T: t0}, src, idgen.Fixed{Value: "tok"})
	h := New(a, &assistant.Recorder{}, nil)

	e := echo.New()
	e.Renderer = tplpkg.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if signedInOK {
				c.Set(mw.SessionContextKey, signedIn)
			}
			return next(c)
		}
	})
	e.GET("/debts", h.DebtsGet)
	return e
}

// TestDebtsGet_Unauthenticated_RedirectsToLogin: signed-out visitors go
// back to /login like every other authenticated page.
func TestDebtsGet_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := debtsTestServer(t, user.User{}, false)

	req := httptest.NewRequest(http.MethodGet, "/debts", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

// TestDebtsGet_Authenticated_RendersPage: with no DB the page renders
// the empty state instead of 500 (placeholder boot path).
func TestDebtsGet_Authenticated_RendersPage(t *testing.T) {
	t.Parallel()
	e := debtsTestServer(t, user.Seeded()[0], true)

	req := httptest.NewRequest(http.MethodGet, "/debts", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Sisa Hutang") {
		t.Error("debts page does not render its title")
	}
}
