package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func dispatch(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	e := newServer()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestServer_GETLogin_Returns200(t *testing.T) {
	t.Parallel()
	rec := dispatch(t, http.MethodGet, "/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestServer_POSTLogin_Returns501NotImplemented pins the Phase-1 contract:
// the form's action="/login" target is registered, so the form-server
// contract isn't fictional, but no real handler runs yet. When Phase 2
// wires OTP issuing this test goes red and forces an intentional update.
func TestServer_POSTLogin_Returns501NotImplemented(t *testing.T) {
	t.Parallel()
	rec := dispatch(t, http.MethodPost, "/login")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d (Phase 1 stub)",
			rec.Code, http.StatusNotImplemented)
	}
}

func TestServer_UnknownPath_Returns404(t *testing.T) {
	t.Parallel()
	rec := dispatch(t, http.MethodGet, "/this-route-does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestServer_AppliesSecurityHeaders verifies the middleware applies on every
// response, not just /login. A 404 is a real response and a real surface for
// header-stripping bugs.
func TestServer_AppliesSecurityHeaders(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'",
	}
	for _, path := range []string{"/login", "/this-route-does-not-exist"} {
		rec := dispatch(t, http.MethodGet, path)
		for k, v := range want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s on %s = %q, want %q", k, path, got, v)
			}
		}
	}
}

// TestServer_HandlerOverRealTCP_ServesLoginForm exercises the assembled
// handler tree over an actual TCP listener via httptest.NewServer. It does
// NOT exercise main()'s e.Start(addr) bind path or signal-driven shutdown —
// those are not reachable from a test without extracting main into a
// run() helper. Body content asserted so a 200 with empty body fails.
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
	wantSubstrings := []string{`<form`, `name="identifier"`, `Send code`}
	for _, s := range wantSubstrings {
		if !strings.Contains(string(body), s) {
			t.Errorf("body missing %q", s)
		}
	}
}
