//go:build ignore

// e2e_api_p2_readonly proves the read-only P2 endpoints: GET /api/v1/debts
// and GET /api/v1/recap, plus their filters and empty cases.
//
// Style mirrors scripts/e2e_api_p1.go: strict status codes, error-code
// bodies, DB row verification. Debts seed via direct SQL into
// pos_obligation (the borrow write path belongs to the tx-service
// worker; this script only proves the reads).
//
//	export DATABASE_URL=postgres://postgreuser@localhost:5432/financial_shima?sslmode=disable
//	export LLM_API_KEY=test-api-key-for-e2e
//	go run ./scripts/e2e_api_p2_readonly.go
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
	api.POST("/counterparties", h.APICounterpartiesCreate)
	api.POST("/transactions", h.APITransactionsCreate)
	api.GET("/debts", h.APIDebtsList)
	api.GET("/recap", h.APIRecapGet)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	pfx := fmt.Sprintf("e2e-p2-%d", stamp)
	defer cleanup(pool, pfx)

	today := time.Now().Format("2006-01-02")

	// ── Seed: acc, posCred, posDebt, cp, money_in 800k, money_out 300k ─
	var acc map[string]any
	postJSON(srv.URL+"/api/v1/accounts", mustJSON(map[string]string{"name": pfx + "-acc"}), 201, &acc)
	accID := acc["id"].(string)

	var posC, posD map[string]any
	postJSON(srv.URL+"/api/v1/pos", mustJSON(map[string]any{
		"name": pfx + "-pos-c", "currency": "idr", "account_id": accID,
	}), 201, &posC)
	postJSON(srv.URL+"/api/v1/pos", mustJSON(map[string]any{
		"name": pfx + "-pos-d", "currency": "idr", "account_id": accID,
	}), 201, &posD)
	posCID := posC["id"].(string)
	posDID := posD["id"].(string)

	var cp map[string]any
	postJSON(srv.URL+"/api/v1/counterparties",
		mustJSON(map[string]string{"name": pfx + "-cp"}), 201, &cp)
	cpID := cp["id"].(string)

	var txnIn map[string]any
	postJSON(srv.URL+"/api/v1/transactions", mustJSON(map[string]any{
		"type": "money_in", "effective_date": today,
		"account_amount": int64(800000), "pos_id": posCID, "pos_amount": int64(800000),
		"counterparty_id": cpID, "note": "p2 seed in",
		"idempotency_key": pfx + "-txn-in",
	}), 201, &txnIn)
	txnID := txnIn["id"].(string)

	postJSON(srv.URL+"/api/v1/transactions", mustJSON(map[string]any{
		"type": "money_out", "effective_date": today,
		"account_amount": int64(300000), "pos_id": posCID, "pos_amount": int64(300000),
		"counterparty_id": cpID, "note": "p2 seed out",
		"idempotency_key": pfx + "-txn-out",
	}), 201, nil)
	pass("seeded 1 account, 2 pos, 1 counterparty, in=800k out=300k")

	// Debts seed: two open rows (one partial) + one cleared (excluded).
	// FK transaction_id points at the seeded money_in row; the borrow
	// write path that would own this linkage ships separately.
	if _, err := pool.Exec(ctx, `INSERT INTO pos_obligation
		(transaction_id, creditor_pos_id, debtor_pos_id, currency, amount_owed, amount_repaid)
		VALUES ($1, $2, $3, 'idr', 500000, 100000)`,
		txnID, posCID, posDID); err != nil {
		die("seed ob1: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pos_obligation
		(transaction_id, creditor_pos_id, debtor_pos_id, currency, amount_owed, amount_repaid)
		VALUES ($1, $2, $3, 'idr', 200000, 0)`,
		txnID, posCID, posDID); err != nil {
		die("seed ob2: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pos_obligation
		(transaction_id, creditor_pos_id, debtor_pos_id, currency, amount_owed, amount_repaid, cleared_at)
		VALUES ($1, $2, $3, 'idr', 300000, 300000, now())`,
		txnID, posCID, posDID); err != nil {
		die("seed ob-cleared: %v", err)
	}
	pass("seeded 2 open + 1 cleared obligation rows")

	// ── D1 GET /debts filtered to our debtor: shape + names + HATEOAS ─
	{
		var resp struct {
			Debts   []map[string]any  `json:"debts"`
			Totals  []map[string]any  `json:"totals"`
			Links   map[string]string `json:"_links"`
			Actions []map[string]any  `json:"_actions"`
		}
		getJSON(srv.URL+"/api/v1/debts?debtor_pos_id="+posDID, &resp)
		if len(resp.Debts) != 2 {
			die("D1 debts for our debtor = %d (want 2; cleared must be excluded)", len(resp.Debts))
		}
		byOwed := map[float64]map[string]any{}
		for _, d := range resp.Debts {
			byOwed[d["owed"].(float64)] = d
		}
		ob1, ok1 := byOwed[500000]
		ob2, ok2 := byOwed[200000]
		if !ok1 || !ok2 {
			die("D1 owed values missing: have %v", byOwed)
		}
		if ob1["outstanding"].(float64) != 400000 || ob1["repaid"].(float64) != 100000 {
			die("D1 ob1 outstanding=%v repaid=%v (want 400000/100000)", ob1["outstanding"], ob1["repaid"])
		}
		if ob2["outstanding"].(float64) != 200000 {
			die("D1 ob2 outstanding=%v (want 200000)", ob2["outstanding"])
		}
		for _, d := range resp.Debts {
			if d["creditor_pos_name"].(string) != pfx+"-pos-c" || d["debtor_pos_name"].(string) != pfx+"-pos-d" {
				die("D1 pos names not joined: %v", d)
			}
			if d["creditor_pos_id"].(string) != posCID || d["debtor_pos_id"].(string) != posDID {
				die("D1 pos ids wrong: %v", d)
			}
			if d["transaction_id"].(string) != txnID {
				die("D1 transaction_id = %v (want %s)", d["transaction_id"], txnID)
			}
			links, ok := d["_links"].(map[string]any)
			if !ok || links["creditor_pos"] == nil || links["debtor_pos"] == nil || links["transactions"] == nil {
				die("D1 row _links missing creditor/debtor/transactions: %v", d["_links"])
			}
		}
		if resp.Links["self"] != "/api/v1/debts" {
			die("D1 top _links.self = %q", resp.Links["self"])
		}
		if len(resp.Actions) == 0 {
			die("D1 _actions empty")
		}
		if len(resp.Totals) != 1 || resp.Totals[0]["currency"].(string) != "idr" ||
			resp.Totals[0]["outstanding"].(float64) != 600000 {
			die("D1 totals = %v (want [{idr 600000}])", resp.Totals)
		}
		pass("D1 GET /debts → 2 open (cleared excluded), names joined, outstanding 400k/200k, totals idr=600k, _links/_actions")
	}

	// ── D2 debts filters + empty case + 400 ──────────────────────────
	{
		var byCred struct {
			Debts []map[string]any `json:"debts"`
		}
		getJSON(srv.URL+"/api/v1/debts?creditor_pos_id="+posCID, &byCred)
		if len(byCred.Debts) != 2 {
			die("D2 creditor filter = %d (want 2)", len(byCred.Debts))
		}
		var byBoth struct {
			Debts []map[string]any `json:"debts"`
		}
		getJSON(srv.URL+"/api/v1/debts?creditor_pos_id="+posCID+"&debtor_pos_id="+posDID+"&currency=idr", &byBoth)
		if len(byBoth.Debts) != 2 {
			die("D2 combined filters = %d (want 2)", len(byBoth.Debts))
		}
		var empty struct {
			Debts  []map[string]any `json:"debts"`
			Totals []map[string]any `json:"totals"`
		}
		getJSON(srv.URL+"/api/v1/debts?currency=gold-g", &empty)
		if empty.Debts == nil || len(empty.Debts) != 0 {
			die("D2 currency miss: debts = %v (want [])", empty.Debts)
		}
		if empty.Totals == nil || len(empty.Totals) != 0 {
			die("D2 currency miss: totals = %v (want [])", empty.Totals)
		}
		getExpectStatus(srv.URL+"/api/v1/debts?creditor_pos_id=bad", 400, "validation_failed")
		getExpectStatus(srv.URL+"/api/v1/debts?debtor_pos_id=bad", 400, "validation_failed")
		pass("D2 filters creditor/debtor/currency + empty [] (never null) + 400 on bad UUID")
	}

	// ── R1 GET /recap default month: seeded flows present, net = in-out ─
	{
		var resp struct {
			Bucket  string            `json:"bucket"`
			From    string            `json:"from"`
			To      string            `json:"to"`
			Rows    []map[string]any  `json:"rows"`
			Totals  []map[string]any  `json:"totals"`
			Links   map[string]string `json:"_links"`
			Actions []map[string]any  `json:"_actions"`
		}
		getJSON(srv.URL+"/api/v1/recap", &resp)
		if resp.Bucket != "month" {
			die("R1 bucket = %q (want month)", resp.Bucket)
		}
		if resp.From == "" || resp.To == "" {
			die("R1 from/to not echoed: %q/%q", resp.From, resp.To)
		}
		var idrIn, idrOut float64
		for _, r := range resp.Rows {
			in, out, net := r["inflow"].(float64), r["outflow"].(float64), r["net"].(float64)
			if in-out != net {
				die("R1 row net mismatch: %v", r)
			}
			if r["currency"].(string) == "idr" {
				idrIn += in
				idrOut += out
			}
		}
		if idrIn < 800000 || idrOut < 300000 {
			die("R1 idr window inflow=%v outflow=%v (want >=800000/>=300000)", idrIn, idrOut)
		}
		found := false
		for _, t := range resp.Totals {
			if t["currency"].(string) != "idr" {
				continue
			}
			found = true
			in, out, net := t["inflow"].(float64), t["outflow"].(float64), t["net"].(float64)
			if in-out != net {
				die("R1 idr totals net mismatch: %v", t)
			}
			if in < 800000 || out < 300000 {
				die("R1 idr totals inflow=%v outflow=%v (want >=800000/>=300000)", in, out)
			}
		}
		if !found {
			die("R1 idr totals row missing")
		}
		if resp.Links["self"] == "" || resp.Links["transactions"] == "" {
			die("R1 _links missing self/transactions: %v", resp.Links)
		}
		if len(resp.Actions) == 0 {
			die("R1 _actions empty")
		}
		pass("R1 GET /recap month → seeded 800k/300k present, every net == in-out, totals consistent, _links/_actions")
	}

	// ── R2 bucket variants + 400s ────────────────────────────────────
	{
		var week struct {
			Bucket string           `json:"bucket"`
			Rows   []map[string]any `json:"rows"`
		}
		getJSON(srv.URL+"/api/v1/recap?bucket=week", &week)
		if week.Bucket != "week" {
			die("R2 week bucket = %q", week.Bucket)
		}
		for _, r := range week.Rows {
			if r["inflow"].(float64)-r["outflow"].(float64) != r["net"].(float64) {
				die("R2 week row net mismatch: %v", r)
			}
		}
		var year struct {
			Bucket string           `json:"bucket"`
			Rows   []map[string]any `json:"rows"`
		}
		getJSON(srv.URL+"/api/v1/recap?bucket=year", &year)
		if year.Bucket != "year" || len(year.Rows) == 0 {
			die("R2 year: bucket=%q rows=%d", year.Bucket, len(year.Rows))
		}
		var wide struct {
			Rows []map[string]any `json:"rows"`
		}
		getJSON(srv.URL+"/api/v1/recap?bucket=month&from=2000-01-01&to=2100-01-01", &wide)
		var wIn, wOut float64
		for _, r := range wide.Rows {
			if r["currency"].(string) == "idr" {
				wIn += r["inflow"].(float64)
				wOut += r["outflow"].(float64)
			}
		}
		if wIn < 800000 || wOut < 300000 {
			die("R2 wide window idr inflow=%v outflow=%v (want >=800000/>=300000)", wIn, wOut)
		}
		getExpectStatus(srv.URL+"/api/v1/recap?bucket=bogus", 400, "validation_failed")
		getExpectStatus(srv.URL+"/api/v1/recap?from=not-a-date", 400, "validation_failed")
		getExpectStatus(srv.URL+"/api/v1/recap?to=not-a-date", 400, "validation_failed")
		pass("R2 bucket week/year + wide window + 400 on bad bucket/from/to")
	}

	// ── R3 empty case: future window → rows [] totals [] ─────────────
	{
		futureFrom := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
		futureTo := time.Now().AddDate(0, 0, 60).Format("2006-01-02")
		var resp struct {
			Rows   []map[string]any `json:"rows"`
			Totals []map[string]any `json:"totals"`
		}
		getJSON(srv.URL+"/api/v1/recap?from="+futureFrom+"&to="+futureTo, &resp)
		if resp.Rows == nil || len(resp.Rows) != 0 {
			die("R3 future window rows = %v (want [])", resp.Rows)
		}
		if resp.Totals == nil || len(resp.Totals) != 0 {
			die("R3 future window totals = %v (want [])", resp.Totals)
		}
		pass("R3 future window → rows [] totals [] (never null)")
	}

	fmt.Println()
	fmt.Println("PASS — P2 readonly: /debts open+filters+empty, /recap buckets+filters+empty")
}

// ── helpers (mirrors e2e_api_p1.go) ─────────────────────────────────

func cleanup(pool *pgxpool.Pool, pfx string) {
	bg := context.Background()
	pool.Exec(bg, `DELETE FROM pos_obligation WHERE transaction_id IN (SELECT id FROM transactions WHERE idempotency_key LIKE $1)`, pfx+"%")
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
