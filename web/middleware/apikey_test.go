package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

const testAPIKey = "test-key-secret-do-not-use-in-prod"

// newAPIKeyEchoApp builds an Echo with the APIKey middleware armed and a
// single GET /api/v1/ping route that responds with 200 plus a stable JSON
// body. The body is decoded by the pass-through test to prove the
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
// `WWW-Authenticate: ApiKey` (RFC 7235), non-empty message. The caller
// then asserts the specific [APIError.Code] on top.
func assertReject(t *testing.T, rec *httptest.ResponseRecorder) APIError {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != APIKeyAuthChallenge {
		t.Errorf("WWW-Authenticate = %q, want %q", got, APIKeyAuthChallenge)
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

// TestAPIKey_MissingHeader_RejectsWithMissingKeyCode covers both halves
// of the godoc's "missing or empty value" disjunction:
//   - "absent": the request carries no x-api-key header at all.
//   - "present_but_empty": the request carries `x-api-key:` with no value.
//
// Both must route to [APIErrorCodeMissingKey] per the godoc on [APIKey].
func TestAPIKey_MissingHeader_RejectsWithMissingKeyCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		setHeader  bool
		headerVal  string
	}{
		{"absent", false, ""},
		{"present_but_empty", true, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newAPIKeyEchoApp(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
			if tc.setHeader {
				req.Header.Set("x-api-key", tc.headerVal)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			ae := assertReject(t, rec)
			if ae.Code != APIErrorCodeMissingKey {
				t.Errorf("APIError.Code = %q, want %q", ae.Code, APIErrorCodeMissingKey)
			}
		})
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
	if ae.Code != APIErrorCodeInvalidKey {
		t.Errorf("APIError.Code = %q, want %q", ae.Code, APIErrorCodeInvalidKey)
	}
}

// TestAPIKey_MultipleHeaders_RejectsWithMultipleCode covers two cases
// jointly to pin the precedence: the multi-header check fires before
// the value-validation checks. The "valid first key + extra value"
// case proves a request that *would* otherwise validate is still
// rejected; the "two wrong values" case proves the multi-check is
// reported even when the value-validation would also have failed —
// the structural problem wins.
func TestAPIKey_MultipleHeaders_RejectsWithMultipleCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		first  string
		second string
	}{
		{"first_value_would_validate_alone", testAPIKey, "second-value"},
		{"both_values_invalid", "wrong-1", "wrong-2"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newAPIKeyEchoApp(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
			req.Header.Add("x-api-key", tc.first)
			req.Header.Add("x-api-key", tc.second)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			ae := assertReject(t, rec)
			if ae.Code != APIErrorCodeMultipleKeyHeaders {
				t.Errorf("APIError.Code = %q, want %q", ae.Code, APIErrorCodeMultipleKeyHeaders)
			}
		})
	}
}

// TestAPIKey_AcceptsCanonicalAndShoutyHeaderCasing pins that the
// implementation goes through Go's canonicalizing accessors (Header.Get
// / Header.Values) rather than direct map lookup. Without this guard,
// a future maintainer could replace `Header.Get(APIKeyHeader)` with
// `Header["x-api-key"]` and silently break every client that doesn't
// happen to send the exact lowercase form.
func TestAPIKey_AcceptsCanonicalAndShoutyHeaderCasing(t *testing.T) {
	t.Parallel()
	cases := []string{
		"x-api-key",
		"X-Api-Key",
		"X-API-KEY",
	}
	for _, headerName := range cases {
		headerName := headerName
		t.Run(headerName, func(t *testing.T) {
			t.Parallel()
			e := newAPIKeyEchoApp(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
			req.Header.Set(headerName, testAPIKey)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
			}
		})
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
	// Decode the response — proves both that next(c) ran AND survives
	// future encoder formatting changes (e.g. spaces after colons).
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal pass-through body: %v; body=%q", err, rec.Body.String())
	}
	if body["ok"] != "true" {
		t.Errorf("body[\"ok\"] = %q, want %q (full body=%q)", body["ok"], "true", rec.Body.String())
	}
	// Pass-through must NOT set the auth challenge header.
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate on pass-through = %q, want empty", got)
	}
}

func TestAPIKey_PanicsOnEmptyConfiguredKey(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when constructing APIKey(\"\"), got none")
		}
		// Pin two halves of the deploy-time signal: the package/function
		// prefix (so a rename surfaces in this test) and the inner
		// sentinel (so a generic panic message swap surfaces too). If a
		// future refactor breaks either, the operator still gets a
		// diagnostic that names the problem.
		s, ok := r.(string)
		if !ok {
			t.Errorf("panic value type = %T, want string", r)
			return
		}
		const wantPrefix = "middleware.APIKey:"
		const wantInner = "expected key is empty"
		if !strings.HasPrefix(s, wantPrefix) {
			t.Errorf("panic message = %q, want prefix %q", s, wantPrefix)
		}
		if !strings.Contains(s, wantInner) {
			t.Errorf("panic message = %q, want it to contain %q", s, wantInner)
		}
	}()
	APIKey("")
}

// TestAPIKey_RejectsMismatch sweeps the constant-time comparator across
// shapes that mimic the practical attack surface: shorter/longer than
// expected, same length with different content, same length with null
// bytes. Each case asserts [APIErrorCodeInvalidKey] and runs as its own
// subtest so a CI failure isolates the regressing input.
//
// The empty-string case is intentionally omitted — the missing-header
// path is exercised by [TestAPIKey_MissingHeader_RejectsWithMissingKeyCode]
// with the distinct [APIErrorCodeMissingKey] code.
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
			if ae.Code != APIErrorCodeInvalidKey {
				t.Errorf("APIError.Code = %q, want %q (key=%q)", ae.Code, APIErrorCodeInvalidKey, tc.key)
			}
		})
	}
}

// ExampleAPIKey shows the canonical wiring: read the secret from an
// environment variable, then mount the middleware on the API route group.
//
// The trailing fmt.Println + // Output: lets `go test` execute this
// example so the documented snippet stays in sync with the package's
// real surface (echo.Group, APIKey, etc.).
func ExampleAPIKey() {
	e := echo.New()
	apiKey := "your-shared-secret-from-env" // os.Getenv("LLM_API_KEY") in production
	apiV1 := e.Group("/api/v1", APIKey(apiKey))
	apiV1.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	// Run with: e.Start(":8080")
	// Test with: curl -H "x-api-key: your-shared-secret-from-env" http://localhost:8080/api/v1/health
	fmt.Println("middleware mounted")
	// Output: middleware mounted
}
