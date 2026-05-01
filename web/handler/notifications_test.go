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

// notifsTestServer is the equivalent of homeTestServer but registers the
// notifications routes.
func notifsTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
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
	e.GET("/notifications", h.NotificationsGet)
	e.POST("/notifications/:id/read", h.NotificationMarkRead)
	e.POST("/notifications/mark-all-read", h.NotificationsMarkAllRead)
	return e
}

func TestNotificationsGet_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := notifsTestServer(t, user.User{}, false)
	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
}

func TestNotificationsGet_NilPool_RendersEmptyState(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := notifsTestServer(t, signedIn, true)

	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<h1>Notifications</h1>") {
		t.Error("body missing h1")
	}
	if !strings.Contains(body, "Nothing to read") {
		t.Error("body missing empty-state subtitle")
	}
	// LoadError must NOT trigger (nil pool != error).
	if strings.Contains(body, `class="alert"`) {
		t.Error("body has alert class on nil-pool path")
	}
}

func TestNotificationMarkRead_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := notifsTestServer(t, user.User{}, false)
	req := httptest.NewRequest(http.MethodPost, "/notifications/abc/read", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
}

func TestNotificationsMarkAllRead_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := notifsTestServer(t, user.User{}, false)
	req := httptest.NewRequest(http.MethodPost, "/notifications/mark-all-read", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}
