package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestLoginGet_RendersFormWithIdentifierInput(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := LoginGet(c); err != nil {
		t.Fatalf("LoginGet returned error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	wantSubstrings := []string{
		`<form`,
		`method="post"`,
		`action="/login"`,
		`name="identifier"`,
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q\nbody:\n%s", s, body)
		}
	}
}
