package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

// searchIntegrationServer builds an Echo wired to a real pool, with a session
// for "Tester". Used by the search integration tests below.
func searchIntegrationServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(user.Seeded(), clock.System{}, src, idgen.Crypto{})
	h := New(a, &assistant.Recorder{}, pool)
	e := echo.New()
	e.Renderer = tplpkg.New()
	signed := user.User{ID: "test-user", DisplayName: "Tester"}
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, signed)
			return next(c)
		}
	})
	e.GET("/search", h.SearchGet)
	return e
}

// TestIntegration_Search_FindsAccountByName runs the real query against a
// seeded DB: it pulls the alphabetically-first active account, searches for a
// substring of its name, and asserts that account surfaces in the rendered
// results under an "Accounts" section.
//
// Requires DATABASE_URL pointing at a DB seeded via db/seed/demo.sql.
func TestIntegration_Search_FindsAccountByName(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL unset; integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("connect %s: %v", dbURL, err)
	}
	defer pool.Close()

	accs, err := dbq.New(pool).ListAccounts(ctx) // active, ORDER BY name
	if err != nil || len(accs) == 0 {
		t.Skipf("no accounts in DB; seed via db/seed/demo.sql first (err=%v)", err)
	}
	name := accs[0].Name
	// A short prefix is enough to match and, since both ListAccounts and
	// SearchAccounts order by name, accs[0] stays within the LIMIT 10 window.
	needle := name
	if len(needle) > 4 {
		needle = needle[:4]
	}

	e := searchIntegrationServer(t, pool)
	req := httptest.NewRequest(http.MethodGet, "/search?q="+url_QueryEscape(needle), nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ">Accounts<") {
		t.Errorf("results page has no Accounts section for q=%q", needle)
	}
	if !strings.Contains(body, htmlEscape(name)) {
		t.Errorf("results page missing account %q for q=%q", name, needle)
	}
}

// TestIntegration_Search_NoMatchShowsEmptyState: a query that cannot match
// any entity renders the explicit empty state, not a blank page.
func TestIntegration_Search_NoMatchShowsEmptyState(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL unset; integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("connect %s: %v", dbURL, err)
	}
	defer pool.Close()

	e := searchIntegrationServer(t, pool)
	req := httptest.NewRequest(http.MethodGet, "/search?q=zzqx-no-such-term-9173", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No matches") {
		t.Error("expected empty-state 'No matches' for a non-matching query")
	}
}

// url_QueryEscape / htmlEscape are tiny local helpers to avoid extra imports
// colliding with the package's existing test files.
func url_QueryEscape(s string) string {
	return strings.ReplaceAll(s, " ", "+")
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
