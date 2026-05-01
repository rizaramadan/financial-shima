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

func txnsTestServer(t *testing.T, signedIn user.User, signedInOK bool) *echo.Echo {
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
	e.GET("/transactions", h.TransactionsGet)
	return e
}

func TestTransactionsGet_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e := txnsTestServer(t, user.User{}, false)
	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
}

func TestTransactionsGet_NilPool_RendersFilterFormWithDefaultRange(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := txnsTestServer(t, signedIn, true)

	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<h1>Transactions</h1>`) {
		t.Error("body missing h1")
	}
	if !strings.Contains(body, `name="from"`) || !strings.Contains(body, `name="to"`) {
		t.Error("body missing filter inputs")
	}
	if !strings.Contains(body, "No transactions in this range") {
		t.Error("body missing empty-state subtitle (nil pool)")
	}
	// Default range echoes today and 30 days ago into the form values.
	today := time.Now().Format("2006-01-02")
	if !strings.Contains(body, `value="`+today+`"`) {
		t.Errorf("body missing today's date in form: %q", today)
	}
	// Bell badge is empty since no DB.
	if !strings.Contains(body, `class="bell"`) {
		t.Error("bell missing on authenticated transactions page")
	}
}

func TestTransactionsGet_QueryParamsParsed(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := txnsTestServer(t, signedIn, true)

	req := httptest.NewRequest(http.MethodGet,
		"/transactions?from=2026-04-01&to=2026-04-30", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="2026-04-01"`) {
		t.Error("body did not echo from=2026-04-01")
	}
	if !strings.Contains(body, `value="2026-04-30"`) {
		t.Error("body did not echo to=2026-04-30")
	}
}

func TestTransactionsGet_InvalidQueryParams_FallsBackToDefault(t *testing.T) {
	t.Parallel()
	signedIn := user.User{ID: "u-1", DisplayName: "Tester"}
	e := txnsTestServer(t, signedIn, true)

	req := httptest.NewRequest(http.MethodGet, "/transactions?from=garbage&to=", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (invalid date should silently fall back)", rec.Code)
	}
	// Should not contain "garbage" anywhere.
	if strings.Contains(rec.Body.String(), "garbage") {
		t.Error("body echoed invalid date verbatim; should have fallen back to default")
	}
}
