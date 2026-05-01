package main

import (
	"net/http"
	"net/http/httptest"
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

func TestServer_POSTLogin_NotYetWired_Returns405(t *testing.T) {
	t.Parallel()
	e := newServer()

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// Phase 1 boundary: POST /login is NOT served. When Phase 2 wires it,
	// this test goes red and forces an intentional update. Echo's router
	// returns 405 for a registered path with an unregistered method.
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d (Phase 1 should not yet serve POST /login)",
			rec.Code, http.StatusMethodNotAllowed)
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

func TestServer_SetsConservativeTimeouts(t *testing.T) {
	t.Parallel()
	e := newServer()

	if e.Server.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want > 0 (Slowloris defense)", e.Server.ReadHeaderTimeout)
	}
	if e.Server.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout = %v, want > 0", e.Server.ReadTimeout)
	}
	if e.Server.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout = %v, want > 0", e.Server.WriteTimeout)
	}
	if e.Server.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want > 0", e.Server.IdleTimeout)
	}
}
