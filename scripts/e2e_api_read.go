//go:build ignore

// e2e_api_read drives the four /api/v1 read endpoints end-to-end
// against the real handler tree + real Postgres. The walk:
//
//   1. Seed an account, two Pos (idr + gold-g), a counterparty.
//   2. Seed two transactions to drive non-zero balances.
//   3. GET each read endpoint with the api key and assert content.
//   4. Reconciliation: assert IDR account_total == idr pos_total.
//
//   export DATABASE_URL=…
//   export LLM_API_KEY=test-api-key-for-e2e
//   go run ./scripts/e2e_api_read.go
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
	"strings"
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
			die("resolve user %s: %v", u.TelegramIdentifier, err)
		}
		users[i].ID = idStr
	}

	a := auth.New(users, clock.System{}, rand.Reader, idgen.Crypto{})
	h := handler.New(a, &assistant.Recorder{}, pool)

	e := echo.New()
	e.Renderer = template.New()
	api := e.Group("/api/v1", mw.APIKey(apiKey))
	api.GET("/accounts", h.APIAccountsList)
	api.POST("/accounts", h.APIAccountsCreate)
	api.GET("/pos", h.APIPosList)
	api.POST("/pos", h.APIPosCreate)
	api.GET("/counterparties", h.APICounterpartiesList)
	api.POST("/counterparties", h.APICounterpartiesCreate)
	api.GET("/transactions", h.APITransactionsList)
	api.POST("/transactions", h.APITransactionsCreate)
	api.GET("/balances", h.APIBalancesGet)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	defer cleanup(pool, stamp)

	// ── Seed: 1 account, 2 pos, 1 counterparty, 2 txns ───────────────
	var account map[string]any
	postJSON(srv.URL+"/api/v1/accounts",
		mustJSON(map[string]string{"name": fmt.Sprintf("e2e-r-acc-%d", stamp)}),
		201, &account)
	accID := account["id"].(string)

	posIDs := map[string]string{}
	for _, spec := range []struct{ name, currency string }{
		{"e2e-r-pos-idr-" + tsk(stamp), "idr"},
		{"e2e-r-pos-gold-" + tsk(stamp), "gold-g"},
	} {
		var p map[string]any
		postJSON(srv.URL+"/api/v1/pos",
			mustJSON(map[string]any{"name": spec.name, "currency": spec.currency}),
			201, &p)
		posIDs[spec.currency] = p["id"].(string)
	}

	var cp map[string]any
	postJSON(srv.URL+"/api/v1/counterparties",
		mustJSON(map[string]string{"name": "e2e-r-cp-" + tsk(stamp)}),
		201, &cp)
	cpID := cp["id"].(string)

	// 1.5M IDR money_in
	postJSON(srv.URL+"/api/v1/transactions",
		mustJSON(map[string]any{
			"type":            "money_in",
			"effective_date":  time.Now().Format("2006-01-02"),
			"account_id":      accID,
			"account_amount":  1_500_000,
			"pos_id":          posIDs["idr"],
			"pos_amount":      1_500_000,
			"counterparty_id": cpID,
			"idempotency_key": fmt.Sprintf("e2e-r-%d-1", stamp),
		}), 201, nil)
	// 250k IDR money_out
	postJSON(srv.URL+"/api/v1/transactions",
		mustJSON(map[string]any{
			"type":            "money_out",
			"effective_date":  time.Now().Format("2006-01-02"),
			"account_id":      accID,
			"account_amount":  250_000,
			"pos_id":          posIDs["idr"],
			"pos_amount":      250_000,
			"counterparty_id": cpID,
			"idempotency_key": fmt.Sprintf("e2e-r-%d-2", stamp),
		}), 201, nil)
	pass("seeded 1 account, 2 pos, 1 counterparty, 2 transactions")

	// ── S1 GET /api/v1/accounts contains seeded account ─────────────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/accounts", &list)
		if !find(list, "id", accID) {
			die("S1 GET /accounts: created account not in list")
		}
		pass("S1 GET /accounts → seeded account present")
	}

	// ── S2 GET /api/v1/pos has both, with currencies ────────────────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/pos", &list)
		if !find(list, "id", posIDs["idr"]) {
			die("S2 GET /pos: idr pos missing")
		}
		if !find(list, "id", posIDs["gold-g"]) {
			die("S2 GET /pos: gold-g pos missing")
		}
		// Confirm currency surfaces in JSON
		var idrPos map[string]any
		for _, p := range list {
			if p["id"] == posIDs["idr"] {
				idrPos = p
				break
			}
		}
		if idrPos["currency"].(string) != "idr" {
			die("S2 GET /pos: idr pos currency = %v", idrPos["currency"])
		}
		pass("S2 GET /pos → both pos present + currencies correct")
	}

	// ── S3 GET /api/v1/counterparties + ?q= prefix search ──────────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/counterparties", &list)
		if !find(list, "id", cpID) {
			die("S3 GET /counterparties: seeded cp missing")
		}
		var hits []map[string]any
		getJSON(srv.URL+"/api/v1/counterparties?q=e2e-r-cp", &hits)
		if !find(hits, "id", cpID) {
			die("S3 GET /counterparties?q=…: prefix match missing")
		}
		// Negative: a definitely-no-match prefix returns []
		var none []map[string]any
		getJSON(srv.URL+"/api/v1/counterparties?q=zzz_no_match_zzz", &none)
		if len(none) != 0 {
			die("S3 GET /counterparties?q=zzz: got %d (want 0)", len(none))
		}
		pass("S3 GET /counterparties (full + ?q= prefix + miss) all behave")
	}

	// ── S4 GET /api/v1/transactions (default range) lists both ─────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/transactions", &list)
		var foundIn, foundOut bool
		for _, t := range list {
			idem, _ := t["idempotency_key"].(string)
			switch idem {
			case fmt.Sprintf("e2e-r-%d-1", stamp):
				foundIn = true
				if t["account_amount"].(float64) != 1_500_000 {
					die("S4 money_in account_amount = %v", t["account_amount"])
				}
				if t["account_name"].(string) != fmt.Sprintf("e2e-r-acc-%d", stamp) {
					die("S4 money_in account_name = %v", t["account_name"])
				}
			case fmt.Sprintf("e2e-r-%d-2", stamp):
				foundOut = true
			}
		}
		if !foundIn || !foundOut {
			die("S4 transactions: in=%v out=%v", foundIn, foundOut)
		}
		pass("S4 GET /transactions → both seeded txns present, names joined")
	}

	// ── S5 GET /transactions?type=money_in filters ──────────────────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/transactions?type=money_in", &list)
		for _, t := range list {
			if t["type"].(string) != "money_in" {
				die("S5 type filter leaked %v", t["type"])
			}
		}
		pass("S5 GET /transactions?type=money_in → only money_in rows")
	}

	// ── S6 GET /transactions?account_id= + ?pos_id= filter ─────────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/transactions?account_id="+accID+"&pos_id="+posIDs["idr"], &list)
		// We seeded exactly 2 txns matching this combo for THIS run;
		// other rows in shared DB may also match, so we just assert
		// >= 2 and that all returned rows have the right ids.
		matchedThisRun := 0
		for _, t := range list {
			if t["account_id"] != accID || t["pos_id"] != posIDs["idr"] {
				die("S6 filter leaked: account=%v pos=%v", t["account_id"], t["pos_id"])
			}
			idem := t["idempotency_key"].(string)
			if strings.HasPrefix(idem, fmt.Sprintf("e2e-r-%d", stamp)) {
				matchedThisRun++
			}
		}
		if matchedThisRun != 2 {
			die("S6 our 2 seeded txns: matched %d", matchedThisRun)
		}
		pass("S6 GET /transactions?account_id&pos_id → exactly the seeded pair, no leakage")
	}

	// ── S7 GET /balances reconciliation: net IDR = 1.5M - 250k = 1.25M ─
	{
		var bal map[string]any
		getJSON(srv.URL+"/api/v1/balances", &bal)
		// Find the account row matching ours; its balance contributes
		// 1.25M to the IDR account total. Other seeded accounts in the
		// shared DB may also contribute, so we don't assert the exact
		// IDR account total — but we DO assert reconciliation parity:
		// IDR account total == IDR pos total.
		var ours map[string]any
		for _, a := range bal["accounts"].([]any) {
			if m, ok := a.(map[string]any); ok && m["id"] == accID {
				ours = m
				break
			}
		}
		if ours == nil {
			die("S7 /balances: our account missing")
		}
		if int64(ours["balance_idr"].(float64)) != 1_250_000 {
			die("S7 our account balance_idr = %v (want 1250000)", ours["balance_idr"])
		}
		// Reconciliation IDR row: account_total - pos_total should be
		// the same delta for every account/pos pair, so equal must hold
		// across the whole IDR slice (regardless of other seeded data).
		var idrRecon map[string]any
		for _, r := range bal["reconciliation"].([]any) {
			if m, ok := r.(map[string]any); ok && m["currency"] == "idr" {
				idrRecon = m
				break
			}
		}
		if idrRecon == nil {
			die("S7 /balances: idr reconciliation row missing")
		}
		if !idrRecon["equal"].(bool) {
			die("S7 reconciliation: idr not balanced; account=%v pos=%v difference=%v",
				idrRecon["account_total"], idrRecon["pos_total"], idrRecon["difference"])
		}
		pass(fmt.Sprintf("S7 /balances → our acc balance=Rp 1.250.000; IDR reconciled (Σ Account = Σ Pos.cash = %v)", idrRecon["account_total"]))
	}

	// ── S8 Default-archived filter: archived pos hidden by default ──
	{
		// Archive our gold-g pos directly via SQL
		if _, err := pool.Exec(ctx,
			`UPDATE pos SET archived = true WHERE id = $1`, posIDs["gold-g"],
		); err != nil {
			die("S8 archive sql: %v", err)
		}
		var hidden []map[string]any
		getJSON(srv.URL+"/api/v1/pos", &hidden)
		if find(hidden, "id", posIDs["gold-g"]) {
			die("S8 archived pos still visible without ?include_archived=true")
		}
		var withArchived []map[string]any
		getJSON(srv.URL+"/api/v1/pos?include_archived=true", &withArchived)
		if !find(withArchived, "id", posIDs["gold-g"]) {
			die("S8 archived pos missing with ?include_archived=true")
		}
		pass("S8 archived filter: default omits, ?include_archived=true includes")
	}

	fmt.Println()
	fmt.Println("PASS — every /api/v1 read endpoint works end-to-end")
}

