//go:build ignore

// e2e_api_loans proves the /api/v1 loans surface:
// setup → detail → payment → approve → cancel → reject, with
// 404/409 handling and loan-scoped cancels.
//
//	export DATABASE_URL=postgres://postgreuser@localhost:5432/financial_shima?sslmode=disable
//	go run ./scripts/e2e_api_loans.go
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	"github.com/rizaramadan/financial-shima/web/handler"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

const apiKey = "test-api-key-for-e2e"

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		die("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		die("pool: %v", err)
	}
	defer pool.Close()

	users := user.Seeded()
	for i, u := range users {
		var idStr string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE telegram_identifier = $1`,
			u.TelegramIdentifier,
		).Scan(&idStr); err != nil {
			die("resolve user %q: %v", u.TelegramIdentifier, err)
		}
		users[i].ID = idStr
	}
	a := auth.New(users, clock.System{}, rand.Reader, idgen.Crypto{})
	h := handler.New(a, &assistant.Recorder{}, pool)

	e := echo.New()
	e.Renderer = template.New()
	api := e.Group("/api/v1", mw.APIKey(apiKey))
	api.POST("/accounts", h.APIAccountsCreate)
	api.POST("/loans", h.APILoanCreate)
	api.GET("/loans/:id", h.APILoanGet)
	api.POST("/loans/:id/payments", h.APILoanPaymentCreate)
	api.POST("/loans/:id/payments/:sid/cancel", h.APILoanPaymentCancel)
	api.POST("/loan-submissions/:sid/approve", h.APILoanApprove)
	api.POST("/loan-submissions/:sid/reject", h.APILoanReject)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	pfx := fmt.Sprintf("e2e-loan-%d", stamp)
	defer cleanup(pool, pfx)

	today := time.Now().Format("2006-01-02")

	// ── Seed: one cash account ────────────────────────────────
	var acc map[string]any
	postJSON(srv.URL+"/api/v1/accounts", mustJSON(map[string]string{"name": pfx + "-acc"}), 201, &acc)
	accID := acc["id"].(string)

	setup := func(name, username string) map[string]any {
		var loan map[string]any
		postJSON(srv.URL+"/api/v1/loans", mustJSON(map[string]any{
			"name": name, "borrower_name": "Borrower " + name,
			"amount": 10000000, "account_id": accID,
			"funded_from": "Bank " + pfx, "username": username, "password": "secret123",
		}), 201, &loan)
		return loan
	}
	loanGet := func(id string) map[string]any {
		var loan map[string]any
		getJSON(srv.URL+"/api/v1/loans/"+id, &loan)
		return loan
	}
	pay := func(loanID string, amount int64, note string) map[string]any {
		var sub map[string]any
		postJSON(srv.URL+"/api/v1/loans/"+loanID+"/payments", mustJSON(map[string]any{
			"payer_name": "Borrower", "amount": amount,
			"effective_date": today, "note": note,
		}), 201, &sub)
		return sub
	}

	// ── L1 setup → 201 + detail shape ────────────────────────
	loan := setup(pfx+"-A", pfx+"-user-a")
	loanID := loan["id"].(string)
	{
		if loan["balance"].(float64) != 0 || loan["outstanding"].(float64) != 10000000 ||
			loan["pct_repaid"].(float64) != 0 {
			die("L1 progress = balance %v outstanding %v pct %v (want 0/10000000/0)",
				loan["balance"], loan["outstanding"], loan["pct_repaid"])
		}
		subs, ok := loan["submissions"].([]any)
		if !ok || len(subs) != 0 {
			die("L1 submissions = %v (want [])", loan["submissions"])
		}
		links, _ := loan["_links"].(map[string]any)
		for _, k := range []string{"self", "pos", "transactions", "balances"} {
			if links == nil || links[k] == nil {
				die("L1 _links missing %q: %v", k, loan["_links"])
			}
		}
		if acts, _ := loan["_actions"].([]any); len(acts) == 0 {
			die("L1 _actions empty")
		}
		pass("L1 setup → 201 + balance 0/outstanding 10000000/pct 0 + _links/_actions")
	}

	// ── L2 GET detail ────────────────────────────────────────
	{
		got := loanGet(loanID)
		if got["outstanding"].(float64) != 10000000 || got["balance"].(float64) != 0 {
			die("L2 detail wrong: %v", got)
		}
		pass("L2 GET detail → 200 + full outstanding")
	}

	// ── L3 payment → 201 pending + affordances ───────────────
	var s1 map[string]any
	{
		s1 = pay(loanID, 3000000, "installment 1")
		if s1["status"].(string) != "pending" {
			die("L3 status = %v (want pending)", s1["status"])
		}
		if s1["effective_date"].(string) != today {
			die("L3 date = %v (want %s)", s1["effective_date"], today)
		}
		acts, _ := s1["_actions"].([]any)
		if len(acts) != 3 {
			die("L3 pending _actions = %d (want approve/reject/cancel)", len(acts))
		}
		got := loanGet(loanID)
		if got["balance"].(float64) != 0 || len(got["submissions"].([]any)) != 1 {
			die("L3 pending moves no money: %v", got)
		}
		pass("L3 payment → 201 pending + 3 affordances, balance still 0")
	}
	s1ID := s1["id"].(string)

	// ── L4 approve → balance moves ───────────────────────────
	{
		var appr map[string]any
		postJSON(srv.URL+"/api/v1/loan-submissions/"+s1ID+"/approve", nil, 200, &appr)
		if appr["status"].(string) != "approved" {
			die("L4 status = %v (want approved)", appr["status"])
		}
		if appr["transaction_id"] == nil || appr["transaction_id"].(string) == "" {
			die("L4 approved without transaction_id: %v", appr)
		}
		if acts, _ := appr["_actions"].([]any); len(acts) != 0 {
			die("L4 decided _actions = %d (want none)", len(acts))
		}
		got := loanGet(loanID)
		if got["balance"].(float64) != 3000000 || got["outstanding"].(float64) != 7000000 ||
			got["pct_repaid"].(float64) != 30 {
			die("L4 progress = %v/%v/%v (want 3000000/7000000/30)",
				got["balance"], got["outstanding"], got["pct_repaid"])
		}
		pass("L4 approve → 200 + txn linked, balance 3000000/outstanding 7000000/pct 30")
	}

	// ── L5 double approve → 409, balance unchanged ───────────
	{
		s, b := doReq("POST", srv.URL+"/api/v1/loan-submissions/"+s1ID+"/approve", nil)
		if s != 409 || !isErr(b, "conflict") {
			die("L5 double approve: status=%d body=%s (want 409/conflict)", s, string(b))
		}
		if got := loanGet(loanID); got["balance"].(float64) != 3000000 {
			die("L5 balance moved to %v (want 3000000)", got["balance"])
		}
		pass("L5 re-approve → 409 conflict, balance unchanged")
	}

	// ── L6 payment → cancel → 409 on retry ───────────────────
	{
		s2 := pay(loanID, 1000000, "installment 2")
		s2ID := s2["id"].(string)
		var cancelled map[string]any
		postJSON(srv.URL+"/api/v1/loans/"+loanID+"/payments/"+s2ID+"/cancel", nil, 200, &cancelled)
		if cancelled["status"].(string) != "cancelled" {
			die("L6 status = %v (want cancelled)", cancelled["status"])
		}
		s, b := doReq("POST", srv.URL+"/api/v1/loans/"+loanID+"/payments/"+s2ID+"/cancel", nil)
		if s != 409 || !isErr(b, "conflict") {
			die("L6 re-cancel: status=%d body=%s (want 409/conflict)", s, string(b))
		}
		if got := loanGet(loanID); got["balance"].(float64) != 3000000 {
			die("L6 cancel moved money: balance %v", got["balance"])
		}
		pass("L6 cancel → 200 cancelled, retry → 409, balance unchanged")
	}

	// ── L7 cross-loan cancel → 404 (loan scoping) ────────────
	{
		loanB := setup(pfx+"-B", pfx+"-user-b")
		s3 := pay(loanID, 500000, "scoped")
		s, b := doReq("POST", srv.URL+"/api/v1/loans/"+loanB["id"].(string)+"/payments/"+s3["id"].(string)+"/cancel", nil)
		if s != 404 || !isErr(b, "not_found") {
			die("L7 cross-loan cancel: status=%d body=%s (want 404/not_found)", s, string(b))
		}
		// The submission is still pending on loan A — cancel it there.
		var cancelled map[string]any
		postJSON(srv.URL+"/api/v1/loans/"+loanID+"/payments/"+s3["id"].(string)+"/cancel", nil, 200, &cancelled)
		pass("L7 cross-loan cancel → 404 not_found (scoping holds)")
	}

	// ── L8 payment → reject with reason → 409 on retry ───────
	{
		s4 := pay(loanID, 500000, "disputed")
		var rej map[string]any
		postJSON(srv.URL+"/api/v1/loan-submissions/"+s4["id"].(string)+"/reject",
			mustJSON(map[string]string{"reason": "duplicate"}), 200, &rej)
		if rej["status"].(string) != "rejected" || rej["reject_reason"].(string) != "duplicate" {
			die("L8 reject wrong: %v", rej)
		}
		s, b := doReq("POST", srv.URL+"/api/v1/loan-submissions/"+s4["id"].(string)+"/reject",
			mustJSON(map[string]string{"reason": "again"}))
		if s != 409 || !isErr(b, "conflict") {
			die("L8 re-reject: status=%d body=%s (want 409/conflict)", s, string(b))
		}
		if got := loanGet(loanID); got["balance"].(float64) != 3000000 {
			die("L8 reject moved money: balance %v", got["balance"])
		}
		// Empty-body reject (no reason) on a fresh submission.
		s5 := pay(loanID, 100000, "no reason")
		var rej2 map[string]any
		postJSON(srv.URL+"/api/v1/loan-submissions/"+s5["id"].(string)+"/reject", nil, 200, &rej2)
		if rej2["status"].(string) != "rejected" {
			die("L8 empty-body reject: %v", rej2)
		}
		pass("L8 reject → 200 + reason, retry → 409, empty-body reject works")
	}

	// ── L9 validation → 400 ──────────────────────────────────
	{
		bad := func(body []byte, desc string) {
			s, b := doReq("POST", srv.URL+"/api/v1/loans/"+loanID+"/payments", body)
			if s != 400 || !isErr(b, "validation_failed") {
				die("L9 %s: status=%d body=%s (want 400/validation_failed)", desc, s, string(b))
			}
		}
		bad(mustJSON(map[string]any{"payer_name": "", "amount": 100, "effective_date": today}), "empty payer")
		bad(mustJSON(map[string]any{"payer_name": "x", "amount": 0, "effective_date": today}), "zero amount")
		bad(mustJSON(map[string]any{"payer_name": "x", "amount": 100, "effective_date": "06/04/2026"}), "bad date")
		bad(mustJSON(map[string]any{"payer_name": "x", "amount": 100, "effective_date": today, "zzz": 1}), "unknown field")
		bad([]byte("{not json"), "malformed json")
		pass("L9 payment validation → 400 (payer/amount/date/strict/malformed)")
	}

	// ── L10 404s + setup guards ──────────────────────────────
	{
		zero := "00000000-0000-0000-0000-000000000000"
		for _, u := range []string{
			srv.URL + "/api/v1/loans/" + zero,
		} {
			if s, b := doReq("GET", u, nil); s != 404 || !isErr(b, "not_found") {
				die("L10 GET %s: status=%d body=%s (want 404)", u, s, string(b))
			}
		}
		if s, b := doReq("GET", srv.URL+"/api/v1/loans/nope", nil); s != 400 || !isErr(b, "validation_failed") {
			die("L10 bad uuid: status=%d body=%s", s, string(b))
		}
		if s, b := doReq("POST", srv.URL+"/api/v1/loan-submissions/"+zero+"/approve", nil); s != 404 || !isErr(b, "not_found") {
			die("L10 approve unknown: status=%d body=%s", s, string(b))
		}
		if s, b := doReq("POST", srv.URL+"/api/v1/loan-submissions/"+zero+"/reject", nil); s != 404 || !isErr(b, "not_found") {
			die("L10 reject unknown: status=%d body=%s", s, string(b))
		}
		// Setup: duplicate borrower username → 409.
		s, b := doReq("POST", srv.URL+"/api/v1/loans", mustJSON(map[string]any{
			"name": pfx + "-C", "borrower_name": "x", "amount": 1000,
			"account_id": accID, "funded_from": "y",
			"username": pfx + "-user-a", "password": "secret123",
		}))
		if s != 409 || !isErr(b, "conflict") {
			die("L10 dup username: status=%d body=%s (want 409/conflict)", s, string(b))
		}
		// Setup: duplicate loan name → 409.
		s, b = doReq("POST", srv.URL+"/api/v1/loans", mustJSON(map[string]any{
			"name": pfx + "-A", "borrower_name": "x", "amount": 1000,
			"account_id": accID, "funded_from": "y",
			"username": pfx + "-user-c", "password": "secret123",
		}))
		if s != 409 || !isErr(b, "conflict") {
			die("L10 dup name: status=%d body=%s (want 409/conflict)", s, string(b))
		}
		// Setup: unknown account → 404; malformed account → 400.
		s, b = doReq("POST", srv.URL+"/api/v1/loans", mustJSON(map[string]any{
			"name": pfx + "-D", "borrower_name": "x", "amount": 1000,
			"account_id": zero, "funded_from": "y",
			"username": pfx + "-user-d", "password": "secret123",
		}))
		if s != 404 || !isErr(b, "not_found") {
			die("L10 unknown account: status=%d body=%s (want 404)", s, string(b))
		}
		s, b = doReq("POST", srv.URL+"/api/v1/loans", mustJSON(map[string]any{
			"name": pfx + "-D", "borrower_name": "x", "amount": 1000,
			"account_id": "nope", "funded_from": "y",
			"username": pfx + "-user-d", "password": "secret123",
		}))
		if s != 400 || !isErr(b, "validation_failed") {
			die("L10 bad account uuid: status=%d body=%s (want 400)", s, string(b))
		}
		pass("L10 unknown loan/submission → 404, dup username/name → 409, bad account → 404/400")
	}

	fmt.Println()
	fmt.Println("PASS — loans e2e: setup, detail, payment, approve, cancel, reject, 400/404/409, loan scoping")
}

// ── helpers ─────────────────────────────────────────────────────

func cleanup(pool *pgxpool.Pool, pfx string) {
	bg := context.Background()
	// FK order: notifications → submissions → headers → access → pos → accounts → counterparties.
	pool.Exec(bg, `DELETE FROM notifications WHERE related_transaction_id IN (SELECT id FROM transactions WHERE pos_id IN (SELECT id FROM pos WHERE name LIKE $1))`, pfx+"%")
	pool.Exec(bg, `DELETE FROM loan_payment_submission WHERE pos_id IN (SELECT id FROM pos WHERE name LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM transactions WHERE pos_id IN (SELECT id FROM pos WHERE name LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM loan_access WHERE pos_id IN (SELECT id FROM pos WHERE name LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM pos WHERE name LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM accounts WHERE name LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM counterparties WHERE name LIKE $1`, pfx+"%")
}

func doReq(method, url string, body []byte) (int, []byte) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		die("%s %s: new request: %v", method, url, err)
	}
	req.Header.Set("x-api-key", apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		die("%s %s: %v", method, url, err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, b
}

func getJSON(url string, dst interface{}) {
	status, body := doReq("GET", url, nil)
	if status != 200 {
		die("GET %s: status=%d body=%s", url, status, string(body))
	}
	if err := json.Unmarshal(body, dst); err != nil {
		die("decode %s: %v body=%s", url, err, string(body))
	}
}

func postJSON(url string, body []byte, wantStatus int, dst interface{}) {
	status, resp := doReq("POST", url, body)
	if status != wantStatus {
		die("POST %s: status=%d want=%d body=%s", url, status, wantStatus, string(resp))
	}
	if dst != nil {
		if err := json.Unmarshal(resp, dst); err != nil {
			die("decode %s: %v body=%s", url, err, string(resp))
		}
	}
}

func isErr(body []byte, code string) bool {
	var apiErr struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &apiErr); err != nil {
		return false
	}
	return apiErr.Error == code
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		die("marshal: %v", err)
	}
	return b
}

func pass(msg string) { fmt.Printf("✓ %s\n", msg) }
func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", args...)
	os.Exit(1)
}
