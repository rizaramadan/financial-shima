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
// single GET /api/v1/ping route that responds with 200 plus a stable JSON
// body. The body literal is asserted by the pass-through test to prove the
// downstream handler ran (vs. the middleware returning 200 itself).
func newAPIKeyEchoApp(t *testing.T) *echo.Echo {
	t.Helper()
	e := echo.New()
	e.Use(APIKey(testAPIKey))
	e.GET("/api/v1/ping", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	})
	return e
}

func decodeAPIError(t *testing.T, body []byte) APIError {
	t.Helper()
	var ae APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("unmarshal APIError: %v; body=%q", err, string(body))
	}
	return ae
}

// assertReject pins every rejection's shape: 401, JSON content-type,
// `WWW-Authenticate: ApiKey` (RFC 7235), non-empty message. Each test
// then asserts the specific [APIError.Error] code on top.
func assertReject(t *testing.T, rec *httptest.ResponseRecorder) APIError {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "ApiKey" {
		t.Errorf("WWW-Authenticate = %q, want %q", got, "ApiKey")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}
	ae := decodeAPIError(t, rec.Body.Bytes())
	if ae.Message == "" {
		t.Errorf("APIError.Message is empty; body=%q", rec.Body.String())
	}
	return ae
}

func TestAPIKey_MissingHeader_RejectsWithMissingKeyCode(t *testing.T) {
	t.Parallel()
	e := newAPIKeyEchoApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	ae := assertReject(t, rec)
	if ae.Error != APIErrorCodeMissingKey {
		t.Errorf("APIError.Error = %q, want %q", ae.Error, APIErrorCodeMissingKey)
	}
}

func TestAPIKey_WrongKey_RejectsWithInvalidKeyCode(t *testing.T) {
	t.Parallel()
	e := newAPIKeyEchoApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	req.Header.Set("x-api-key", "wrong-key")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	ae := assertReject(t, rec)
	if ae.Error != APIErrorCodeInvalidKey {
		t.Errorf("APIError.Error = %q, want %q", ae.Error, APIErrorCodeInvalidKey)
	}
}

func TestAPIKey_MultipleHeaders_RejectsWithMultipleKeysCode(t *testing.T) {
	t.Parallel()
	e := newAPIKeyEchoApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	// First value would validate alone; second value's presence is what's rejected.
	req.Header.Add("x-api-key", testAPIKey)
	req.Header.Add("x-api-key", "second-value")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	ae := assertReject(t, rec)
	if ae.Error != APIErrorCodeMultipleKeys {
		t.Errorf("APIError.Error = %q, want %q", ae.Error, APIErrorCodeMultipleKeys)
	}
}

func TestAPIKey_CorrectKey_PassesThroughAndDownstreamRuns(t *testing.T) {
	t.Parallel()
	e := newAPIKeyEchoApp(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	req.Header.Set("x-api-key", testAPIKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	// Body must come from the downstream handler — proves next(c) ran.
	if !strings.Contains(rec.Body.String(), `"ok":"true"`) {
		t.Errorf("body = %q, want to contain downstream {\"ok\":\"true\"}", rec.Body.String())
	}
	// Pass-through must NOT set the auth challenge header.
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate on pass-through = %q, want empty", got)
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

// TestAPIKey_RejectsMismatch sweeps the constant-time comparator across
// shapes that mimic the practical attack surface: shorter/longer than
// expected, same length with different content, same length with null
// bytes. Each case asserts [APIErrorCodeInvalidKey], and runs as its own
// subtest so a CI failure isolates the input that regressed.
//
// The empty-string case is intentionally omitted — that path is exercised
// by [TestAPIKey_MissingHeader_RejectsWithMissingKeyCode] with the
// distinct [APIErrorCodeMissingKey] code.
func TestAPIKey_RejectsMismatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  string
	}{
		{"shorter", "x"},
		{"one_byte_short", testAPIKey[:len(testAPIKey)-1]},
		{"one_byte_long", testAPIKey + "x"},
		{"same_length_all_a", strings.Repeat("a", len(testAPIKey))},
		{"same_length_null_bytes", strings.Repeat("\x00", len(testAPIKey))},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newAPIKeyEchoApp(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
			req.Header.Set("x-api-key", tc.key)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			ae := assertReject(t, rec)
			if ae.Error != APIErrorCodeInvalidKey {
				t.Errorf("APIError.Error = %q, want %q (key=%q)", ae.Error, APIErrorCodeInvalidKey, tc.key)
			}
		})
	}
}
