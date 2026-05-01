package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestServer_POSTLogin_UnknownUser_Returns200 confirms POST /login is now a
// real handler (Phase 2 replaced the 501 stub). Unknown identifier re-renders
// the form with an error rather than throwing.
func TestServer_POSTLogin_UnknownUser_Returns200(t *testing.T) {
	t.Parallel()
	e := newServer()
	form := url.Values{"identifier": {"@nobody"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "User not found") {
		t.Error("body missing 'User not found'")
	}
}

func TestServer_GETVerify_NoID_Redirects(t *testing.T) {
	t.Parallel()
	rec := dispatch(t, http.MethodGet, "/verify")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
}

func TestServer_GETHome_NoSession_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	rec := dispatch(t, http.MethodGet, "/")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("Location = %q, want /login", rec.Header().Get("Location"))
	}
}

func TestServer_UnknownPath_Returns404(t *testing.T) {
	t.Parallel()
	rec := dispatch(t, http.MethodGet, "/this-route-does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

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

const (
	bindBudget     = 2 * time.Second
	shutdownBudget = 2 * time.Second
)

func TestRun_StopsCleanlyOnContextCancel(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, newServer(), addr)
	}()

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
			t.Errorf("run returned %v, want nil", err)
		}
	case <-time.After(shutdownBudget):
		t.Fatalf("run did not return within %v", shutdownBudget)
	}
}

func TestServer_HandlerOverRealTCP_ServesLoginForm(t *testing.T) {
	t.Parallel()
	e := newServer()
	ts := httptest.NewServer(e)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), `action="/login"`) {
		t.Error(`body missing action="/login"`)
	}
}
