//go:build ignore

// e2e_api_p1 extends e2e_api.go with proof for the remaining EXISTING
// read/mutate handlers: GET/PATCH/DELETE accounts + pos, GET
// counterparties, GET transactions (list+filters), GET balances, and
// GET/POST income-templates + POST /:id/apply.
//
// Style mirrors scripts/e2e_api.go + e2e_api_read.go: strict status
// codes, error-code bodies, DB row verification, idempotency checks.
//
//	export DATABASE_URL=postgres://postgreuser@localhost:5432/financial_shima?sslmode=disable
//	export LLM_API_KEY=test-api-key-for-e2e
//	go run ./scripts/e2e_api_p1.go
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
			die("resolve user %q: %v", u.TelegramIdentifier, err)
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
	api.GET("/accounts/:id", h.APIAccountGet)
	api.PATCH("/accounts/:id", h.APIAccountUpdate)
	api.DELETE("/accounts/:id", h.APIAccountArchive)
	api.GET("/pos", h.APIPosList)
	api.POST("/pos", h.APIPosCreate)
	api.GET("/pos/:id", h.APIPosGet)
	api.PATCH("/pos/:id", h.APIPosUpdate)
	api.DELETE("/pos/:id", h.APIPosArchive)
	api.GET("/counterparties", h.APICounterpartiesList)
	api.POST("/counterparties", h.APICounterpartiesCreate)
	api.GET("/transactions", h.APITransactionsList)
	api.POST("/transactions", h.APITransactionsCreate)
	api.GET("/balances", h.APIBalancesGet)
	api.GET("/income-templates", h.APIIncomeTemplatesList)
	api.POST("/income-templates", h.APIIncomeTemplatesCreate)
	api.POST("/income-templates/:id/apply", h.APIIncomeTemplateApply)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	pfx := fmt.Sprintf("e2e-p1-%d", stamp)
	defer cleanup(pool, pfx)

	today := time.Now().Format("2006-01-02")

	// ── Seed: accA, accB, accDel, posP, posQ, posR, cp, 1 txn ──────
	var accA, accB, accDel map[string]any
	postJSON(srv.URL+"/api/v1/accounts", mustJSON(map[string]string{"name": pfx + "-acc-a"}), 201, &accA)
	postJSON(srv.URL+"/api/v1/accounts", mustJSON(map[string]string{"name": pfx + "-acc-b"}), 201, &accB)
	postJSON(srv.URL+"/api/v1/accounts", mustJSON(map[string]string{"name": pfx + "-acc-del"}), 201, &accDel)
	accAID := accA["id"].(string)
	accBID := accB["id"].(string)
	accDelID := accDel["id"].(string)

	var posP map[string]any
	postJSON(srv.URL+"/api/v1/pos", mustJSON(map[string]any{
		"name": pfx + "-pos-p", "currency": "idr",
		"target": int64(12000000), "account_id": accAID,
	}), 201, &posP)
	posPID := posP["id"].(string)

	posIDs := map[string]string{}
	for _, n := range []string{"q", "r"} {
		var p map[string]any
		postJSON(srv.URL+"/api/v1/pos", mustJSON(map[string]any{
			"name": pfx + "-pos-" + n, "currency": "idr", "account_id": accAID,
		}), 201, &p)
		posIDs[n] = p["id"].(string)
	}

	var cp map[string]any
	postJSON(srv.URL+"/api/v1/counterparties",
		mustJSON(map[string]string{"name": pfx + "-cp"}), 201, &cp)
	cpID := cp["id"].(string)

	postJSON(srv.URL+"/api/v1/transactions", mustJSON(map[string]any{
		"type": "money_in", "effective_date": today,
		"account_amount": int64(800000), "pos_id": posPID, "pos_amount": int64(800000),
		"counterparty_id": cpID, "note": "p1 seed",
		"idempotency_key": pfx + "-txn-1",
	}), 201, nil)
	pass("seeded 3 accounts, 3 pos, 1 counterparty, 1 transaction")

	// ── P1 GET /accounts/:id → 200, shape + DB match ───────────────
	{
		var got map[string]any
		getJSON(srv.URL+"/api/v1/accounts/"+accAID, &got)
		if got["id"].(string) != accAID || got["name"].(string) != pfx+"-acc-a" {
			die("P1 GET account: id=%v name=%v", got["id"], got["name"])
		}
		var dbName string
		var dbArchived bool
		if err := pool.QueryRow(ctx, `SELECT name, archived FROM accounts WHERE id = $1`, accAID).Scan(&dbName, &dbArchived); err != nil {
			die("P1 DB lookup account: %v", err)
		}
		if dbName != pfx+"-acc-a" || dbArchived {
			die("P1 DB row: name=%q archived=%v", dbName, dbArchived)
		}
		pass("P1 GET /accounts/:id → 200 + DB row verified")
	}

	// ── P2 GET /accounts/:id errors: unknown → 404, bad UUID → 400 ─
	{
		getExpectStatus(srv.URL+"/api/v1/accounts/00000000-0000-0000-0000-000000000000", 404, "not_found")
		getExpectStatus(srv.URL+"/api/v1/accounts/not-a-uuid", 400, "validation_failed")
		pass("P2 GET /accounts/:id unknown → 404, malformed → 400")
	}

	// ── P3 PATCH /accounts/:id rename → 200 + DB verify ────────────
	{
		var updated map[string]any
		patchJSON(srv.URL+"/api/v1/accounts/"+accAID,
			mustJSON(map[string]string{"name": pfx + "-acc-a-renamed"}), 200, &updated)
		if updated["name"].(string) != pfx+"-acc-a-renamed" {
			die("P3 PATCH response name = %v", updated["name"])
		}
		var dbName string
		if err := pool.QueryRow(ctx, `SELECT name FROM accounts WHERE id = $1`, accAID).Scan(&dbName); err != nil {
			die("P3 DB lookup: %v", err)
		}
		if dbName != pfx+"-acc-a-renamed" {
			die("P3 DB name = %q", dbName)
		}
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/accounts", &list)
		if !find(list, "id", accAID) {
			die("P3 renamed account missing from list")
		}
		patchExpectStatus(srv.URL+"/api/v1/accounts/"+accAID,
			mustJSON(map[string]string{"name": "   "}), 400, "validation_failed")
		patchExpectStatus(srv.URL+"/api/v1/accounts/00000000-0000-0000-0000-000000000000",
			mustJSON(map[string]string{"name": "x"}), 404, "not_found")
		pass("P3 PATCH /accounts/:id rename → 200 + DB verified; empty → 400, unknown → 404")
	}

	// ── P4 GET /pos + GET /pos/:id → 200; errors 404/400 ───────────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/pos", &list)
		if !find(list, "id", posPID) {
			die("P4 GET /pos: seeded pos missing")
		}
		var got map[string]any
		getJSON(srv.URL+"/api/v1/pos/"+posPID, &got)
		if got["currency"].(string) != "idr" || got["account_id"].(string) != accAID {
			die("P4 GET pos: currency=%v account=%v", got["currency"], got["account_id"])
		}
		if t, ok := got["target"].(float64); !ok || int64(t) != 12000000 {
			die("P4 GET pos target = %v (want 12000000)", got["target"])
		}
		getExpectStatus(srv.URL+"/api/v1/pos/00000000-0000-0000-0000-000000000000", 404, "not_found")
		getExpectStatus(srv.URL+"/api/v1/pos/not-a-uuid", 400, "validation_failed")
		pass("P4 GET /pos + /pos/:id → 200 (currency, account_id, target); unknown → 404, malformed → 400")
	}

	// ── P5 GET /transactions list + filters ────────────────────────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/transactions", &list)
		if !findBy(list, "idempotency_key", pfx+"-txn-1") {
			die("P5 default list: seeded txn missing")
		}
		var byType []map[string]any
		getJSON(srv.URL+"/api/v1/transactions?type=money_in", &byType)
		for _, t := range byType {
			if t["type"].(string) != "money_in" {
				die("P5 type filter leaked %v", t["type"])
			}
		}
		if !findBy(byType, "idempotency_key", pfx+"-txn-1") {
			die("P5 type=money_in missing seeded txn")
		}
		var byIDs []map[string]any
		getJSON(srv.URL+"/api/v1/transactions?account_id="+accAID+"&pos_id="+posPID+"&counterparty_id="+cpID, &byIDs)
		matched := 0
		for _, t := range byIDs {
			if t["account_id"] != accAID || t["pos_id"] != posPID || t["counterparty_id"] != cpID {
				die("P5 id filter leaked: %v", t)
			}
			if strings.HasPrefix(t["idempotency_key"].(string), pfx) {
				matched++
			}
		}
		if matched != 1 {
			die("P5 id filters: matched %d this-run rows (want 1)", matched)
		}
		// Explicit wide date range still contains the txn; a future-only
		// window excludes it.
		var wide []map[string]any
		getJSON(srv.URL+"/api/v1/transactions?from=2000-01-01&to=2100-01-01", &wide)
		if !findBy(wide, "idempotency_key", pfx+"-txn-1") {
			die("P5 wide date range missing seeded txn")
		}
		futureFrom := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
		futureTo := time.Now().AddDate(0, 0, 60).Format("2006-01-02")
		var future []map[string]any
		getJSON(srv.URL+"/api/v1/transactions?from="+futureFrom+"&to="+futureTo, &future)
		if findBy(future, "idempotency_key", pfx+"-txn-1") {
			die("P5 future window should exclude seeded txn")
		}
		// Invalid filters → 400 validation_failed.
		getExpectStatus(srv.URL+"/api/v1/transactions?type=bogus", 400, "validation_failed")
		getExpectStatus(srv.URL+"/api/v1/transactions?from=not-a-date", 400, "validation_failed")
		getExpectStatus(srv.URL+"/api/v1/transactions?account_id=bad", 400, "validation_failed")
		pass("P5 GET /transactions list + type/id/date filters + 400 on bad filters")
	}

	// ── P6 GET /balances: our account 800k, IDR reconciled ─────────
	{
		var bal map[string]any
		getJSON(srv.URL+"/api/v1/balances", &bal)
		var ours map[string]any
		for _, a := range bal["accounts"].([]any) {
			if m, ok := a.(map[string]any); ok && m["id"] == accAID {
				ours = m
				break
			}
		}
		if ours == nil {
			die("P6 /balances: our account missing")
		}
		if int64(ours["balance_idr"].(float64)) != 800000 {
			die("P6 balance_idr = %v (want 800000)", ours["balance_idr"])
		}
		var idrRecon map[string]any
		for _, r := range bal["reconciliation"].([]any) {
			if m, ok := r.(map[string]any); ok && m["currency"] == "idr" {
				idrRecon = m
				break
			}
		}
		if idrRecon == nil {
			die("P6 idr reconciliation row missing")
		}
		if !idrRecon["equal"].(bool) {
			die("P6 idr not reconciled: account=%v pos=%v diff=%v",
				idrRecon["account_total"], idrRecon["pos_total"], idrRecon["difference"])
		}
		pass("P6 GET /balances → acc=Rp 800.000, IDR reconciliation equal=true")
	}

	// ── P7 PATCH /pos/:id rename+target → 200 + DB verify ──────────
	{
		var updated map[string]any
		patchJSON(srv.URL+"/api/v1/pos/"+posPID,
			mustJSON(map[string]any{"name": pfx + "-pos-p-renamed", "target": int64(5000000)}),
			200, &updated)
		if updated["name"].(string) != pfx+"-pos-p-renamed" {
			die("P7 PATCH name = %v", updated["name"])
		}
		var dbName string
		var dbTarget *int64
		if err := pool.QueryRow(ctx, `SELECT name, target FROM pos WHERE id = $1`, posPID).Scan(&dbName, &dbTarget); err != nil {
			die("P7 DB lookup: %v", err)
		}
		if dbName != pfx+"-pos-p-renamed" || dbTarget == nil || *dbTarget != 5000000 {
			die("P7 DB row: name=%q target=%v", dbName, dbTarget)
		}
		pass("P7 PATCH /pos/:id rename+target → 200 + DB verified")
	}

	// ── P8 PATCH /pos/:id move account → 200 + DB verify ───────────
	{
		var moved map[string]any
		patchJSON(srv.URL+"/api/v1/pos/"+posPID,
			mustJSON(map[string]any{"account_id": accBID}), 200, &moved)
		if moved["account_id"].(string) != accBID {
			die("P8 PATCH account_id = %v (want %s)", moved["account_id"], accBID)
		}
		var dbAcc string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM accounts WHERE id = (SELECT account_id FROM pos WHERE id = $1)`, posPID).Scan(&dbAcc); err != nil {
			die("P8 DB lookup: %v", err)
		}
		if dbAcc != accBID {
			die("P8 DB account_id = %s (want %s)", dbAcc, accBID)
		}
		// Reconciliation must still hold after the move (§5.6 snapshot).
		var bal map[string]any
		getJSON(srv.URL+"/api/v1/balances", &bal)
		for _, r := range bal["reconciliation"].([]any) {
			if m, ok := r.(map[string]any); ok && m["currency"] == "idr" && !m["equal"].(bool) {
				die("P8 idr unbalanced after move")
			}
		}
		pass("P8 PATCH /pos/:id account move → 200 + DB verified, IDR still reconciled")
	}

	// ── P9 PATCH /pos/:id errors ───────────────────────────────────
	{
		patchExpectStatus(srv.URL+"/api/v1/pos/"+posPID, mustJSON(map[string]any{}), 400, "validation_failed")
		patchExpectStatus(srv.URL+"/api/v1/pos/"+posPID, mustJSON(map[string]any{"target": int64(-1)}), 400, "validation_failed")
		patchExpectStatus(srv.URL+"/api/v1/pos/"+posPID,
			mustJSON(map[string]any{"account_id": "00000000-0000-0000-0000-000000000000"}), 400, "validation_failed")
		patchExpectStatus(srv.URL+"/api/v1/pos/00000000-0000-0000-0000-000000000000",
			mustJSON(map[string]any{"name": "x"}), 404, "not_found")
		pass("P9 PATCH /pos/:id empty/negative/bad-account → 400, unknown pos → 404")
	}

	// ── P10 DELETE /pos/:id → 204, hidden by default, archived=true ─
	{
		deleteExpect(srv.URL+"/api/v1/pos/"+posPID, 204)
		var def []map[string]any
		getJSON(srv.URL+"/api/v1/pos", &def)
		if find(def, "id", posPID) {
			die("P10 archived pos still in default list")
		}
		var withArch []map[string]any
		getJSON(srv.URL+"/api/v1/pos?include_archived=true", &withArch)
		found := false
		for _, p := range withArch {
			if p["id"] == posPID {
				found = true
				if !p["archived"].(bool) {
					die("P10 include_archived row archived=false")
				}
			}
		}
		if !found {
			die("P10 archived pos missing with ?include_archived=true")
		}
		var got map[string]any
		getJSON(srv.URL+"/api/v1/pos/"+posPID, &got)
		if !got["archived"].(bool) {
			die("P10 GET :id after delete archived=false")
		}
		var dbArchived bool
		if err := pool.QueryRow(ctx, `SELECT archived FROM pos WHERE id = $1`, posPID).Scan(&dbArchived); err != nil {
			die("P10 DB lookup: %v", err)
		}
		if !dbArchived {
			die("P10 DB archived=false")
		}
		deleteExpect(srv.URL+"/api/v1/pos/00000000-0000-0000-0000-000000000000", 404)
		pass("P10 DELETE /pos/:id → 204 + archived filter + DB verified; unknown → 404")
	}

	// ── P11 DELETE /accounts/:id → 204, hidden by default ──────────
	{
		deleteExpect(srv.URL+"/api/v1/accounts/"+accDelID, 204)
		var def []map[string]any
		getJSON(srv.URL+"/api/v1/accounts", &def)
		if find(def, "id", accDelID) {
			die("P11 archived account still in default list")
		}
		var withArch []map[string]any
		getJSON(srv.URL+"/api/v1/accounts?include_archived=true", &withArch)
		found := false
		for _, a := range withArch {
			if a["id"] == accDelID {
				found = true
				if !a["archived"].(bool) {
					die("P11 include_archived row archived=false")
				}
			}
		}
		if !found {
			die("P11 archived account missing with ?include_archived=true")
		}
		var dbArchived bool
		if err := pool.QueryRow(ctx, `SELECT archived FROM accounts WHERE id = $1`, accDelID).Scan(&dbArchived); err != nil {
			die("P11 DB lookup: %v", err)
		}
		if !dbArchived {
			die("P11 DB archived=false")
		}
		deleteExpect(srv.URL+"/api/v1/accounts/00000000-0000-0000-0000-000000000000", 404)
		pass("P11 DELETE /accounts/:id → 204 + archived filter + DB verified; unknown → 404")
	}

	// ── P12 GET /counterparties (+?q=) ──────────────────────────────
	{
		var list []map[string]any
		getJSON(srv.URL+"/api/v1/counterparties", &list)
		if !find(list, "id", cpID) {
			die("P12 seeded cp missing from list")
		}
		var hits []map[string]any
		getJSON(srv.URL+"/api/v1/counterparties?q="+pfx+"-cp", &hits)
		if !find(hits, "id", cpID) {
			die("P12 ?q= prefix missed seeded cp")
		}
		var none []map[string]any
		getJSON(srv.URL+"/api/v1/counterparties?q=zzz_no_match_zzz", &none)
		if len(none) != 0 {
			die("P12 ?q= miss: got %d (want 0)", len(none))
		}
		pass("P12 GET /counterparties full + ?q= prefix + miss")
	}

	// ── P13 GET+POST /income-templates ─────────────────────────────
	{
		var before []map[string]any
		getJSON(srv.URL+"/api/v1/income-templates", &before)
		var tmpl map[string]any
		postJSON(srv.URL+"/api/v1/income-templates", mustJSON(map[string]any{
			"name": pfx + "-tpl",
			"lines": []map[string]any{
				{"pos_id": posIDs["q"], "amount": int64(300000), "sort_order": int32(1)},
				{"pos_id": posIDs["r"], "amount": int64(200000), "sort_order": int32(2)},
			},
		}), 201, &tmpl)
		tmplID := tmpl["id"].(string)
		if len(tmpl["lines"].([]any)) != 2 {
			die("P13 create lines = %d (want 2)", len(tmpl["lines"].([]any)))
		}
		var dbLines int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM income_template_line WHERE template_id = $1`, tmplID).Scan(&dbLines); err != nil {
			die("P13 DB count lines: %v", err)
		}
		if dbLines != 2 {
			die("P13 DB lines = %d (want 2)", dbLines)
		}
		var after []map[string]any
		getJSON(srv.URL+"/api/v1/income-templates", &after)
		if !find(after, "id", tmplID) {
			die("P13 created template missing from GET list")
		}
		postJSONExpectStatus(srv.URL+"/api/v1/income-templates",
			mustJSON(map[string]any{
				"name": pfx + "-tpl-bad",
				"lines": []map[string]any{
					{"pos_id": posIDs["q"], "amount": int64(0), "sort_order": int32(1)},
				},
			}), 400, "validation_failed")

		// P14 apply exact sum → 201 + 2 txns; idempotent re-apply.
		idem := pfx + "-apply-exact"
		var apply1 map[string]any
		postJSON(srv.URL+"/api/v1/income-templates/"+tmplID+"/apply", mustJSON(map[string]any{
			"amount": int64(500000), "effective_date": today,
			"counterparty_id": cpID, "note": "p1 apply",
			"idempotency_key": idem,
		}), 201, &apply1)
		ids1, _ := apply1["transaction_ids"].([]any)
		if len(ids1) != 2 {
			die("P14 transaction_ids = %d (want 2)", len(ids1))
		}
		var sum int64
		var cnt int
		if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(account_amount),0)::bigint, count(*) FROM transactions WHERE idempotency_key LIKE $1`, idem+":%").Scan(&sum, &cnt); err != nil {
			die("P14 DB sum/count: %v", err)
		}
		if cnt != 2 || sum != 500000 {
			die("P14 DB rows: cnt=%d sum=%d (want 2/500000)", cnt, sum)
		}
		var apply2 map[string]any
		postJSON(srv.URL+"/api/v1/income-templates/"+tmplID+"/apply", mustJSON(map[string]any{
			"amount": int64(500000), "effective_date": today,
			"counterparty_id": cpID, "note": "p1 apply",
			"idempotency_key": idem,
		}), 201, &apply2)
		ids2, _ := apply2["transaction_ids"].([]any)
		if len(ids1) != len(ids2) {
			die("P14 re-apply id count differs")
		}
		for i := range ids1 {
			if ids1[i] != ids2[i] {
				die("P14 re-apply id[%d] differs", i)
			}
		}
		var afterCnt int
		pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE idempotency_key LIKE $1`, idem+":%").Scan(&afterCnt)
		if afterCnt != 2 {
			die("P14 re-apply added rows: now %d (want 2)", afterCnt)
		}
		postJSONExpectStatus(srv.URL+"/api/v1/income-templates/"+tmplID+"/apply",
			mustJSON(map[string]any{
				"amount": int64(100000), "effective_date": today,
				"counterparty_id": cpID, "idempotency_key": pfx + "-apply-below",
			}), 400, "validation_failed")
		postJSONExpectStatus(srv.URL+"/api/v1/income-templates/00000000-0000-0000-0000-000000000000/apply",
			mustJSON(map[string]any{
				"amount": int64(500000), "effective_date": today,
				"counterparty_id": cpID, "idempotency_key": pfx + "-apply-missing",
			}), 404, "not_found")
		pass("P13-14 income-templates GET/POST + apply exact/idempotent/below-400/unknown-404 + DB verified")
	}

	fmt.Println()
	fmt.Println("PASS — P1 e2e: accounts/pos mutate, counterparties, transactions filters, balances, income-templates")
}

