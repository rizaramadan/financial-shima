package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

// loanIntegrationServer wires every loan route plus a middleware that injects
// the given family user (so the admin routes see a session). Borrower routes
// rely on the loan_session cookie carried on each request, not this user.
// resolveSeededUsers mirrors cmd/server.resolveUserIDs: it replaces each
// seeded user's slug ID with the real DB uuid so ledger notification inserts
// (which key on users.id) succeed, exactly as they do in production.
func resolveSeededUsers(t *testing.T, pool *pgxpool.Pool) []user.User {
	t.Helper()
	q := dbq.New(pool)
	ctx := context.Background()
	out := make([]user.User, 0, 2)
	for _, u := range user.Seeded() {
		if row, err := q.GetUserByTelegramIdentifier(ctx, u.TelegramIdentifier); err == nil {
			u.ID = uuid.UUID(row.ID.Bytes).String()
		}
		out = append(out, u)
	}
	return out
}

func loanIntegrationServer(t *testing.T, pool *pgxpool.Pool, famUser user.User) *echo.Echo {
	t.Helper()
	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(resolveSeededUsers(t, pool), clock.System{}, src, idgen.Crypto{})
	h := New(a, &assistant.Recorder{}, pool)
	e := echo.New()
	e.Renderer = tplpkg.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, famUser)
			return next(c)
		}
	})
	e.POST("/loans/new", h.LoanSetupPost)
	e.POST("/loan-submissions/:sid/approve", h.LoanApprovePost)
	e.POST("/loan-submissions/:sid/reject", h.LoanRejectPost)
	e.POST("/loan/:id/login", h.LoanLoginPost)
	e.GET("/loan/:id", h.LoanViewGet)
	e.POST("/loan/:id/payments", h.LoanPaymentPost)
	e.POST("/loan/:id/payments/:sid/cancel", h.LoanCancelPost)
	return e
}

func loanTestSetup(t *testing.T) (*pgxpool.Pool, user.User, string) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL unset; integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	// A real users.id for created_by / decided_by FKs.
	var uid pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users LIMIT 1`).Scan(&uid); err != nil {
		pool.Close()
		t.Skipf("no users seeded: %v", err)
	}
	var accID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM accounts WHERE NOT archived LIMIT 1`).Scan(&accID); err != nil {
		pool.Close()
		t.Skipf("no accounts seeded: %v", err)
	}
	famUser := user.User{ID: uuid.UUID(uid.Bytes).String(), DisplayName: "Tester"}
	return pool, famUser, uuid.UUID(accID.Bytes).String()
}

