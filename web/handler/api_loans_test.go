package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// apiLoanTestServer wires the /api/v1 loan routes with the APIKey
// middleware armed and a nil-DB Handlers, mirroring apiTestServer so
// the auth gate and the nil-DB path stay in lockstep with production.
func apiLoanTestServer(t *testing.T) *echo.Echo {
	t.Helper()
	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(user.Seeded(), clock.Fixed{T: t0}, src, idgen.Fixed{Value: "tok"})
	h := New(a, &assistant.Recorder{}, nil)
	e := echo.New()
	api := e.Group("/api/v1", mw.APIKey(apiTestKey))
	api.POST("/loans", h.APILoanCreate)
	api.GET("/loans/:id", h.APILoanGet)
	api.POST("/loans/:id/payments", h.APILoanPaymentCreate)
	api.POST("/loans/:id/payments/:sid/cancel", h.APILoanPaymentCancel)
	api.POST("/loan-submissions/:sid/approve", h.APILoanApprove)
	api.POST("/loan-submissions/:sid/reject", h.APILoanReject)
	return e
}

func TestCalcLoanProgress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		target      int64
		balance     int64
		outstanding int64
		pct         int
	}{
		{"fresh loan", 10000000, 0, 10000000, 0},
		{"partial", 10000000, 3000000, 7000000, 30},
		{"paid off", 10000000, 10000000, 0, 100},
		{"overpaid clamps", 10000000, 12000000, 0, 100},
		{"negative balance clamps", 10000000, -500, 10000000, 0},
		{"zero target", 0, 0, 0, 0},
	}
	for _, tc := range cases {
		out, pct := calcLoanProgress(tc.target, tc.balance)
		if out != tc.outstanding || pct != tc.pct {
			t.Errorf("%s: calcLoanProgress(%d,%d) = (%d,%d), want (%d,%d)",
				tc.name, tc.target, tc.balance, out, pct, tc.outstanding, tc.pct)
		}
	}
}

func TestValidateLoanSetupInput(t *testing.T) {
	t.Parallel()
	good := LoanSetupInput{
		Name: "KPR", BorrowerName: "Budi", Amount: 10000000,
		AccountID: uuid.New(), FundedFrom: "Bank", Username: "budi", Password: "secret123",
	}
	if v := validateLoanSetupInput(good); len(v) != 0 {
		t.Errorf("good input violations = %v, want none", v)
	}
	bad := good
	bad.Name = "  "
	if v := validateLoanSetupInput(bad); len(v) == 0 {
		t.Error("empty name: want violation, got none")
	}
	bad = good
	bad.Amount = 0
	if v := validateLoanSetupInput(bad); len(v) == 0 {
		t.Error("zero amount: want violation, got none")
	}
	bad = good
	bad.Password = ""
	if v := validateLoanSetupInput(bad); len(v) == 0 {
		t.Error("empty password: want violation, got none")
	}
}

func TestValidateLoanPaymentInput(t *testing.T) {
	t.Parallel()
	day, _ := time.Parse("2006-01-02", "2026-06-04")
	good := LoanPaymentInput{PayerName: "Budi", Amount: 500000, EffectiveDate: day}
	if v := validateLoanPaymentInput(good); len(v) != 0 {
		t.Errorf("good input violations = %v, want none", v)
	}
	bad := good
	bad.PayerName = ""
	if v := validateLoanPaymentInput(bad); len(v) == 0 {
		t.Error("empty payer: want violation, got none")
	}
	bad = good
	bad.Amount = -1
	if v := validateLoanPaymentInput(bad); len(v) == 0 {
		t.Error("negative amount: want violation, got none")
	}
	bad = good
	bad.EffectiveDate = time.Time{}
	if v := validateLoanPaymentInput(bad); len(v) == 0 {
		t.Error("zero date: want violation, got none")
	}
}

// TestAPILoans_NilDB503: every loan endpoint without a DB wired must
// fail loud (503 service_unavailable), never silently succeed.
func TestAPILoans_NilDB503(t *testing.T) {
	t.Parallel()
	e := apiLoanTestServer(t)
	id := "00000000-0000-0000-0000-000000000000"
	cases := []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/v1/loans", `{"name":"x"}`},
		{http.MethodGet, "/api/v1/loans/" + id, ""},
		{http.MethodPost, "/api/v1/loans/" + id + "/payments", `{"payer_name":"x"}`},
		{http.MethodPost, "/api/v1/loans/" + id + "/payments/" + id + "/cancel", ""},
		{http.MethodPost, "/api/v1/loan-submissions/" + id + "/approve", ""},
		{http.MethodPost, "/api/v1/loan-submissions/" + id + "/reject", `{}`},
	}
	for _, tc := range cases {
		var req *http.Request
		if tc.body != "" {
			req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		}
		req.Header.Set("x-api-key", apiTestKey)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status = %d, want 503; body=%q", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}
