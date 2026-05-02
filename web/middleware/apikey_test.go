package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

const testAPIKey = "test-key-secret-do-not-use-in-prod"

// newAPIKeyEchoApp builds an Echo with the APIKey middleware armed and a
// single GET /api/v1/ping route that responds with 200 + a tiny JSON body.
// Every test in this file routes through it, so the middleware is exercised
// in the same way it will be in production.
func newAPIKeyEchoApp(t *testing.T) *echo.Echo {
	t.Helper()
	e := echo.New()
	e.Use(APIKey(testAPIKey))
	e.GET("/api/v1/ping", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	})
	return e
}

func TestAPIKey_MissingHeader_Returns401WithJSONErrorBody(t *testing.T) {
	t.Parallel()
	e := newAPIKeyEchoApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v; body = %q", err, rec.Body.String())
	}
	if body.Error == "" {
		t.Errorf("error code missing; body = %q", rec.Body.String())
	}
	if body.Message == "" {
		t.Errorf("message missing; body = %q", rec.Body.String())
	}
}

func TestAPIKey_WrongKey_Returns401(t *testing.T) {
	t.Parallel()
	e := newAPIKeyEchoApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	req.Header.Set("x-api-key", "wrong-key")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %q", rec.Code, rec.Body.String())
	}
}

func TestAPIKey_CorrectKey_PassesThrough(t *testing.T) {
	t.Parallel()
	e := newAPIKeyEchoApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	req.Header.Set("x-api-key", testAPIKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
}

func TestAPIKey_EmptyHeaderTreatedAsMissing(t *testing.T) {
	t.Parallel()
	e := newAPIKeyEchoApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	req.Header.Set("x-api-key", "")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAPIKey_PanicsOnEmptyConfiguredKey(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when constructing APIKey(\"\"), got none")
		}
	}()
	APIKey("")
}

// Property-style: a key of different length than expected must never match,
// regardless of contents. Guards against a regression where length-equal but
// content-different inputs accidentally validated.
func TestAPIKey_DifferingLengthsAlwaysReject(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",                                    // empty (covered separately)
		"x",                                   // shorter
		testAPIKey[:len(testAPIKey)-1],        // 1 short
		testAPIKey + "x",                      // 1 long
		strings.Repeat("a", len(testAPIKey)),  // same length, different content
		strings.Repeat("\x00", len(testAPIKey)), // null bytes, same length
	}
	e := newAPIKeyEchoApp(t)
	for _, k := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
		if k != "" {
			req.Header.Set("x-api-key", k)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("key %q: status = %d, want 401", k, rec.Code)
		}
	}
}
