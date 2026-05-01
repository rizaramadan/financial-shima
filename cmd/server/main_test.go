package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServer_GETLogin_Returns200(t *testing.T) {
	t.Parallel()
	e := newServer()

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestServer_UnknownPath_Returns404(t *testing.T) {
	t.Parallel()
	e := newServer()

	req := httptest.NewRequest(http.MethodGet, "/this-route-does-not-exist", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServer_AppliesSecurityHeaders(t *testing.T) {
	t.Parallel()
	e := newServer()

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

// TestServer_HandlerOverRealTCP_ServesLoginForm exercises the assembled
// handler tree over an actual TCP listener (httptest.NewServer wraps it in a
// real *http.Server). This is the regression guard against changes that pass
// in-memory ServeHTTP but break the real wire path — e.g., a future encoder
// that depends on hijacking. It also verifies the body actually contains the
// form (a 200 with empty body would otherwise pass).
//
// Note: this does NOT exercise main()'s e.Start(addr) bind path or the
// signal-driven shutdown — those aren't reachable from a test without
// extracting main into a run() helper. The named test scope is "handler over
// real TCP," not "the bootstrap binds."
func TestServer_HandlerOverRealTCP_ServesLoginForm(t *testing.T) {
	t.Parallel()
	e := newServer()
	ts := httptest.NewServer(e)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login via real listener: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	wantSubstrings := []string{`<form`, `name="identifier"`, `Send code via Telegram`}
	for _, s := range wantSubstrings {
		if !strings.Contains(string(body), s) {
			t.Errorf("body missing %q", s)
		}
	}
}
