package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

func TestParseRupiah(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		want   int64
		wantOK bool
	}{
		{"10000000", 10000000, true},
		{"10.000.000", 10000000, true},
		{"Rp 1.500.000", 1500000, true},
		{"3,000,000", 3000000, true},
		{"0", 0, false},
		{"", 0, false},
		{"abc", 0, false},
		{"-5", 5, true}, // sign stripped; digits only
	}
	for _, c := range cases {
		got, ok := parseRupiah(c.in)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("parseRupiah(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// loanRouteServer wires the borrower loan routes with a nil DB so the
// no-session redirect paths can be asserted without Postgres.
func loanRouteServer(t *testing.T) *echo.Echo {
	t.Helper()
	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(user.Seeded(), clock.Fixed{T: t0}, src, idgen.Fixed{Value: "tok"})
	h := New(a, &assistant.Recorder{}, nil)
	e := echo.New()
	e.Renderer = tplpkg.New()
	e.GET("/loan/:id/login", h.LoanLoginGet)
	e.GET("/loan/:id", h.LoanViewGet)
	e.POST("/loan/:id/payments", h.LoanPaymentPost)
	return e
}

// TestLoanViewGet_RedirectsWithoutSession: the borrower view is gated — no
// loan_session cookie means a bounce to that loan's login, never the data.
func TestLoanViewGet_RedirectsWithoutSession(t *testing.T) {
	t.Parallel()
	e := loanRouteServer(t)
	id := uuid.NewString()

	req := httptest.NewRequest(http.MethodGet, "/loan/"+id, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/loan/"+id+"/login" {
		t.Errorf("Location = %q, want /loan/%s/login", loc, id)
	}
}

// TestLoanPaymentPost_RedirectsWithoutSession: submitting without a session
// must not create anything — it bounces to login.
func TestLoanPaymentPost_RedirectsWithoutSession(t *testing.T) {
	t.Parallel()
	e := loanRouteServer(t)
	id := uuid.NewString()

	req := httptest.NewRequest(http.MethodPost, "/loan/"+id+"/payments", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/loan/"+id+"/login" {
		t.Errorf("Location = %q, want login", loc)
	}
}

// TestLoanLoginGet_BadIDRedirectsHome: a non-UUID loan id is never a real
// loan, so the login route bounces home rather than rendering a form.
func TestLoanLoginGet_BadIDRedirectsHome(t *testing.T) {
	t.Parallel()
	e := loanRouteServer(t)

	req := httptest.NewRequest(http.MethodGet, "/loan/not-a-uuid/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}
