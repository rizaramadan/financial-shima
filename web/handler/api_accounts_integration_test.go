package handler_test

import (
	"context"
	"encoding/json"
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
)

const apiIntegrationKey = "integration-test-key-not-for-prod"

// setupAPIIntegrationHandler builds a real-DB handler. Skips the test
// if DATABASE_URL is unset (matches the home_integration_test.go
// pattern). Returns the handler and the pool so the caller can both
// run requests AND seed/inspect rows directly.
//
// The caller is responsible for `pool.Close()` (some tests want to
// close it early to force errors).
func setupAPIIntegrationHandler(t *testing.T) (*handler.Handlers, *pgxpool.Pool) {
	t.Helper()
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
	a := auth.New(user.Seeded(), clock.System{},
		strings.NewReader("                "),
		idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, pool)
	return h, pool
}

func newAPIIntegrationEcho(t *testing.T, h *handler.Handlers) *echo.Echo {
	t.Helper()
	e := echo.New()
	api := e.Group("/api/v1", mw.APIKey(apiIntegrationKey))
	api.GET("/accounts", h.APIAccountsList)
	return e
}

// TestIntegration_APIAccountsList_ReturnsRowFromDB exercises the happy
// path against a real Postgres. Verifies:
//   - The wired pool is actually read (vs. nil-DB or stub).
//   - The seeded row is in the response.
//   - Per-row JSON shape matches APIAccount: id is a parseable UUID,
//     archived is bool, created_at is RFC3339 and recent.
func TestIntegration_APIAccountsList_ReturnsRowFromDB(t *testing.T) {
	h, pool := setupAPIIntegrationHandler(t)
	defer pool.Close()

	// Insert a uniquely-named account so we can find it amid whatever
	// the seed leaves around.
	stamp := uuid.NewString()[:8]
	acctName := "Integ-Test " + stamp
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO accounts (name) VALUES ($1)`, acctName); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	e := newAPIIntegrationEcho(t, h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("x-api-key", apiIntegrationKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	var accounts []handler.APIAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, rec.Body.String())
	}

	var found *handler.APIAccount
	for i := range accounts {
		if accounts[i].Name == acctName {
			found = &accounts[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("inserted account %q not in response; got %d accounts", acctName, len(accounts))
	}

	// Per-row JSON shape — pinned only here, since unit tests can't
	// produce real DB rows.
	if _, err := uuid.Parse(found.ID); err != nil {
		t.Errorf("APIAccount.ID = %q is not a parseable uuid: %v", found.ID, err)
	}
	if found.Archived {
		t.Errorf("APIAccount.Archived = true; want false (inserted without archived flag)")
	}
	if delta := time.Since(found.CreatedAt); delta > time.Minute || delta < -time.Second {
		t.Errorf("APIAccount.CreatedAt = %v, expected within ±1min of now (delta=%v)", found.CreatedAt, delta)
	}
}

// TestIntegration_APIAccountsList_ArchivedFilter pins that:
//   - Default response excludes archived rows.
//   - ?include_archived=true response includes them.
//   - The row's Archived field is correctly serialized as true.
func TestIntegration_APIAccountsList_ArchivedFilter(t *testing.T) {
	h, pool := setupAPIIntegrationHandler(t)
	defer pool.Close()

	stamp := uuid.NewString()[:8]
	archName := "Arch-Integ " + stamp
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO accounts (name, archived) VALUES ($1, true)`, archName); err != nil {
		t.Fatalf("insert archived: %v", err)
	}

	e := newAPIIntegrationEcho(t, h)

	// Default — archived row must be absent.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("x-api-key", apiIntegrationKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("default status = %d, want 200", rec.Code)
	}
	var defaultRows []handler.APIAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &defaultRows); err != nil {
		t.Fatalf("unmarshal default: %v", err)
	}
	for _, a := range defaultRows {
		if a.Name == archName {
			t.Errorf("default response includes archived row %q", archName)
		}
	}

	// ?include_archived=true — archived row must be present, with Archived=true.
	reqArch := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?include_archived=true", nil)
	reqArch.Header.Set("x-api-key", apiIntegrationKey)
	recArch := httptest.NewRecorder()
	e.ServeHTTP(recArch, reqArch)
	if recArch.Code != http.StatusOK {
		t.Fatalf("include_archived status = %d, want 200", recArch.Code)
	}
	var archRows []handler.APIAccount
	if err := json.Unmarshal(recArch.Body.Bytes(), &archRows); err != nil {
		t.Fatalf("unmarshal arch: %v", err)
	}
	var found *handler.APIAccount
	for i := range archRows {
		if archRows[i].Name == archName {
			found = &archRows[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("include_archived response missing %q; got %d accounts", archName, len(archRows))
	}
	if !found.Archived {
		t.Errorf("APIAccount.Archived = false; want true (row was inserted as archived)")
	}
}

// TestIntegration_APIAccountsList_DBError_Returns500 forces a DB error
// by closing the pool before the request, then verifies the error path:
//   - Status 500.
//   - Body decodes to APIError.
//   - Code is APIErrorCodeInternal.
//   - Message does NOT leak internal detail (no "pool", "sql", "closed").
func TestIntegration_APIAccountsList_DBError_Returns500(t *testing.T) {
	h, pool := setupAPIIntegrationHandler(t)
	pool.Close() // close immediately — subsequent queries fail.

	e := newAPIIntegrationEcho(t, h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("x-api-key", apiIntegrationKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%q", rec.Code, rec.Body.String())
	}
	var ae mw.APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &ae); err != nil {
		t.Fatalf("unmarshal APIError: %v; body=%q", err, rec.Body.String())
	}
	if ae.Code != mw.APIErrorCodeInternal {
		t.Errorf("APIError.Code = %q, want %q", ae.Code, mw.APIErrorCodeInternal)
	}
	// Message must not leak internals (pool state, SQL errors, etc.).
	for _, leaked := range []string{"pool", "sql", "closed"} {
		if strings.Contains(strings.ToLower(ae.Message), leaked) {
			t.Errorf("APIError.Message leaks %q: %q", leaked, ae.Message)
		}
	}
}
