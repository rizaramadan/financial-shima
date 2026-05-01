package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
// Lifecycle test budgets. Both deadlines must accommodate Shutdown's grace
// period plus run() return on a loaded CI runner under -race.
const (
	bindBudget     = 2 * time.Second
	shutdownBudget = 2 * time.Second
)

// TestRun_StopsCleanlyOnContextCancel exercises run()'s lifecycle: bind on a
// real OS-chosen port, cancel the context, expect a clean nil return. This
// is the regression guard against future shutdown bugs in run() — drain
// races, missed close, deadlocks — that no in-memory test can surface.
func TestRun_StopsCleanlyOnContextCancel(t *testing.T) {
	t.Parallel()

	// :0 asks the OS for a free port; we don't care which.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}
	// Small race window between Close and run's rebind — acceptable in tests.

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, newServer(), addr)
	}()

	// Poll the port until it accepts, rather than sleep-and-hope.
	deadline := time.Now().Add(bindBudget)
	bound := false
	for time.Now().Before(deadline) {
		c, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if dialErr == nil {
			c.Close()
			bound = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !bound {
		t.Fatalf("server never bound within %v", bindBudget)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil after context cancel", err)
		}
	case <-time.After(shutdownBudget):
		t.Fatalf("run did not return within %v of context cancel", shutdownBudget)
	}
}

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
	// One structural check is enough: the wire path's job is to prove the
	// real net/http stack serves the same page the in-memory tests verify.
	// Substring duplication of <form / name=identifier / button copy would
	// re-assert facts the DOM tests own.
	if !strings.Contains(string(body), `action="/login"`) {
		t.Error(`body missing action="/login"; wire path may have served the wrong page`)
	}
}