// createLoan drives LoanSetupPost and returns the new pos id.
func createLoan(t *testing.T, e *echo.Echo, accID, name, username, password string) string {
	t.Helper()
	form := url.Values{
		"name":          {name},
		"borrower_name": {"Borrower " + name},
		"amount":        {"10000000"},
		"account_id":    {accID},
		"funded_from":   {"Transfer"},
		"username":      {username},
		"password":      {password},
	}
	req := httptest.NewRequest(http.MethodPost, "/loans/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("setup status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	id := strings.TrimPrefix(loc, "/pos/")
	if id == loc || id == "" {
		t.Fatalf("setup redirect = %q, want /pos/<id>", loc)
	}
	return id
}

// borrowerLogin drives LoanLoginPost and returns the loan_session cookie.
func borrowerLogin(t *testing.T, e *echo.Echo, posID, username, password string) *http.Cookie {
	t.Helper()
	form := url.Values{"username": {username}, "password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/loan/"+posID+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("borrower login status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == LoanSessionCookie && ck.Value != "" {
			return ck
		}
	}
	t.Fatal("borrower login set no loan_session cookie")
	return nil
}

// TestIntegration_LoanLifecycle runs setup → borrower view → submit → approve
// and asserts the balance/outstanding move exactly as the model says.
func TestIntegration_LoanLifecycle(t *testing.T) {
	pool, famUser, accID := loanTestSetup(t)
	defer pool.Close()
	e := loanIntegrationServer(t, pool, famUser)
	stamp := uuid.NewString()[:8]

	posID := createLoan(t, e, accID, "IT Loan "+stamp, "borrower_"+stamp, "secret123")

	// After fund + disburse, repaid balance is 0 and outstanding is the full target.
	ctx := context.Background()
	q := dbq.New(pool)
	pid := pgtype.UUID{Bytes: uuid.MustParse(posID), Valid: true}
	if bal, err := q.GetPosCashBalance(ctx, pid); err != nil || bal != 0 {
		t.Fatalf("post-setup balance = %d (err=%v), want 0", bal, err)
	}

	cookie := borrowerLogin(t, e, posID, "borrower_"+stamp, "secret123")

	// Borrower view shows the loan, gated by the session cookie.
	req := httptest.NewRequest(http.MethodGet, "/loan/"+posID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("borrower view status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Outstanding") {
		t.Error("borrower view missing Outstanding")
	}

	// Submit a 3,000,000 repayment.
	form := url.Values{"payer_name": {"Borrower"}, "amount": {"3000000"}, "date": {"2026-06-04"}, "note": {"installment"}}
	req = httptest.NewRequest(http.MethodPost, "/loan/"+posID+"/payments", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("submit status = %d, want 303", rec.Code)
	}

	pend, err := q.ListPendingLoanSubmissionsByPos(ctx, pid)
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending submissions = %d (err=%v), want 1", len(pend), err)
	}
	subID := uuid.UUID(pend[0].ID.Bytes).String()

	// Approve it.
	req = httptest.NewRequest(http.MethodPost, "/loan-submissions/"+subID+"/approve", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("approve status = %d, want 303", rec.Code)
	}

	// Balance is now the repayment; submission is approved with a txn linked.
	if bal, err := q.GetPosCashBalance(ctx, pid); err != nil || bal != 3000000 {
		t.Fatalf("post-approve balance = %d (err=%v), want 3000000", bal, err)
	}
	sub, err := q.GetLoanSubmission(ctx, pend[0].ID)
	if err != nil || string(sub.Status) != "approved" || !sub.TransactionID.Valid {
		t.Fatalf("submission after approve: status=%v txnValid=%v err=%v", sub.Status, sub.TransactionID.Valid, err)
	}

	// Approving again is a no-op (idempotent guard) — balance unchanged.
	req = httptest.NewRequest(http.MethodPost, "/loan-submissions/"+subID+"/approve", nil)
	e.ServeHTTP(httptest.NewRecorder(), req)
	if bal, err := q.GetPosCashBalance(ctx, pid); err != nil || bal != 3000000 {
		t.Fatalf("double-approve changed balance to %d (err=%v), want 3000000", bal, err)
	}
}

// TestIntegration_LoanSessionScoping is the security-critical check: a session
// for loan A must not grant access to loan B by editing the URL.
func TestIntegration_LoanSessionScoping(t *testing.T) {
	pool, famUser, accID := loanTestSetup(t)
	defer pool.Close()
	e := loanIntegrationServer(t, pool, famUser)
	stamp := uuid.NewString()[:8]

	posA := createLoan(t, e, accID, "Loan A "+stamp, "ua_"+stamp, "secret123")
	posB := createLoan(t, e, accID, "Loan B "+stamp, "ub_"+stamp, "secret123")
	cookieA := borrowerLogin(t, e, posA, "ua_"+stamp, "secret123")

	// Loan A's cookie on Loan B's route must NOT return data — it redirects.
	req := httptest.NewRequest(http.MethodGet, "/loan/"+posB, nil)
	req.AddCookie(cookieA)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("cross-loan access status = %d, want 303 (redirect to login)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/loan/"+posB+"/login" {
		t.Errorf("cross-loan redirect = %q, want loan B login", loc)
	}

	// Sanity: the same cookie DOES work on Loan A.
	req = httptest.NewRequest(http.MethodGet, "/loan/"+posA, nil)
	req.AddCookie(cookieA)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("own-loan access status = %d, want 200", rec.Code)
	}
}
