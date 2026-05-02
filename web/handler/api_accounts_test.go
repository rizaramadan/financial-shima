package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// apiTestServer wires the /api/v1 group with the APIKey middleware armed
// and a nil-DB Handlers — the unauth path is delegated to middleware,
// and the empty-DB path returns []. Real-DB integration belongs in a
// separate _integration_test alongside home_integration_test.go.
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

func TestAPIAccountsList_NoAPIKey_Returns401(t *testing.T) {
	t.Parallel()
	e := apiTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body=%q", rec.Code, rec.Body.String())
	}
	// The middleware's response shape is the contract; pin one assertion
	// at the route level so a regression that mounts the route outside
	// the middleware group surfaces here, not just in the middleware
	// suite.
	if got := rec.Header().Get("WWW-Authenticate"); got != mw.APIKeyAuthChallenge {
		t.Errorf("WWW-Authenticate = %q, want %q", got, mw.APIKeyAuthChallenge)
	}
}

func TestAPIAccountsList_AuthenticatedNilDB_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	e := apiTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("x-api-key", apiTestKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:16] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}
	// Must be a JSON array, not null. Empty result = [].
	var accounts []APIAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, rec.Body.String())
	}
	if accounts == nil {
		t.Errorf("accounts is nil, want empty slice; body=%q", rec.Body.String())
	}
	if len(accounts) != 0 {
		t.Errorf("len(accounts) = %d, want 0; got %+v", len(accounts), accounts)
	}
}