// ── helpers ────────────────────────────────────────────────────────

func cleanup(pool *pgxpool.Pool, stamp int64) {
	bg := context.Background()
	pool.Exec(bg, `DELETE FROM transactions WHERE idempotency_key LIKE $1`, fmt.Sprintf("e2e-r-%d-%%", stamp))
	pool.Exec(bg, `DELETE FROM pos WHERE name LIKE $1`, fmt.Sprintf("e2e-r-pos-%%-%d", stamp))
	pool.Exec(bg, `DELETE FROM accounts WHERE name = $1`, fmt.Sprintf("e2e-r-acc-%d", stamp))
	pool.Exec(bg, `DELETE FROM counterparties WHERE name = $1`, fmt.Sprintf("e2e-r-cp-%d", stamp))
}

func tsk(n int64) string { return fmt.Sprintf("%d", n) }

func find(list []map[string]any, key, val string) bool {
	for _, m := range list {
		if v, ok := m[key].(string); ok && v == val {
			return true
		}
	}
	return false
}

func getJSON(url string, dst interface{}) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("x-api-key", apiKey)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		die("GET %s: %v", url, err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		die("GET %s: status=%d body=%s", url, r.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, dst); err != nil {
		die("decode %s: %v body=%s", url, err, string(body))
	}
}

func postJSON(url string, body []byte, wantStatus int, dst interface{}) {
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		die("POST %s: %v", url, err)
	}
	defer r.Body.Close()
	respBody, _ := io.ReadAll(r.Body)
	if r.StatusCode != wantStatus {
		die("POST %s: status=%d want=%d body=%s", url, r.StatusCode, wantStatus, string(respBody))
	}
	if dst != nil {
		json.Unmarshal(respBody, dst)
	}
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func pass(msg string) { fmt.Printf("✓ %s\n", msg) }
func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", args...)
	os.Exit(1)
}
