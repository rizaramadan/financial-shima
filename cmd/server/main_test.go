package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// TestServer_PostLogin_RejectedByEchoMethodRouting pins the Phase-1 boundary:
// only GET /login is registered, so Echo's router answers POST /login with
// 405 and an Allow: GET header. When Phase 2 wires POST, this test goes red
// and forces an intentional update. The 405 itself is Echo's behavior, not
// ours — the test documents intent.
func TestServer_PostLogin_RejectedByEchoMethodRouting(t *testing.T) {
	t.Parallel()
	e := newServer()

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

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

// TestServer_TimeoutsMatchDeclaredConstants asserts exact configured values,
// not just non-zero — a regression that drops a timeout to 1ms (or removes
// one assignment) fails here, where the previous "> 0" assertion would not.
func TestServer_TimeoutsMatchDeclaredConstants(t *testing.T) {
	t.Parallel()
	e := newServer()

	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"ReadHeaderTimeout", e.Server.ReadHeaderTimeout, readHeaderTimeout},
		{"ReadTimeout", e.Server.ReadTimeout, readTimeout},
		{"WriteTimeout", e.Server.WriteTimeout, writeTimeout},
		{"IdleTimeout", e.Server.IdleTimeout, idleTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
			}
		})
	}
}

// TestServer_BindsAndServesRealHTTP exercises the actual net/http stack — a
// regression that breaks the listener wiring (or moves timeout assignments
// after Start) ships green against e.ServeHTTP-based tests, but fails here.
func TestServer_BindsAndServesRealHTTP(t *testing.T) {
	t.Parallel()
	e := newServer()
	ts := httptest.NewServer(e)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login via real listener: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Errorf("drain body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
