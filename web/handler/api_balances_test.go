package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

func apiBalancesTestServer(t *testing.T) *echo.Echo {
	t.Helper()
	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(user.Seeded(), clock.Fixed{T: t0}, src, idgen.Fixed{Value: "tok"})
	h := New(a, &assistant.Recorder{}, nil)
	e := echo.New()
	api := e.Group("/api/v1", mw.APIKey(apiTestKey))
	api.GET("/balances", h.APIBalancesGet)
	return e
}

func TestAPIBalances_Returns401_NoAPIKey(t *testing.T) {
	t.Parallel()
	e := apiBalancesTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/balances", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAPIBalances_NilDB_Returns503(t *testing.T) {
	t.Parallel()
	e := apiBalancesTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/balances", nil)
	req.Header.Set("x-api-key", apiTestKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != mw.APIErrorCodeServiceUnavailable {
		t.Errorf("error code = %q, want %q", body["error"], mw.APIErrorCodeServiceUnavailable)
	}
}
