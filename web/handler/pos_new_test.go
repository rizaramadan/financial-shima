package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func posNewTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
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
	e.GET("/pos/new", h.PosNewGet)
	e.POST("/pos", h.PosNewPost)
	return e
}

func TestPosNewGet_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := posNewTestServer(t, user.User{}, false)
	req := httptest.NewRequest(http.MethodGet, "/pos/new", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}

func TestPosNewGet_Authenticated_RendersForm(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Riza"}
	e := posNewTestServer(t, signedIn, true)
	req := httptest.NewRequest(http.MethodGet, "/pos/new", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<h1>New Pos</h1>`) {
		t.Error("body missing h1")
	}
	for _, field := range []string{`name="name"`, `name="currency"`, `name="target"`} {
		if !strings.Contains(body, field) {
			t.Errorf("body missing %s", field)
		}
	}
	if !strings.Contains(body, `action="/pos"`) {
		t.Error("form does not POST to /pos")
	}
}

func TestPosNewPost_InvalidName_RendersErrors(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Riza"}
	e := posNewTestServer(t, signedIn, true)

	form := url.Values{"name": {"  "}, "currency": {"idr"}}
	req := httptest.NewRequest(http.MethodPost, "/pos", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Name is required") {
		t.Error("body missing Name error")
	}
}

func TestPosNewPost_InvalidCurrency_RendersErrors(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Riza"}
	e := posNewTestServer(t, signedIn, true)

	// Note: handler normalizes by lowercasing; "ID R" with the space is
	// what's left after normalization (still has the space) and fails
	// the regex.
	form := url.Values{"name": {"Mortgage"}, "currency": {"id r"}}
	req := httptest.NewRequest(http.MethodPost, "/pos", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "lowercase") {
		t.Errorf("body missing currency-format error; body excerpt:\n%s", excerpt(body, "alert", 600))
	}
	// User input round-trips so they don't retype.
	if !strings.Contains(body, `value="Mortgage"`) {
		t.Error("name not echoed back into form")
	}
}

func TestPosNewPost_NonIntegerTarget_RendersErrors(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Riza"}
	e := posNewTestServer(t, signedIn, true)

	form := url.Values{
		"name": {"Liburan"}, "currency": {"idr"}, "target": {"1.5e7"},
	}
	req := httptest.NewRequest(http.MethodPost, "/pos", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "whole number") {
		t.Errorf("body missing target-format error; body:\n%s", excerpt(body, "alert", 600))
	}
}

func TestPosNewPost_NilDB_RendersDBNotConfigured(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Riza"}
	e := posNewTestServer(t, signedIn, true)

	form := url.Values{
		"name": {"Mortgage"}, "currency": {"idr"}, "target": {"12000000"},
	}
	req := httptest.NewRequest(http.MethodPost, "/pos", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (graceful fallback when DB nil)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Database is not configured") {
		t.Error("body missing nil-DB error")
	}
}

func TestPosNewPost_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := posNewTestServer(t, user.User{}, false)
	form := url.Values{"name": {"X"}, "currency": {"idr"}}
	req := httptest.NewRequest(http.MethodPost, "/pos", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}

func excerpt(s, needle string, span int) string {
	i := strings.Index(s, needle)
	if i < 0 {
		return s
	}
	start := i - span/2
	if start < 0 {
		start = 0
	}
	end := i + span/2
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}
