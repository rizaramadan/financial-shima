//go:build ignore

// e2e_api_reverse proves POST /api/v1/transactions/:id/reverse:
// append-only offsetting entry, idempotency, and conflict handling.
//
//	export DATABASE_URL=postgres://postgreuser@localhost:5432/financial_shima?sslmode=disable
//	go run ./scripts/e2e_api_reverse.go
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
	api.POST("/pos", h.APIPosCreate)
	api.GET("/pos/:id", h.APIPosGet)
	api.POST("/counterparties", h.APICounterpartiesCreate)
	api.GET("/transactions", h.APITransactionsList)
	api.POST("/transactions", h.APITransactionsCreate)
	api.POST("/transactions/:id/reverse", h.APITransactionsReverse)
	api.GET("/balances", h.APIBalancesGet)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	pfx := fmt.Sprintf("e2e-rev-%d", stamp)
	defer cleanup(pool, pfx)

	today := time.Now().Format("2006-01-02")

	// ── Seed: account, pos, counterparty ─────────────────────────
	var acc map[string]any
	postJSON(srv.URL+"/api/v1/accounts", mustJSON(map[string]string{"name": pfx + "-acc"}), 201, &acc)
	accID := acc["id"].(string)

	var pos map[string]any
	postJSON(srv.URL+"/api/v1/pos", mustJSON(map[string]any{
		"name": pfx + "-pos", "currency": "idr", "account_id": accID,
	}), 201, &pos)
	posID := pos["id"].(string)

	var cp map[string]any
	postJSON(srv.URL+"/api/v1/counterparties",
		mustJSON(map[string]string{"name": pfx + "-cp"}), 201, &cp)
	cpID := cp["id"].(string)

	// ── R1 create money_out → reverse (empty body, derived key) ──
	var out map[string]any
	postJSON(srv.URL+"/api/v1/transactions", mustJSON(map[string]any{
		"type": "money_out", "effective_date": today,
		"account_amount": int64(500000), "pos_id": posID, "pos_amount": int64(500000),
		"counterparty_id": cpID, "note": "rev me",
		"idempotency_key": pfx + "-txn-out",
	}), 201, &out)
	origID := out["id"].(string)

	var beforeType string
	var beforeAcct, beforePos int64
	var beforeNote *string
	if err := pool.QueryRow(ctx,
		`SELECT type::text, account_amount, pos_amount, note FROM transactions WHERE id = $1`,
		origID).Scan(&beforeType, &beforeAcct, &beforePos, &beforeNote); err != nil {
		die("R1 snapshot original: %v", err)
	}

	var rev map[string]any
	postRaw(srv.URL+"/api/v1/transactions/"+origID+"/reverse", nil, 201, &rev)
	revID := rev["id"].(string)
	if rev["type"].(string) != "money_in" {
		die("R1 reversal type = %v (want money_in)", rev["type"])
	}
	if rev["reverses_id"].(string) != origID {
		die("R1 reverses_id = %v (want %s)", rev["reverses_id"], origID)
	}
	if rev["account_amount"].(float64) != 500000 || rev["pos_amount"].(float64) != 500000 {
		die("R1 reversal amounts = %v/%v (want 500000/500000)", rev["account_amount"], rev["pos_amount"])
	}
	if !rev["was_inserted"].(bool) {
		die("R1 was_inserted = false (want true on first reverse)")
	}
	if rev["idempotency_key"].(string) != "reverse:"+origID {
		die("R1 derived key = %v (want reverse:%s)", rev["idempotency_key"], origID)
	}
	links, _ := rev["_links"].(map[string]any)
	if links == nil || links["original"] == nil || links["self"] == nil {
		die("R1 _links missing original/self: %v", rev["_links"])
	}
	if act, _ := rev["_actions"].([]any); len(act) == 0 {
		die("R1 _actions empty")
	}
	pass("R1 money_out → reverse → 201 money_in + reverses_id + links.original + derived key")

	// ── R2 DB: reverses_id set, original untouched (append-only) ──
	{
		var dbReverses *string
		var dbType string
		if err := pool.QueryRow(ctx,
			`SELECT reverses_id::text, type::text FROM transactions WHERE id = $1`,
			revID).Scan(&dbReverses, &dbType); err != nil {
			die("R2 reversal row: %v", err)
		}
		if dbReverses == nil || *dbReverses != origID || dbType != "money_in" {
			die("R2 reversal row: reverses=%v type=%s", dbReverses, dbType)
		}
		var afterType string
		var afterAcct, afterPos int64
		var afterNote *string
		if err := pool.QueryRow(ctx,
			`SELECT type::text, account_amount, pos_amount, note FROM transactions WHERE id = $1`,
			origID).Scan(&afterType, &afterAcct, &afterPos, &afterNote); err != nil {
			die("R2 original row: %v", err)
		}
		if afterType != beforeType || afterAcct != beforeAcct || afterPos != beforePos {
			die("R2 original mutated: was (%s %d %d), now (%s %d %d)",
				beforeType, beforeAcct, beforePos, afterType, afterAcct, afterPos)
		}
		var revCount int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM transactions WHERE reverses_id = $1`, origID).Scan(&revCount); err != nil {
			die("R2 count reversals: %v", err)
		}
		if revCount != 1 {
			die("R2 reversals for original = %d (want 1)", revCount)
		}
		pass("R2 DB: reverses_id set, original row untouched, exactly 1 reversal")
	}

	// ── R3 balances net-zero ─────────────────────────────────────
	{
		var bal map[string]any
		getJSON(srv.URL+"/api/v1/balances", &bal)
		var ours map[string]any
		for _, a := range bal["accounts"].([]any) {
			if m, ok := a.(map[string]any); ok && m["id"] == accID {
				ours = m
				break
			}
		}
		if ours == nil {
			die("R3 our account missing from balances")
		}
		if ours["balance_idr"].(float64) != 0 {
			die("R3 account balance = %v (want 0 net)", ours["balance_idr"])
		}
		// Global IDR reconciliation is informational only: other runs'
		// archived-pos leftovers pollute it (pos-cash side excludes
		// archived pos, account-flow side does not). The invariant this
		// step owns is scoped: OUR pair sums to zero on both sides.
		var acctNet, posNet int64
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(CASE WHEN type='money_in' THEN account_amount WHEN type='money_out' THEN -account_amount ELSE 0 END),0)::bigint,
			        COALESCE(SUM(CASE WHEN type='money_in' THEN pos_amount WHEN type='money_out' THEN -pos_amount ELSE 0 END),0)::bigint
			 FROM transactions WHERE pos_id = $1`,
			posID).Scan(&acctNet, &posNet); err != nil {
			die("R3 pair net: %v", err)
		}
		if acctNet != 0 || posNet != 0 {
			die("R3 pair net = account %d / pos %d (want 0/0)", acctNet, posNet)
		}
		pass("R3 balances net-zero: account=0, pair nets 0/0 on both sides")
	}

	// ── R4 idempotent re-submit (same derived key) → same row ────
	{
		var again map[string]any
		postJSON(srv.URL+"/api/v1/transactions/"+origID+"/reverse",
			mustJSON(map[string]string{"idempotency_key": "reverse:" + origID}), 201, &again)
		if again["id"].(string) != revID {
			die("R4 re-submit id = %v (want %s)", again["id"], revID)
		}
		if again["was_inserted"].(bool) {
			die("R4 re-submit was_inserted = true (want false)")
		}
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE reverses_id = $1`, origID).Scan(&n)
		if n != 1 {
			die("R4 re-submit added rows: now %d (want 1)", n)
		}
		pass("R4 same-key re-submit → same id, was_inserted=false, no new row")
	}

	// ── R5 double-reverse (fresh key) → 409; unknown → 404 ───────
	{
		postExpect(srv.URL+"/api/v1/transactions/"+origID+"/reverse",
			mustJSON(map[string]string{"idempotency_key": pfx + "-second"}), 409, "conflict")
		postExpect(srv.URL+"/api/v1/transactions/00000000-0000-0000-0000-000000000000/reverse",
			mustJSON(map[string]string{"idempotency_key": pfx + "-ghost"}), 404, "not_found")
		postExpect(srv.URL+"/api/v1/transactions/not-a-uuid/reverse", nil, 400, "validation_failed")
		postExpect(srv.URL+"/api/v1/transactions/"+origID+"/reverse",
			mustJSON(map[string]string{"bogus_field": "x"}), 400, "validation_failed")
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE reverses_id = $1`, origID).Scan(&n)
		if n != 1 {
			die("R5 rejected reverses added rows: now %d (want 1)", n)
		}
		pass("R5 double-reverse → 409, unknown id → 404, malformed id/body → 400, no extra rows")
	}

	// ── R6 money_in reverses to money_out; list shows the pair ───
	{
		var in map[string]any
		postJSON(srv.URL+"/api/v1/transactions", mustJSON(map[string]any{
			"type": "money_in", "effective_date": today,
			"account_amount": int64(200000), "pos_id": posID, "pos_amount": int64(200000),
			"counterparty_id": cpID, "note": "rev in",
			"idempotency_key": pfx + "-txn-in",
		}), 201, &in)
		inID := in["id"].(string)
		var revIn map[string]any
		postJSON(srv.URL+"/api/v1/transactions/"+inID+"/reverse",
			mustJSON(map[string]string{"idempotency_key": pfx + "-rev-in"}), 201, &revIn)
		if revIn["type"].(string) != "money_out" {
			die("R6 reversal type = %v (want money_out)", revIn["type"])
		}
		var bal map[string]any
		getJSON(srv.URL+"/api/v1/balances", &bal)
		for _, a := range bal["accounts"].([]any) {
			if m, ok := a.(map[string]any); ok && m["id"] == accID && m["balance_idr"].(float64) != 0 {
				die("R6 account balance = %v (want 0 net)", m["balance_idr"])
			}
		}

		var list []map[string]any
		getJSON(srv.URL+"/api/v1/transactions?from=2000-01-01&to=2100-01-01", &list)
		seenOrig, seenRev := false, false
		for _, t := range list {
			if t["id"] == origID {
				seenOrig = true
				if t["is_reversal"].(bool) {
					die("R6 original flagged is_reversal")
				}
			}
			if t["id"] == revID {
				seenRev = true
				if !t["is_reversal"].(bool) || t["reverses_id"].(string) != origID {
					die("R6 reversal list flags wrong: %v", t)
				}
			}
		}
		if !seenOrig || !seenRev {
			die("R6 list missing pair: orig=%v rev=%v", seenOrig, seenRev)
		}
		pass("R6 money_in → money_out reversal, balances still net-zero, list shows pair w/ strikethrough flags")
	}

	fmt.Println()
	fmt.Println("PASS — reverse e2e: offsetting insert, append-only, idempotent, 409/404/400, net-zero, list pair")
}