// ── helpers ─────────────────────────────────────────────────────────

func cleanup(pool *pgxpool.Pool, pfx string) {
	bg := context.Background()
	pool.Exec(bg, `DELETE FROM transactions WHERE idempotency_key LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM income_template_line WHERE template_id IN (SELECT id FROM income_template WHERE name LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM income_template WHERE name LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM pos WHERE name LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM accounts WHERE name LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM counterparties WHERE name LIKE $1`, pfx+"%")
}

func find(list []map[string]any, key, val string) bool {
	for _, m := range list {
		if v, ok := m[key].(string); ok && v == val {
			return true
		}
	}
	return false
}

func findBy(list []map[string]any, key, val string) bool { return find(list, key, val) }

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

func postJSONExpectStatus(url string, body []byte, wantStatus int, wantErrCode string) {
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

func patchJSON(url string, body []byte, wantStatus int, dst interface{}) {
	status, resp := doReq("PATCH", url, body)
	if status != wantStatus {
		die("PATCH %s: status=%d want=%d body=%s", url, status, wantStatus, string(resp))
	}
	if dst != nil {
		if err := json.Unmarshal(resp, dst); err != nil {
			die("decode %s: %v body=%s", url, err, string(resp))
		}
	}
}

func patchExpectStatus(url string, body []byte, wantStatus int, wantErrCode string) {
	status, resp := doReq("PATCH", url, body)
	if status != wantStatus {
		die("PATCH %s: status=%d want=%d body=%s", url, status, wantStatus, string(resp))
	}
	var apiErr struct {
		Error string `json:"error"`
	}
	json.Unmarshal(resp, &apiErr)
	if apiErr.Error != wantErrCode {
		die("PATCH %s: error=%q want %q", url, apiErr.Error, wantErrCode)
	}
}

func deleteExpect(url string, wantStatus int) {
	status, body := doReq("DELETE", url, nil)
	if status != wantStatus {
		die("DELETE %s: status=%d want=%d body=%s", url, status, wantStatus, string(body))
	}
	if wantStatus == 404 {
		var apiErr struct {
			Error string `json:"error"`
		}
		json.Unmarshal(body, &apiErr)
		if apiErr.Error != "not_found" {
			die("DELETE %s: error=%q want not_found", url, apiErr.Error)
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
