package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

// homeTestServer constructs a Handlers wired with nil DB pool + a fake
// session-injecting middleware so HomeGet tests don't need a live PG.
func homeTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
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
	e.GET("/", h.HomeGet)
	e.POST("/logout", h.LogoutPost)
	return e
}

// TestHomeGet_Unauthenticated_RedirectsToLogin: spec §3.2 access control.
func TestHomeGet_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := homeTestServer(t, user.User{}, false)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

// TestHomeGet_NilPoolRendersPlaceholder pins the pool=nil fallback path.
// The handler must render a 200 with the empty-state subtitle, not 500.
func TestHomeGet_NilPoolRendersPlaceholder(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-test", DisplayName: "Tester"}
	e := homeTestServer(t, signedIn, true)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Tester") {
		t.Error("body missing display name")
	}
	if !strings.Contains(body, "load once seed data lands") {
		t.Error("body missing empty-state subtitle (nil pool fallback)")
	}
	// LoadError SHOULD NOT trigger (this is no-DB, not error-from-DB).
	if strings.Contains(body, `class="alert"`) {
		t.Error("body contains alert class — nil pool should not render error state")
	}
	// Logout form must always be present.
	if !strings.Contains(body, `action="/logout"`) {
		t.Error("body missing logout form")
	}
}

// TestLogoutPost_ClearsCookieAndRedirects pins the §3.4 logout contract.
func TestLogoutPost_ClearsCookieAndRedirects(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-test", DisplayName: "Tester"}
	e := homeTestServer(t, signedIn, true)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	// Send an existing session cookie so Auth.Logout has a token to revoke.
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "old-token"})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
	// The cookie must be cleared: MaxAge<0, expired Expires, HttpOnly,
	// SameSite=Lax, Path=/.
	var cleared *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			cleared = c
			break
		}
	}
	if cleared == nil {
		t.Fatal("logout did not set Set-Cookie for the session")
	}
	if cleared.Value != "" {
		t.Errorf("cleared cookie value = %q, want empty", cleared.Value)
	}
	if cleared.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want negative (expire immediately)", cleared.MaxAge)
	}
	if !cleared.HttpOnly {
		t.Error("cleared cookie not HttpOnly")
	}
	if cleared.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cleared.SameSite)
	}
	if cleared.Path != "/" {
		t.Errorf("Path = %q, want /", cleared.Path)
	}
	if !cleared.Expires.Before(time.Now()) {
		t.Errorf("Expires = %v, want in the past", cleared.Expires)
	}
}

// TestLogoutPost_NoCookie_StillRedirects: spec §3.4 — logout is idempotent.
// A POST without a session cookie still clears (no-op) and redirects.
func TestLogoutPost_NoCookie_StillRedirects(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-test", DisplayName: "Tester"}
	e := homeTestServer(t, signedIn, true)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", rec.Code)
	}
}
