package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	dbmigrate "github.com/rizaramadan/financial-shima/db"
	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	"github.com/rizaramadan/financial-shima/web/handler"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newPivotFiltersTestEcho(t *testing.T, h *handler.Handlers, u user.User, signedIn bool) *echo.Echo {
	t.Helper()
	e := echo.New()
	e.Renderer = template.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if signedIn {
				c.Set(mw.SessionContextKey, u)
			}
			return next(c)
		}
	})
	e.GET("/pivot", h.PivotGet)
	e.POST("/pivot/filters", h.PivotFiltersPost)
	return e
}

func pivotFiltersTestHandler(t *testing.T, pool *pgxpool.Pool) *handler.Handlers {
	t.Helper()
	src := strings.NewReader("                                                                ")
	a := auth.New(user.Seeded(), clock.Fixed{T: time.Now()}, src, idgen.Fixed{Value: "tok"})
	return handler.New(a, &assistant.Recorder{}, pool)
}

func TestPivotFiltersPost_Unauthenticated_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	h := pivotFiltersTestHandler(t, nil)
	e := newPivotFiltersTestEcho(t, h, user.User{}, false)
	req := httptest.NewRequest(http.MethodPost, "/pivot/filters",
		strings.NewReader(`{"filters":"preset=month"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
}

func TestPivotFiltersPost_NilPool_AcceptsValidBody(t *testing.T) {
	t.Parallel()
	h := pivotFiltersTestHandler(t, nil)
	e := newPivotFiltersTestEcho(t, h, user.User{ID: "u-1", DisplayName: "Tester"}, true)
	for _, body := range []string{
		`{"filters":"preset=month&sort=amount"}`,
		`{"filters":""}`,
		`{}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/pivot/filters", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("body %s: status = %d, want 204", body, rec.Code)
		}
	}
}

func TestPivotFiltersPost_NilPool_RejectsBadInput(t *testing.T) {
	t.Parallel()
	h := pivotFiltersTestHandler(t, nil)
	e := newPivotFiltersTestEcho(t, h, user.User{ID: "u-1", DisplayName: "Tester"}, true)
	for _, body := range []string{
		`{bad json`,
		`{"filters":"evil=1"}`,
		`{"filters":"%zz"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/pivot/filters", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestPivotGet_NilPool_EmbedsEmptySavedFilters(t *testing.T) {
	t.Parallel()
	h := pivotFiltersTestHandler(t, nil)
	e := newPivotFiltersTestEcho(t, h, user.User{ID: "u-1", DisplayName: "Tester"}, true)
	req := httptest.NewRequest(http.MethodGet, "/pivot", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="pivot-saved">""`) {
		t.Error("pivot page missing empty saved-filters embed")
	}
}

// pivotFiltersTestUser inserts a throwaway user and returns its uuid string.
// Skips when DATABASE_URL is unset, matching the project pattern.
func pivotFiltersTestUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ident := fmt.Sprintf("test-pivot-%d", time.Now().UnixNano())
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (display_name, telegram_identifier) VALUES ('Pivot Test', $1) RETURNING id::text`,
		ident).Scan(&id); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id::text = $1`, id)
	})
	return id
}

// TestIntegration_PivotFilters_SaveAndLoadRoundTrip verifies filters saved
// via POST are embedded back into GET /pivot for the same user, updates
// overwrite, and a different user sees nothing.
func TestIntegration_PivotFilters_SaveAndLoadRoundTrip(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	if err := dbmigrate.Migrate(ctx, pool, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := pivotFiltersTestHandler(t, pool)
	uid := pivotFiltersTestUser(t, pool)
	other := pivotFiltersTestUser(t, pool)
	e := newPivotFiltersTestEcho(t, h, user.User{ID: uid, DisplayName: "Tester"}, true)

	post := func(filters string) int {
		req := httptest.NewRequest(http.MethodPost, "/pivot/filters",
			strings.NewReader(`{"filters":`+quoteJSON(t, filters)+`}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}
	getSaved := func(e *echo.Echo) string {
		req := httptest.NewRequest(http.MethodGet, "/pivot", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /pivot status = %d, want 200", rec.Code)
		}
		return rec.Body.String()
	}

	if code := post(`preset=month&sort=amount`); code != http.StatusNoContent {
		t.Fatalf("POST status = %d, want 204", code)
	}
	if body := getSaved(e); !strings.Contains(body, `preset=month`) {
		t.Error("GET /pivot missing saved filters after POST")
	}
	// Update overwrites.
	if code := post(`type=money_in`); code != http.StatusNoContent {
		t.Fatalf("POST status = %d, want 204", code)
	}
	body := getSaved(e)
	if !strings.Contains(body, `type=money_in`) {
		t.Error("GET /pivot missing updated filters")
	}
	if strings.Contains(body, `preset=month`) {
		t.Error("GET /pivot still shows stale filters after update")
	}
	// Other user is unaffected.
	eOther := newPivotFiltersTestEcho(t, h, user.User{ID: other, DisplayName: "Other"}, true)
	if body := getSaved(eOther); !strings.Contains(body, `id="pivot-saved">""`) {
		t.Error("other user should see empty saved filters")
	}
	// Unknown user id is rejected, not stored.
	eBad := newPivotFiltersTestEcho(t, h, user.User{ID: "not-a-uuid", DisplayName: "Bad"}, true)
	req := httptest.NewRequest(http.MethodPost, "/pivot/filters",
		strings.NewReader(`{"filters":"preset=month"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	eBad.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad uuid status = %d, want 400", rec.Code)
	}
}

func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
