package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServer_LoginRouteRegistered(t *testing.T) {
	e := newServer()

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `name="identifier"`) {
		t.Errorf("response body missing identifier input")
	}
}
