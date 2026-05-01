package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

// TestIntegration_HomeGet_RendersAccountsAndPosFromDB verifies the home
// view reads from the wired pool and groups Pos by currency. Skipped when
// DATABASE_URL is unset (matches the project pattern).
func TestIntegration_HomeGet_RendersAccountsAndPosFromDB(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	a := auth.New(user.Seeded(), clock.System{},
		// any io.Reader will do — auth isn't exercised on this path.
		(strings.NewReader("                ")),
		idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, pool)

	e := echo.New()
	e.Renderer = template.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		// Inject a fake "logged in" user so HomeGet doesn't redirect.
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, user.User{
				ID: "test", DisplayName: "Tester",
			})
			return next(c)
		}
	})
	e.GET("/", h.HomeGet)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Accounts") {
		t.Errorf("body missing Accounts section; body:\n%s", body)
	}
	// Seed creates "Tabungan Mobil" (idr) and "Tabungan Emas" (gold-g).
	if !strings.Contains(body, "Tabungan Mobil") {
		t.Error(`body missing seeded pos "Tabungan Mobil" — seed not applied?`)
	}
	if !strings.Contains(body, "gold-g") {
		t.Error(`body missing currency group "gold-g"`)
	}
	// Sign-out form must be present.
	if !strings.Contains(body, `action="/logout"`) {
		t.Error("body missing logout form")
	}
}
