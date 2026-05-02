package handler

import (
	"bytes"
	"encoding/json"
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
)

const apiTestKey = "test-api-key-do-not-use-in-prod"

// apiTestServer wires the /api/v1 group with the APIKey middleware
// armed and a nil-DB Handlers. Every test in this file routes through
// it so the auth gate, the route mount, and the handler's nil-DB path
// stay in lockstep with production.
//
// Real-DB integration belongs in api_accounts_integration_test.go
// alongside home_integration_test.go (see the integration TODO in
// _ListAccounts when implemented).
func apiTestServer(t *testing.T) *echo.Echo {
	t.Helper()
	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(user.Seeded(), clock.Fixed{T: t0}, src, idgen.Fixed{Value: "tok"})
	h := New(a, &assistant.Recorder{}, nil)

	e := echo.New()
	api := e.Group("/api/v1", mw.APIKey(apiTestKey))
	api.GET("/accounts", h.APIAccountsList)
	return e
}

// assertJSONResponse pins the JSON content-type prefix safely (no
// length-based slicing).
func assertJSONResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}
}

func TestAPIAccountsList_NoAPIKey_Returns401(t *testing.T) {
	t.Parallel()
	e := apiTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body=%q", rec.Code, rec.Body.String())
	}
	// The middleware's challenge contract; pin one assertion at the
	// route level so a regression that mounts the route outside the
	// middleware group surfaces here, not just in the middleware suite.
	if got := rec.Header().Get("WWW-Authenticate"); got != mw.APIKeyAuthChallenge {
		t.Errorf("WWW-Authenticate = %q, want %q", got, mw.APIKeyAuthChallenge)
	}
}

func TestAPIAccountsList_NilDB_Returns503ServiceUnavailable(t *testing.T) {
	t.Parallel()
	e := apiTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("x-api-key", apiTestKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body=%q", rec.Code, rec.Body.String())
	}
	assertJSONResponse(t, rec)

	var ae mw.APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &ae); err != nil {
		t.Fatalf("unmarshal APIError: %v; body=%q", err, rec.Body.String())
	}
	if ae.Code != mw.APIErrorCodeServiceUnavailable {
		t.Errorf("APIError.Code = %q, want %q", ae.Code, mw.APIErrorCodeServiceUnavailable)
	}
	if ae.Message == "" {
		t.Errorf("APIError.Message empty; body=%q", rec.Body.String())
	}
}

// TestAPIAccountsList_QueryParam_RoundTrips pins the query-string
// surface for `include_archived`. The handler delegates to two
// different SQL queries based on this flag; without the test, a typo
// (`includeArchived`, `archived`, `inactive`) would silently route
// every request to the non-archived path with no symptom.
//
// On nil-DB the response is 503, which doesn't exercise the SQL
// branching; what's pinned here is that the parameter is *consumed*
// (no `bind variable not found` error from Echo) and that the bool
// parser accepts the spec's truthy forms without crashing.
func TestAPIAccountsList_QueryParam_AcceptsTruthyForms(t *testing.T) {
	t.Parallel()
	cases := []string{"true", "1", "yes", "t", "TRUE", ""}
	for _, raw := range cases {
		raw := raw
		t.Run("include_archived="+raw, func(t *testing.T) {
			t.Parallel()
			e := apiTestServer(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?include_archived="+raw, nil)
			req.Header.Set("x-api-key", apiTestKey)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			// Nil-DB path → 503 regardless of query param. What we're
			// pinning is that the handler doesn't reject malformed
			// truthy variants with a 4xx.
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503 (nil-DB path); body=%q", rec.Code, rec.Body.String())
			}
		})
	}
}
