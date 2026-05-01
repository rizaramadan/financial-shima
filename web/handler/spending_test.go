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

func spendingTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
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
	e.GET("/spending", h.SpendingGet)
	return e
}

func TestSpendingGet_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := spendingTestServer(t, user.User{}, false)
	req := httptest.NewRequest(http.MethodGet, "/spending", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}

func TestSpendingGet_NilPool_RendersFilterFormAndEmptyState(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := spendingTestServer(t, signedIn, true)
	req := httptest.NewRequest(http.MethodGet, "/spending", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<h1>Spending</h1>`) {
		t.Error("body missing h1")
	}
	if !strings.Contains(body, `name="from"`) || !strings.Contains(body, `name="to"`) {
		t.Error("body missing filter inputs")
	}
	if !strings.Contains(body, "No money_out transactions in this range") {
		t.Error("body missing empty-state subtitle (nil pool)")
	}
	if !strings.Contains(body, `class="bell"`) {
		t.Error("bell missing on authenticated spending page")
	}
}

func TestSpendingGet_QueryParamsParsed(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := spendingTestServer(t, signedIn, true)
	req := httptest.NewRequest(http.MethodGet,
		"/spending?from=2026-01-01&to=2026-04-30", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `value="2026-01-01"`) {
		t.Error("body did not echo from=")
	}
	if !strings.Contains(body, `value="2026-04-30"`) {
		t.Error("body did not echo to=")
	}
}

func TestSpendingGet_InvalidDate_FallsBackToDefault(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := spendingTestServer(t, signedIn, true)
	req := httptest.NewRequest(http.MethodGet, "/spending?from=garbage", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "garbage") {
		t.Error("body echoed invalid date verbatim; should have fallen back")
	}
}
