//go:build ignore

// e2e_api_transfers proves POST /api/v1/transfers:
// atomic reallocation + borrow inserts, per-currency net-zero balances,
// borrow obligations visible in GET /debts, validation/404/409 handling,
// and idempotent replay.
//
//	export DATABASE_URL=postgres://postgreuser@localhost:5432/financial_shima?sslmode=disable
//	go run ./scripts/e2e_api_transfers.go
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
	api.POST("/transactions", h.APITransactionsCreate)
	api.POST("/transfers", h.APITransfersCreate)
	api.GET("/balances", h.APIBalancesGet)
	api.GET("/debts", h.APIDebtsList)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	pfx := fmt.Sprintf("e2e-xfer-%d", stamp)
	defer cleanup(pool, pfx)

	today := time.Now().Format("2006-01-02")

	// ── Seed: account + two IDR pos ────────────────────────────
	var acc map[string]any
	postJSON(srv.URL+"/api/v1/accounts", mustJSON(map[string]string{"name": pfx + "-acc"}), 201, &acc)
	accID := acc["id"].(string)
	_ = accID

	mkPos := func(name string) string {
		var p map[string]any
		postJSON(srv.URL+"/api/v1/pos", mustJSON(map[string]any{
			"name": name, "currency": "idr", "account_id": accID,
		}), 201, &p)
		return p["id"].(string)
	}
	posA := mkPos(pfx + "-posA")
	posB := mkPos(pfx + "-posB")

	balances := func() (map[string]float64, float64) {
		var bal map[string]any
		getJSON(srv.URL+"/api/v1/balances", &bal)
		cash := map[string]float64{}
		for _, p := range bal["pos"].([]any) {
			if m, ok := p.(map[string]any); ok {
				cash[m["id"].(string)] = m["cash"].(float64)
			}
		}
		// Global IDR reconciliation is informational only on a shared
		// DB (other runs' leftovers pollute it — see e2e_api_reverse
		// R3). The invariant this step owns is scoped: OUR pair sums
		// to zero per currency AND the global IDR difference is
		// unchanged by our transfers (they net to zero everywhere).
		var diff float64
		for _, r := range bal["reconciliation"].([]any) {
			if m, ok := r.(map[string]any); ok && m["currency"] == "idr" {
				diff = m["difference"].(float64)
			}
		}
		return cash, diff
	}
	_, baseDiff := balances()

	transfer := func(mode string, lines []map[string]any, key string) (int, []byte) {
		return doReq("POST", srv.URL+"/api/v1/transfers", mustJSON(map[string]any{
			"mode": mode, "lines": lines, "effective_date": today,
			"idempotency_key": key,
		}))
	}
	line := func(posID, dir string, amt int64) map[string]any {
		return map[string]any{"pos_id": posID, "direction": dir, "amount": amt, "currency": "idr"}
	}

	// ── T1 reallocation A→B 100000 → 201 ────────────────────────
	var t1 map[string]any
	{
		status, body := transfer("reallocation", []map[string]any{
			line(posA, "out", 100000), line(posB, "in", 100000),
		}, pfx+"-t1")
		if status != 201 {
			die("T1: status=%d want 201 body=%s", status, string(body))
		}
		if err := json.Unmarshal(body, &t1); err != nil {
			die("T1 decode: %v", err)
		}
		if t1["mode"].(string) != "reallocation" {
			die("T1 mode = %v", t1["mode"])
		}
		if !t1["was_inserted"].(bool) {
			die("T1 was_inserted = false (want true)")
		}
		ls, _ := t1["lines"].([]any)
		if len(ls) != 2 {
			die("T1 lines = %d (want 2)", len(ls))
		}
		links, _ := t1["_links"].(map[string]any)
		for _, k := range []string{"self", "pos", "transactions", "debts"} {
			if links == nil || links[k] == nil {
				die("T1 _links missing %q: %v", k, t1["_links"])
			}
		}
		if act, _ := t1["_actions"].([]any); len(act) == 0 {
			die("T1 _actions empty")
		}
		pass("T1 reallocation A→B → 201 + mode + 2 lines + _links/_actions")
	}
	t1ID := t1["id"].(string)

	// ── T2 balances net-zero per currency, debts empty ─────────
	{
		cash, diff := balances()
		if cash[posA] != -100000 || cash[posB] != 100000 {
			die("T2 pos cash = A:%v B:%v (want -100000/+100000)", cash[posA], cash[posB])
		}
		if cash[posA]+cash[posB] != 0 {
			die("T2 pair net = %v (want 0)", cash[posA]+cash[posB])
		}
		if diff != baseDiff {
			die("T2 global idr difference moved %v → %v (transfer must net to zero)", baseDiff, diff)
		}
		for _, f := range []string{"debtor_pos_id=" + posB, "creditor_pos_id=" + posA} {
			var debts map[string]any
			getJSON(srv.URL+"/api/v1/debts?"+f, &debts)
			if len(debts["debts"].([]any)) != 0 {
				die("T2 debts non-empty after reallocation (%s): %v", f, debts["debts"])
			}
		}
		pass("T2 balances: A=-100000 B=+100000 (pair nets 0), global idr diff unchanged, debts empty (reallocation creates none)")
	}

	// ── T3 borrow A→B 50000 → 201 + debt row ───────────────────
	var t2 map[string]any
	{
		status, body := transfer("borrow", []map[string]any{
			line(posA, "out", 50000), line(posB, "in", 50000),
		}, pfx+"-t2")
		if status != 201 {
			die("T3: status=%d want 201 body=%s", status, string(body))
		}
		if err := json.Unmarshal(body, &t2); err != nil {
			die("T3 decode: %v", err)
		}
		var debts map[string]any
		getJSON(srv.URL+"/api/v1/debts?debtor_pos_id="+posB, &debts)
		rows := debts["debts"].([]any)
		if len(rows) != 1 {
			die("T3 debts for B = %d (want 1): %v", len(rows), debts)
		}
		d := rows[0].(map[string]any)
		if d["creditor_pos_id"].(string) != posA || d["owed"].(float64) != 50000 ||
			d["outstanding"].(float64) != 50000 || d["currency"].(string) != "idr" {
			die("T3 debt row wrong: %v", d)
		}
		if d["transaction_id"].(string) != t2["id"].(string) {
			die("T3 debt txn = %v (want %v)", d["transaction_id"], t2["id"])
		}
		var byCred map[string]any
		getJSON(srv.URL+"/api/v1/debts?creditor_pos_id="+posA, &byCred)
		if len(byCred["debts"].([]any)) != 1 {
			die("T3 creditor filter: %v", byCred)
		}
		pass("T3 borrow A→B → 201 + debt A→B owed 50000 visible via debtor+creditor filters")
	}

	// ── T4 balances after borrow: A=-150000 B=+150000, diff unchanged ─
	{
		cash, diff := balances()
		if cash[posA] != -150000 || cash[posB] != 150000 {
			die("T4 pos cash = A:%v B:%v (want -150000/+150000)", cash[posA], cash[posB])
		}
		if diff != baseDiff {
			die("T4 global idr difference moved %v → %v (borrow must net to zero)", baseDiff, diff)
		}
		pass("T4 balances: A=-150000 B=+150000 (pair nets 0), global idr diff unchanged")
	}

	// ── T5 validation + 404 ────────────────────────────────────
	{
		// unbalanced
		if s, b := transfer("reallocation", []map[string]any{
			line(posA, "out", 100), line(posB, "in", 90),
		}, pfx+"-bad1"); s != 400 || !isErr(b, "validation_failed") {
			die("T5 unbalanced: status=%d body=%s", s, string(b))
		}
		// bad mode
		if s, b := transfer("teleport", []map[string]any{
			line(posA, "out", 100), line(posB, "in", 100),
		}, pfx+"-bad2"); s != 400 || !isErr(b, "validation_failed") {
			die("T5 bad mode: status=%d body=%s", s, string(b))
		}
		// unknown pos
		if s, b := transfer("borrow", []map[string]any{
			line("00000000-0000-0000-0000-000000000000", "out", 100), line(posB, "in", 100),
		}, pfx+"-bad3"); s != 404 || !isErr(b, "not_found") {
			die("T5 unknown pos: status=%d body=%s", s, string(b))
		}
		// malformed pos uuid
		if s, b := transfer("borrow", []map[string]any{
			line("nope", "out", 100), line(posB, "in", 100),
		}, pfx+"-bad4"); s != 400 || !isErr(b, "validation_failed") {
			die("T5 bad uuid: status=%d body=%s", s, string(b))
		}
		// empty key
		if s, b := transfer("borrow", []map[string]any{
			line(posA, "out", 100), line(posB, "in", 100),
		}, "  "); s != 400 || !isErr(b, "validation_failed") {
			die("T5 empty key: status=%d body=%s", s, string(b))
		}
		// future date
		fut := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
		s, b := doReq("POST", srv.URL+"/api/v1/transfers", mustJSON(map[string]any{
			"mode": "borrow", "lines": []map[string]any{
				line(posA, "out", 100), line(posB, "in", 100),
			}, "effective_date": fut, "idempotency_key": pfx + "-bad5",
		}))
		if s != 400 || !isErr(b, "validation_failed") {
			die("T5 future date: status=%d body=%s", s, string(b))
		}
		pass("T5 unbalanced/bad-mode → 400, unknown pos → 404, bad uuid/empty key/future → 400")
	}

	// ── T6 idempotent resubmit same key → same id, no dup ──────
	{
		status, body := transfer("reallocation", []map[string]any{
			line(posA, "out", 100000), line(posB, "in", 100000),
		}, pfx+"-t1")
		if status != 201 {
			die("T6: status=%d want 201 body=%s", status, string(body))
		}
		var again map[string]any
		if err := json.Unmarshal(body, &again); err != nil {
			die("T6 decode: %v", err)
		}
		if again["id"].(string) != t1ID {
			die("T6 id = %v (want %s)", again["id"], t1ID)
		}
		if again["was_inserted"].(bool) {
			die("T6 was_inserted = true (want false)")
		}
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM inter_pos_lines WHERE transaction_id = $1`, t1ID).Scan(&n); err != nil {
			die("T6 count lines: %v", err)
		}
		if n != 2 {
			die("T6 lines for txn = %d (want 2, no duplicates)", n)
		}
		var nn int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pos_obligation WHERE transaction_id = $1`, t2["id"].(string)).Scan(&nn); err != nil {
			die("T6 count obligations: %v", err)
		}
		if nn != 1 {
			die("T6 obligations for borrow = %d (want 1)", nn)
		}
		pass("T6 same-key resubmit → same id, was_inserted=false, no dup legs/obligations")
	}

	// ── T7 conflict: same key, different payload → 409 ─────────
	{
		s, b := transfer("reallocation", []map[string]any{
			line(posA, "out", 777), line(posB, "in", 777),
		}, pfx+"-t1")
		if s != 409 || !isErr(b, "conflict") {
			die("T7 conflict: status=%d body=%s (want 409/conflict)", s, string(b))
		}
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE idempotency_key = $1`, pfx+"-t1").Scan(&n)
		if n != 1 {
			die("T7 conflict added rows: now %d (want 1)", n)
		}
		pass("T7 same key + different payload → 409 conflict, no extra row")
	}

	fmt.Println()
	fmt.Println("PASS — transfers e2e: reallocation, borrow+debts, balances net-zero, 400/404/409, idempotent")
}

// ── helpers ─────────────────────────────────────────────────────

func cleanup(pool *pgxpool.Pool, pfx string) {
	bg := context.Background()
	// FK order: notifications → obligations → lines → headers → pos → accounts → counterparties.
	pool.Exec(bg, `DELETE FROM notifications WHERE related_transaction_id IN (SELECT id FROM transactions WHERE idempotency_key LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM pos_obligation WHERE transaction_id IN (SELECT id FROM transactions WHERE idempotency_key LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM inter_pos_lines WHERE transaction_id IN (SELECT id FROM transactions WHERE idempotency_key LIKE $1)`, pfx+"%")
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
