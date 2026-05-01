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

func posTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
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
	e.GET("/pos/:id", h.PosGet)
	return e
}

func TestPosGet_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := posTestServer(t, user.User{}, false)
	req := httptest.NewRequest(http.MethodGet, "/pos/abc", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
}

func TestPosGet_InvalidUUID_RedirectsToHome(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := posTestServer(t, signedIn, true)
	req := httptest.NewRequest(http.MethodGet, "/pos/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/" {
		t.Errorf("Location = %q, want /", rec.Header().Get("Location"))
	}
}

func TestPosGet_NilPool_RendersNotFound(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := posTestServer(t, signedIn, true)
	req := httptest.NewRequest(http.MethodGet,
		"/pos/00000000-0000-0000-0000-000000000001", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Pos not found") {
		t.Error("body missing not-found header (nil pool fallback)")
	}
	// Nav still renders since user is authenticated.
	if !strings.Contains(body, `href="/notifications"`) {
		t.Error("nav missing on authenticated not-found page")
	}
}
