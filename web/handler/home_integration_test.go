package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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

func newHomeTestEcho(t *testing.T, h *handler.Handlers) *echo.Echo {
	t.Helper()
	e := echo.New()
	e.Renderer = template.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, user.User{ID: "test", DisplayName: "Tester"})
			return next(c)
		}
	})
	e.GET("/", h.HomeGet)
	return e
}

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
		strings.NewReader("                "),
		idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, pool)

	e := newHomeTestEcho(t, h)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// Structural assertions: <h2>Accounts</h2>, <h2>Pos &mdash; idr</h2>,
	// <h2>Pos &mdash; gold-g</h2> all present, with idr appearing before
	// gold-g (spec — IDR is the operator's primary currency).
	if !strings.Contains(body, `<h2>Accounts</h2>`) {
		t.Error("body missing <h2>Accounts</h2>")
	}
	idrIdx := strings.Index(body, `<h2>Pos &mdash; idr</h2>`)
	goldIdx := strings.Index(body, `<h2>Pos &mdash; gold-g</h2>`)
	if idrIdx < 0 || goldIdx < 0 {
		t.Fatalf("missing pos group h2: idr=%d gold=%d; body:\n%s", idrIdx, goldIdx, body)
	}
	if idrIdx > goldIdx {
		t.Errorf("idr group at %d should precede gold-g at %d", idrIdx, goldIdx)
	}
	if !strings.Contains(body, "Tabungan Mobil") {
		t.Error(`body missing seeded pos "Tabungan Mobil" — seed not applied?`)
	}
	if !strings.Contains(body, `action="/logout"`) {
		t.Error("body missing logout form")
	}
	if strings.Contains(body, `class="alert"`) {
		t.Error("body has error alert; should not on happy path")
	}
}

// TestIntegration_HomeGet_ArchivedRowsFiltered: archived accounts/pos
// must NOT appear in the default home view (relies on ListAccounts and
// ListPos applying `WHERE NOT archived` — pinned here so a regression
// to ORDER-only queries breaks the test).
func TestIntegration_HomeGet_ArchivedRowsFiltered(t *testing.T) {
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

	// Insert + archive a uniquely-named account and pos. The seed leaves
	// other rows around; we only assert OUR archived rows are absent.
	stamp := uuid.NewString()[:8]
	archAcc := "Archived Acct " + stamp
	archPos := "Archived Pos " + stamp

	if _, err := pool.Exec(ctx, `INSERT INTO accounts (name, archived) VALUES ($1, true)`, archAcc); err != nil {
		t.Fatalf("insert archived account: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO pos (name, currency, archived) VALUES ($1, 'idr', true)`, archPos,
	); err != nil {
		t.Fatalf("insert archived pos: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE name = $1`, archAcc)
		_, _ = pool.Exec(ctx, `DELETE FROM pos WHERE name = $1 AND currency = 'idr'`, archPos)
	})

	a := auth.New(user.Seeded(), clock.System{},
		strings.NewReader("                "),
		idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, pool)
	e := newHomeTestEcho(t, h)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, archAcc) {
		t.Errorf("archived account %q rendered on home", archAcc)
	}
	if strings.Contains(body, archPos) {
		t.Errorf("archived pos %q rendered on home", archPos)
	}
}

// TestIntegration_HomeGet_DBErrorRendersLoadError closes the pool before
// the request and verifies the page renders 200 with the error alert
// rather than 500. Pins the graceful-degrade behavior on DB outage.
func TestIntegration_HomeGet_DBErrorRendersLoadError(t *testing.T) {
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
	pool.Close() // simulate DB unavailable

	a := auth.New(user.Seeded(), clock.System{},
		strings.NewReader("                "),
		idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, pool)
	e := newHomeTestEcho(t, h)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (graceful degrade)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="alert"`) {
		t.Error("body missing error alert; DB-error path should render LoadError state")
	}
	// "load once seed data lands" must NOT appear — that's the empty-state
	// message, not the error message.
	if strings.Contains(body, "load once seed data lands") {
		t.Error("DB-error rendered the empty-state message instead of the error alert")
	}
}
