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
// pattern). Registers pool.Close via t.Cleanup — callers do not need
// defer pool.Close(). Tests that force a DB error by closing the pool
// early can still do so; the t.Cleanup close is a no-op on pgxpool.Pool.
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
	t.Cleanup(pool.Close)
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

// assertAPIJSON pins the Content-Type on an API response. Defined here
// because assertJSONResponse in api_accounts_test.go is in package
// handler (internal test package), not accessible from package handler_test.
func assertAPIJSON(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}
}

// TestIntegration_APIAccountsList_ReturnsRowFromDB exercises the happy
// path against a real Postgres. Verifies:
//   - The wired pool is actually read (vs. nil-DB or stub).
//   - The seeded row is in the response.
//   - Response body is a JSON array ([] not null) — guards against
//     a var-declaration regression that would flip the wire format.
//   - Per-row JSON shape: id parses as UUID, archived is bool,
//     created_at is recent.
func TestIntegration_APIAccountsList_ReturnsRowFromDB(t *testing.T) {
	h, pool := setupAPIIntegrationHandler(t)

	stamp := uuid.NewString()[:8]
	acctName := "Integ-Test " + stamp
	insertCtx, insertCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer insertCancel()
	if _, err := pool.Exec(insertCtx,
		`INSERT INTO accounts (name) VALUES ($1)`, acctName); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanCtx, `DELETE FROM accounts WHERE name = $1`, acctName)
	})

	e := newAPIIntegrationEcho(t, h)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("x-api-key", apiIntegrationKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	assertAPIJSON(t, rec)

	// Response must be a JSON array, never null — pins the make([]APIAccount, 0, ...)
	// contract stated in the handler godoc.
	body := rec.Body.Bytes()
	if len(body) == 0 || body[0] != '[' {
		prefix := body
		if len(prefix) > 20 {
			prefix = prefix[:20]
		}
		t.Errorf("response body is not a JSON array; got prefix %q", prefix)
	}

	var accounts []handler.APIAccount
	if err := json.Unmarshal(body, &accounts); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, body)
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

	stamp := uuid.NewString()[:8]
	archName := "Arch-Integ " + stamp
	insertCtx, insertCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer insertCancel()
	if _, err := pool.Exec(insertCtx,
		`INSERT INTO accounts (name, archived) VALUES ($1, true)`, archName); err != nil {
		t.Fatalf("insert archived: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanCtx, `DELETE FROM accounts WHERE name = $1`, archName)
	})

	e := newAPIIntegrationEcho(t, h)

	// Default — archived row must be absent.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("x-api-key", apiIntegrationKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("default status = %d, want 200", rec.Code)
	}
	assertAPIJSON(t, rec)
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
	assertAPIJSON(t, recArch)
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
// by closing the pool before the request, then verifies both query
// branches (default and include_archived=true):
//   - Status 500.
//   - Body decodes to APIError with code APIErrorCodeInternal.
//   - Message is exactly "failed to list accounts" — allowlist, not a
//     denylist, so any leak or wording drift fails immediately.
func TestIntegration_APIAccountsList_DBError_Returns500(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{"default_branch", "/api/v1/accounts"},
		{"archived_branch", "/api/v1/accounts?include_archived=true"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h, pool := setupAPIIntegrationHandler(t)
			pool.Close() // force error; t.Cleanup close is a no-op on pgxpool.Pool.

			e := newAPIIntegrationEcho(t, h)
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			req.Header.Set("x-api-key", apiIntegrationKey)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%q", rec.Code, rec.Body.String())
			}
			assertAPIJSON(t, rec)
			var ae mw.APIError
			if err := json.Unmarshal(rec.Body.Bytes(), &ae); err != nil {
				t.Fatalf("unmarshal APIError: %v; body=%q", err, rec.Body.String())
			}
			if ae.Code != mw.APIErrorCodeInternal {
				t.Errorf("APIError.Code = %q, want %q", ae.Code, mw.APIErrorCodeInternal)
			}
			const wantMsg = "failed to list accounts"
			if ae.Message != wantMsg {
				t.Errorf("APIError.Message = %q, want %q", ae.Message, wantMsg)
			}
		})
	}
}
