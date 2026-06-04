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

// searchTestServer wires SearchGet with a nil DB pool and a session-injecting
// middleware, so the no-DB rendering paths can be asserted without Postgres.
func searchTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
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
	e.GET("/search", h.SearchGet)
	return e
}

// TestSearchGet_RedirectsWhenUnauthenticated: no session → bounce to /login.
func TestSearchGet_RedirectsWhenUnauthenticated(t *testing.T) {
	t.Parallel()
	e := searchTestServer(t, user.User{}, false)

	req := httptest.NewRequest(http.MethodGet, "/search?q=foo", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

// TestSearchGet_RendersSearchFormAndChrome: an authenticated request renders
// the search page (its own form + the top-bar search input that drives it).
// With a nil DB the page surfaces the "not configured" notice rather than
// crashing — the box must still be a real, focusable <form>.
func TestSearchGet_RendersSearchFormAndChrome(t *testing.T) {
	t.Parallel()
	signed := user.User{ID: "u-1", DisplayName: "Tester"}
	e := searchTestServer(t, signed, true)

	req := httptest.NewRequest(http.MethodGet, "/search?q=foo", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`action="/search"`,    // the on-page search form
		`id="topbar-q"`,       // the top-bar box is now a real input
		`name="q"`,            // submits the query param SearchGet reads
		"Database is not configured.", // nil-DB notice (graceful, not a panic)
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered /search missing %q", want)
		}
	}
}

// TestSearchGet_BlankQueryStillRenders: an empty q must not error — it renders
// the page (here the nil-DB notice short-circuits before the prompt, but the
// point is no panic and a 200).
func TestSearchGet_BlankQueryStillRenders(t *testing.T) {
	t.Parallel()
	signed := user.User{ID: "u-1", DisplayName: "Tester"}
	e := searchTestServer(t, signed, true)

	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `action="/search"`) {
		t.Error("blank-query /search did not render the search form")
	}
}