// ── helpers ─────────────────────────────────────────────────────

func cleanup(pool *pgxpool.Pool, pfx string) {
	bg := context.Background()
	// Reversal rows carry derived "reverse:<uuid>" keys (no pfx), so
	// remove them via their reverses_id link to this run's rows.
	// Notifications reference transactions with no ON DELETE CASCADE,
	// so they go first; Exec errors are ignored like e2e_api_p1.
	pool.Exec(bg, `DELETE FROM notifications WHERE related_transaction_id IN (SELECT id FROM transactions WHERE reverses_id IN (SELECT id FROM transactions WHERE idempotency_key LIKE $1))`, pfx+"%")
	pool.Exec(bg, `DELETE FROM notifications WHERE related_transaction_id IN (SELECT id FROM transactions WHERE idempotency_key LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM transactions WHERE reverses_id IN (SELECT id FROM transactions WHERE idempotency_key LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM transactions WHERE idempotency_key LIKE $1`, pfx+"%")
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

func getExpectStatus(url string, wantStatus int, wantErrCode string) {
	status, body := doReq("GET", url, nil)
	if status != wantStatus {
		die("GET %s: status=%d want=%d body=%s", url, status, wantStatus, string(body))
	}
	var apiErr struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &apiErr); err != nil {
		die("decode error %s: %v", url, err)
	}
	if apiErr.Error != wantErrCode {
		die("GET %s: error=%q want %q", url, apiErr.Error, wantErrCode)
	}
}

func postJSON(url string, body []byte, wantStatus int, dst interface{}) {
	postRaw(url, body, wantStatus, dst)
}

func postRaw(url string, body []byte, wantStatus int, dst interface{}) {
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

func postExpect(url string, body []byte, wantStatus int, wantErrCode string) {
	status, resp := doReq("POST", url, body)
	if status != wantStatus {
		die("POST %s: status=%d want=%d body=%s", url, status, wantStatus, string(resp))
	}
	var apiErr struct {
		Error string `json:"error"`
	}
	json.Unmarshal(resp, &apiErr)
	if apiErr.Error != wantErrCode {
		die("POST %s: error=%q want %q body=%s", url, apiErr.Error, wantErrCode, string(resp))
	}
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
