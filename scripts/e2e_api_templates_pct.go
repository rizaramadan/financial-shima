//go:build ignore

// e2e_api_templates_pct proves percent income-templates over REST JSON:
//
//	create percent template → preview → apply exact/above/below →
//	Σ == amount, idempotent re-apply, preview matches apply,
//	fixed+percent mix, PATCH edit.
//
//	export DATABASE_URL=postgres://postgreuser@localhost:5432/financial_shima?sslmode=disable
//	export LLM_API_KEY=test-api-key-for-e2e
//	go run ./scripts/e2e_api_templates_pct.go
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

const pctAPIKey = "test-api-key-for-e2e"

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		pctDie("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		pctDie("pool: %v", err)
	}
	defer pool.Close()

	users := user.Seeded()
	for i, u := range users {
		var idStr string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE telegram_identifier = $1`,
			u.TelegramIdentifier,
		).Scan(&idStr); err != nil {
			pctDie("resolve user %q: %v", u.TelegramIdentifier, err)
		}
		users[i].ID = idStr
	}
	a := auth.New(users, clock.System{}, rand.Reader, idgen.Crypto{})
	h := handler.New(a, &assistant.Recorder{}, pool)

	e := echo.New()
	e.Renderer = template.New()
	api := e.Group("/api/v1", mw.APIKey(pctAPIKey))
	api.GET("/income-templates", h.APIIncomeTemplatesList)
	api.POST("/income-templates", h.APIIncomeTemplatesCreate)
	api.PATCH("/income-templates/:id", h.APIIncomeTemplateUpdate)
	api.POST("/income-templates/:id/preview", h.APIIncomeTemplatePreview)
	api.POST("/income-templates/:id/apply", h.APIIncomeTemplateApply)
	api.POST("/pos", h.APIPosCreate)
	api.POST("/accounts", h.APIAccountsCreate)
	api.POST("/counterparties", h.APICounterpartiesCreate)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pctPass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	pfx := fmt.Sprintf("e2e-pct-%d", stamp)
	defer pctCleanup(pool, pfx)
	today := time.Now().Format("2006-01-02")

	var acc map[string]any
	pctPostJSON(srv.URL+"/api/v1/accounts", pctMustJSON(map[string]string{"name": pfx + "-acc"}), 201, &acc)
	accID := acc["id"].(string)

	posIDs := map[string]string{}
	for _, n := range []string{"a", "b", "left"} {
		var p map[string]any
		pctPostJSON(srv.URL+"/api/v1/pos", pctMustJSON(map[string]any{
			"name": pfx + "-pos-" + n, "currency": "idr", "account_id": accID,
		}), 201, &p)
		posIDs[n] = p["id"].(string)
	}
	var cp map[string]any
	pctPostJSON(srv.URL+"/api/v1/counterparties",
		pctMustJSON(map[string]string{"name": pfx + "-cp"}), 201, &cp)
	cpID := cp["id"].(string)
	pctPass("seeded 1 account, 3 pos, 1 counterparty")

	// ── T1 create 60/40 percent template (no leftover) ────────────
	var tmpl map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates", pctMustJSON(map[string]any{
		"name": pfx + "-tpl-split",
		"lines": []map[string]any{
			{"pos_id": posIDs["a"], "amount": int64(0), "percent_bps": int64(6000), "sort_order": int32(1)},
			{"pos_id": posIDs["b"], "amount": int64(0), "percent_bps": int64(4000), "sort_order": int32(2)},
		},
	}), 201, &tmpl)
	tmplID := tmpl["id"].(string)
	lines := tmpl["lines"].([]any)
	if len(lines) != 2 {
		pctDie("T1 lines = %d (want 2)", len(lines))
	}
	for _, l := range lines {
		m := l.(map[string]any)
		if _, ok := m["percent_bps"]; !ok {
			pctDie("T1 line missing percent_bps: %v", m)
		}
	}
	pctPass("T1 POST percent template 60/40 → 201 + percent_bps echoed")

	// ── T2 preview exact amount → computed nominals ──────────────
	var prev map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates/"+tmplID+"/preview",
		pctMustJSON(map[string]any{"amount": int64(10000000)}), 200, &prev)
	if int64(prev["total"].(float64)) != 10000000 {
		pctDie("T2 preview total = %v (want 10000000)", prev["total"])
	}
	prevAllocs := prev["allocations"].([]any)
	if len(prevAllocs) != 2 {
		pctDie("T2 preview allocations = %d (want 2)", len(prevAllocs))
	}
	if pctAllocByPos(prevAllocs, posIDs["a"]) != 6000000 ||
		pctAllocByPos(prevAllocs, posIDs["b"]) != 4000000 {
		pctDie("T2 preview split wrong: %v", prevAllocs)
	}
	pctPass("T2 preview 10M → 6M/4M computed nominals, Σ == amount")

	// ── T3 apply + rounding + idempotency ────────────────────────
	var apply1 map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates/"+tmplID+"/apply", pctMustJSON(map[string]any{
		"amount": int64(10000001), "effective_date": today,
		"counterparty_id": cpID, "idempotency_key": pfx + "-apply-exact",
	}), 201, &apply1)
	ids1, _ := apply1["transaction_ids"].([]any)
	if len(ids1) != 2 {
		pctDie("T3 transaction_ids = %d (want 2)", len(ids1))
	}
	applyAllocs := apply1["allocations"].([]any)
	var sum int64
	for _, x := range applyAllocs {
		sum += int64(x.(map[string]any)["Amount"].(float64))
	}
	if sum != 10000001 {
		pctDie("T3 Σ allocations = %d (want 10000001)", sum)
	}
	if pctAllocByPos(applyAllocs, posIDs["a"])+pctAllocByPos(applyAllocs, posIDs["b"]) != sum {
		pctDie("T3 allocations do not cover both pos")
	}
	// Rounding bound: each line within 1 of exact share.
	for _, x := range applyAllocs {
		m := x.(map[string]any)
		amt := m["Amount"].(float64)
		var exact float64
		if m["PosID"].(string) == posIDs["a"] {
			exact = 0.6 * 10000001
		} else {
			exact = 0.4 * 10000001
		}
		if d := amt - exact; d > 1 || d < -1 {
			pctDie("T3 rounding >1 unit: got %v exact %v", amt, exact)
		}
	}
	// Preview at the same odd amount matches apply exactly.
	var prevOdd map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates/"+tmplID+"/preview",
		pctMustJSON(map[string]any{"amount": int64(10000001)}), 200, &prevOdd)
	for _, x := range prevOdd["allocations"].([]any) {
		m := x.(map[string]any)
		if pctAllocByPos(applyAllocs, m["PosID"].(string)) != int64(m["Amount"].(float64)) {
			pctDie("T3 preview != apply for pos %s", m["PosID"])
		}
	}
	var apply2 map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates/"+tmplID+"/apply", pctMustJSON(map[string]any{
		"amount": int64(10000001), "effective_date": today,
		"counterparty_id": cpID, "idempotency_key": pfx + "-apply-exact",
	}), 201, &apply2)
	ids2, _ := apply2["transaction_ids"].([]any)
	for i := range ids1 {
		if ids1[i] != ids2[i] {
			pctDie("T3 re-apply id[%d] differs", i)
		}
	}
	var cnt int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE idempotency_key LIKE $1`,
		pfx+"-apply-exact:%").Scan(&cnt); err != nil || cnt != 2 {
		pctDie("T3 re-apply added rows: cnt=%d err=%v (want 2)", cnt, err)
	}
	pctPass("T3 apply 10000001 → Σ == amount, rounding ≤1, preview matches, idempotent")

	// ── T4 partial percent (50%) with leftover ────────────────────
	var tmplP map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates", pctMustJSON(map[string]any{
		"name": pfx + "-tpl-partial", "leftover_pos_id": posIDs["left"],
		"lines": []map[string]any{
			{"pos_id": posIDs["a"], "amount": int64(0), "percent_bps": int64(5000), "sort_order": int32(1)},
		},
	}), 201, &tmplP)
	tmplPID := tmplP["id"].(string)
	var applyP map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates/"+tmplPID+"/apply", pctMustJSON(map[string]any{
		"amount": int64(10000000), "effective_date": today,
		"counterparty_id": cpID, "idempotency_key": pfx + "-apply-partial",
	}), 201, &applyP)
	pAllocs := applyP["allocations"].([]any)
	if len(pAllocs) != 2 {
		pctDie("T4 allocations = %d (want 2: line + leftover)", len(pAllocs))
	}
	if pctAllocByPos(pAllocs, posIDs["a"]) != 5000000 ||
		pctAllocByPos(pAllocs, posIDs["left"]) != 5000000 {
		pctDie("T4 split wrong: %v", pAllocs)
	}
	pctPass("T4 partial 50% + leftover → 5M/5M, Σ == amount")

	// ── T5 fixed + percent mix ────────────────────────────────────
	var tmplM map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates", pctMustJSON(map[string]any{
		"name": pfx + "-tpl-mix", "leftover_pos_id": posIDs["left"],
		"lines": []map[string]any{
			{"pos_id": posIDs["a"], "amount": int64(2000000), "sort_order": int32(1)},
			{"pos_id": posIDs["b"], "amount": int64(0), "percent_bps": int64(5000), "sort_order": int32(2)},
		},
	}), 201, &tmplM)
	tmplMID := tmplM["id"].(string)
	var applyM map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates/"+tmplMID+"/apply", pctMustJSON(map[string]any{
		"amount": int64(10000000), "effective_date": today,
		"counterparty_id": cpID, "idempotency_key": pfx + "-apply-mix",
	}), 201, &applyM)
	mAllocs := applyM["allocations"].([]any)
	var mSum int64
	for _, x := range mAllocs {
		mSum += int64(x.(map[string]any)["Amount"].(float64))
	}
	if mSum != 10000000 {
		pctDie("T5 Σ = %d (want 10000000)", mSum)
	}
	if pctAllocByPos(mAllocs, posIDs["a"]) != 2000000 ||
		pctAllocByPos(mAllocs, posIDs["b"]) != 5000000 ||
		pctAllocByPos(mAllocs, posIDs["left"]) != 3000000 {
		pctDie("T5 mix split wrong: %v", mAllocs)
	}
	pctPass("T5 fixed 2M + 50% + leftover → 2M/5M/3M, Σ == amount")

	// ── T6 below / above / invalid rejects ───────────────────────
	pctPostExpect(srv.URL+"/api/v1/income-templates/"+tmplMID+"/apply",
		pctMustJSON(map[string]any{
			"amount": int64(3000000), "effective_date": today,
			"counterparty_id": cpID, "idempotency_key": pfx + "-apply-below",
		}), 400, "validation_failed") // 2M + 1.5M demand > 3M
	pctPostExpect(srv.URL+"/api/v1/income-templates/"+tmplID+"/apply",
		pctMustJSON(map[string]any{
			"amount": int64(10000000), "effective_date": today,
			"counterparty_id": cpID, "idempotency_key": pfx + "-apply-partial2",
		}), 201, "") // 60/40 of anything is exact — sanity 201, not 400
	pctPostExpect(srv.URL+"/api/v1/income-templates",
		pctMustJSON(map[string]any{
			"name": pfx + "-tpl-bad",
			"lines": []map[string]any{
				{"pos_id": posIDs["a"], "amount": int64(0), "percent_bps": int64(6000)},
				{"pos_id": posIDs["b"], "amount": int64(0), "percent_bps": int64(5000)},
			},
		}), 400, "validation_failed") // Σ 110% > 100%
	pctPostExpect(srv.URL+"/api/v1/income-templates",
		pctMustJSON(map[string]any{
			"name": pfx + "-tpl-bad2",
			"lines": []map[string]any{
				{"pos_id": posIDs["a"], "amount": int64(0)},
			},
		}), 400, "validation_failed") // neither fixed nor percent
	pctPostExpect(srv.URL+"/api/v1/income-templates/"+tmplP["id"].(string)+"/preview",
		pctMustJSON(map[string]any{"amount": int64(0)}), 400, "validation_failed")
	pctPass("T6 below-400 / total-percent-400 / no-share-400 / preview-zero-400")

	// ── T7 PATCH edit → 200 + re-apply reflects change ───────────
	var patched map[string]any
	pctPatchJSON(srv.URL+"/api/v1/income-templates/"+tmplPID,
		pctMustJSON(map[string]any{
			"name": pfx + "-tpl-partial-renamed",
			"lines": []map[string]any{
				{"pos_id": posIDs["a"], "amount": int64(0), "percent_bps": int64(3000), "sort_order": int32(1)},
				{"pos_id": posIDs["b"], "amount": int64(1000000), "percent_bps": int64(2000), "sort_order": int32(2)},
			},
		}), 200, &patched)
	if patched["name"].(string) != pfx+"-tpl-partial-renamed" {
		pctDie("T7 PATCH name = %v", patched["name"])
	}
	if len(patched["lines"].([]any)) != 2 {
		pctDie("T7 PATCH lines = %d (want 2)", len(patched["lines"].([]any)))
	}
	var dbName string
	var dbLines int
	if err := pool.QueryRow(ctx, `SELECT name FROM income_template WHERE id = $1`,
		tmplPID).Scan(&dbName); err != nil || dbName != pfx+"-tpl-partial-renamed" {
		pctDie("T7 DB name = %q err=%v", dbName, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM income_template_line WHERE template_id = $1`,
		tmplPID).Scan(&dbLines); err != nil || dbLines != 2 {
		pctDie("T7 DB lines = %d err=%v (want 2)", dbLines, err)
	}
	// 30% of 10M = 3M; fixed 1M + 20% (2M); leftover = 4M.
	var applyPatched map[string]any
	pctPostJSON(srv.URL+"/api/v1/income-templates/"+tmplPID+"/apply", pctMustJSON(map[string]any{
		"amount": int64(10000000), "effective_date": today,
		"counterparty_id": cpID, "idempotency_key": pfx + "-apply-patched",
	}), 201, &applyPatched)
	ppAllocs := applyPatched["allocations"].([]any)
	if pctAllocByPos(ppAllocs, posIDs["a"]) != 3000000 ||
		pctAllocByPos(ppAllocs, posIDs["b"]) != 3000000 ||
		pctAllocByPos(ppAllocs, posIDs["left"]) != 4000000 {
		pctDie("T7 patched split wrong: %v", ppAllocs)
	}
	pctPatchExpect(srv.URL+"/api/v1/income-templates/"+tmplPID,
		pctMustJSON(map[string]any{
			"lines": []map[string]any{
				{"pos_id": posIDs["a"], "amount": int64(0), "percent_bps": int64(9000)},
				{"pos_id": posIDs["b"], "amount": int64(0), "percent_bps": int64(2000)},
			},
		}), 400, "validation_failed")
	pctPatchExpect(srv.URL+"/api/v1/income-templates/00000000-0000-0000-0000-000000000000",
		pctMustJSON(map[string]any{"name": "x"}), 404, "not_found")
	pctPass("T7 PATCH edit → 200 + DB verified + re-apply 3M/3M/4M; bad-percent → 400, unknown → 404")

	fmt.Println()
	fmt.Println("PASS — percent income-templates: create/preview/apply/mix/PATCH + Σ == amount + idempotent")
}

// ── helpers ─────────────────────────────────────────────────────

func pctAllocByPos(allocs []any, posID string) int64 {
	for _, x := range allocs {
		m := x.(map[string]any)
		pos, _ := m["PosID"].(string)
		if pos == "" {
			pos, _ = m["pos_id"].(string)
		}
		if pos == posID {
			if f, ok := m["Amount"].(float64); ok {
				return int64(f)
			}
			if f, ok := m["amount"].(float64); ok {
				return int64(f)
			}
		}
	}
	return -1
}

func pctCleanup(pool *pgxpool.Pool, pfx string) {
	bg := context.Background()
	pool.Exec(bg, `DELETE FROM transactions WHERE idempotency_key LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM income_template_line WHERE template_id IN (SELECT id FROM income_template WHERE name LIKE $1)`, pfx+"%")
	pool.Exec(bg, `DELETE FROM income_template WHERE name LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM pos WHERE name LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM accounts WHERE name LIKE $1`, pfx+"%")
	pool.Exec(bg, `DELETE FROM counterparties WHERE name LIKE $1`, pfx+"%")
}

func pctDoReq(method, url string, body []byte) (int, []byte) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		pctDie("%s %s: new request: %v", method, url, err)
	}
	req.Header.Set("x-api-key", pctAPIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		pctDie("%s %s: %v", method, url, err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, b
}

func pctPostJSON(url string, body []byte, wantStatus int, dst interface{}) {
	status, resp := pctDoReq("POST", url, body)
	if status != wantStatus {
		pctDie("POST %s: status=%d want=%d body=%s", url, status, wantStatus, string(resp))
	}
	if dst != nil {
		if err := json.Unmarshal(resp, dst); err != nil {
			pctDie("decode %s: %v body=%s", url, err, string(resp))
		}
	}
}

func pctPostExpect(url string, body []byte, wantStatus int, wantErrCode string) {
	status, resp := pctDoReq("POST", url, body)
	if status != wantStatus {
		pctDie("POST %s: status=%d want=%d body=%s", url, status, wantStatus, string(resp))
	}
	if wantErrCode == "" {
		return
	}
	var apiErr struct {
		Error string `json:"error"`
	}
	json.Unmarshal(resp, &apiErr)
	if apiErr.Error != wantErrCode {
		pctDie("POST %s: error=%q want %q body=%s", url, apiErr.Error, wantErrCode, string(resp))
	}
}

func pctPatchJSON(url string, body []byte, wantStatus int, dst interface{}) {
	status, resp := pctDoReq("PATCH", url, body)
	if status != wantStatus {
		pctDie("PATCH %s: status=%d want=%d body=%s", url, status, wantStatus, string(resp))
	}
	if dst != nil {
		if err := json.Unmarshal(resp, dst); err != nil {
			pctDie("decode %s: %v body=%s", url, err, string(resp))
		}
	}
}

func pctPatchExpect(url string, body []byte, wantStatus int, wantErrCode string) {
	status, resp := pctDoReq("PATCH", url, body)
	if status != wantStatus {
		pctDie("PATCH %s: status=%d want=%d body=%s", url, status, wantStatus, string(resp))
	}
	var apiErr struct {
		Error string `json:"error"`
	}
	json.Unmarshal(resp, &apiErr)
	if apiErr.Error != wantErrCode {
		pctDie("PATCH %s: error=%q want %q body=%s", url, apiErr.Error, wantErrCode, string(resp))
	}
}

func pctMustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		pctDie("marshal: %v", err)
	}
	return b
}

func pctPass(msg string) { fmt.Printf("✓ %s\n", msg) }
func pctDie(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", args...)
	if !strings.HasPrefix(format, "✓") {
		os.Exit(1)
	}
}
