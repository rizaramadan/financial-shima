//go:build ignore

// e2e_income_template walks every income-template scenario end-to-end
// against the real handler tree + real Postgres:
//
//   - Create template (name + leftover Pos + 3 lines).
//
//   - Reject apply with amount < Σ(lines).
//
//   - Apply with amount == Σ(lines): expand to 3 money_in rows.
//
//   - Apply with amount > Σ(lines): expand to 3 + leftover row.
//
//   - Idempotent re-apply: same idempotency_key returns the same txn ids.
//
//   - Strict template (no leftover): amount > Σ rejected.
//
//     export DATABASE_URL=postgres://…
//     export LLM_API_KEY=test-api-key-for-e2e
//     go run ./scripts/e2e_income_template.go
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
	api.POST("/accounts", h.APIAccountsCreate)
	api.POST("/pos", h.APIPosCreate)
	api.POST("/counterparties", h.APICounterpartiesCreate)
	api.GET("/income-templates", h.APIIncomeTemplatesList)
	api.POST("/income-templates", h.APIIncomeTemplatesCreate)
	api.POST("/income-templates/:id/apply", h.APIIncomeTemplateApply)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	cleanup := func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM transactions WHERE idempotency_key LIKE $1`,
			fmt.Sprintf("e2e-tpl-%d-%%", stamp))
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM income_template_line WHERE template_id IN (SELECT id FROM income_template WHERE name LIKE $1)`,
			fmt.Sprintf("e2e-tpl-%d-%%", stamp))
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM income_template WHERE name LIKE $1`,
			fmt.Sprintf("e2e-tpl-%d-%%", stamp))
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM pos WHERE name LIKE $1`,
			fmt.Sprintf("e2e-tpl-pos-%d-%%", stamp))
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM accounts WHERE name LIKE $1`,
			fmt.Sprintf("e2e-tpl-acc-%d-%%", stamp))
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM counterparties WHERE name LIKE $1`,
			fmt.Sprintf("e2e-tpl-cp-%d-%%", stamp))
	}
	defer cleanup()

	// ── Setup: create accounts, 4 pos (3 line + 1 leftover), counterparty ─
	var account map[string]any
	postJSON(srv.URL+"/api/v1/accounts",
		mustJSON(map[string]string{"name": fmt.Sprintf("e2e-tpl-acc-%d-1", stamp)}),
		http.StatusCreated, &account)
	accID := account["id"].(string)

	posIDs := map[string]string{}
	for _, name := range []string{"mortgage", "groceries", "liburan", "leftover"} {
		var p map[string]any
		postJSON(srv.URL+"/api/v1/pos", mustJSON(map[string]any{
			"name":       fmt.Sprintf("e2e-tpl-pos-%d-%s", stamp, name),
			"currency":   "idr",
			"account_id": accID,
		}), http.StatusCreated, &p)
		posIDs[name] = p["id"].(string)
	}
	var cp map[string]any
	postJSON(srv.URL+"/api/v1/counterparties",
		mustJSON(map[string]string{"name": fmt.Sprintf("e2e-tpl-cp-%d", stamp)}),
		http.StatusCreated, &cp)
	cpID := cp["id"].(string)
	pass("setup: 1 account, 4 pos, 1 counterparty")

	// ── S1 Create template with 3 lines + leftover Pos ──────────────
	var tmpl map[string]any
	tplBody := mustJSON(map[string]any{
		"name":            fmt.Sprintf("e2e-tpl-%d-with-leftover", stamp),
		"leftover_pos_id": posIDs["leftover"],
		"lines": []map[string]any{
			{"pos_id": posIDs["mortgage"], "amount": 12_000_000, "sort_order": 1},
			{"pos_id": posIDs["groceries"], "amount": 5_000_000, "sort_order": 2},
			{"pos_id": posIDs["liburan"], "amount": 3_000_000, "sort_order": 3},
		},
	})
	postJSON(srv.URL+"/api/v1/income-templates", tplBody, http.StatusCreated, &tmpl)
	tmplID := tmpl["id"].(string)
	if len(tmpl["lines"].([]any)) != 3 {
		die("template lines: got %d, want 3", len(tmpl["lines"].([]any)))
	}
	pass("S1 created template " + tmplID + " with 3 lines + leftover Pos")

	// ── S2 DB verify: template + lines inserted ──────────────────────
	var dbLineCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM income_template_line WHERE template_id = $1`, tmplID,
	).Scan(&dbLineCount); err != nil {
		die("S2 DB count lines: %v", err)
	}
	if dbLineCount != 3 {
		die("S2 DB line count = %d (want 3)", dbLineCount)
	}
	pass("S2 DB verified: 3 lines persisted")

	// ── S3 Apply with amount < Σ(lines)=20M → 400 amount_below ──────
	postJSONExpectStatus(
		srv.URL+"/api/v1/income-templates/"+tmplID+"/apply",
		applyBody(stamp, "below", 10_000_000, accID, cpID),
		400, "validation_failed")
	pass("S3 apply amount < Σ(lines) → 400 validation_failed")

	// ── S4 Apply with amount == Σ(lines)=20M → 201 + 3 txns ─────────
	var apply1 map[string]any
	postJSON(
		srv.URL+"/api/v1/income-templates/"+tmplID+"/apply",
		applyBody(stamp, "exact", 20_000_000, accID, cpID),
		http.StatusCreated, &apply1)
	if ids, _ := apply1["transaction_ids"].([]any); len(ids) != 3 {
		die("S4 transaction_ids: got %d, want 3", len(ids))
	}
	pass("S4 apply amount == Σ(lines) → 201 + 3 transactions")

	// ── S5 DB verify: 3 money_in rows for this idempotency root, summing to 20M ─
	idempRoot := fmt.Sprintf("e2e-tpl-%d-exact", stamp)
	var sumExact int64
	var countExact int
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(account_amount), 0)::bigint, count(*)
		FROM transactions
		WHERE idempotency_key LIKE $1`, idempRoot+":%",
	).Scan(&sumExact, &countExact); err != nil {
		die("S5 DB sum/count: %v", err)
	}
	if countExact != 3 {
		die("S5 DB count = %d (want 3)", countExact)
	}
	if sumExact != 20_000_000 {
		die("S5 DB sum = %d (want 20000000)", sumExact)
	}
	pass("S5 DB verified: 3 transactions sum to Rp 20.000.000 exactly")

	// ── S6 Apply with amount > Σ(lines)=25M → 201 + 4 txns (3 lines + leftover) ─
	var apply2 map[string]any
	postJSON(
		srv.URL+"/api/v1/income-templates/"+tmplID+"/apply",
		applyBody(stamp, "withleftover", 25_000_000, accID, cpID),
		http.StatusCreated, &apply2)
	if ids, _ := apply2["transaction_ids"].([]any); len(ids) != 4 {
		die("S6 transaction_ids: got %d, want 4 (3 lines + leftover)", len(ids))
	}
	pass("S6 apply amount > Σ(lines) → 201 + 4 transactions (3 + leftover)")

	// ── S7 DB verify: 4 rows; leftover row credits the leftover Pos with 5M ─
	idempLeftover := fmt.Sprintf("e2e-tpl-%d-withleftover", stamp)
	var sumLeftover int64
	var countLeftover int
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(account_amount), 0)::bigint, count(*)
		FROM transactions
		WHERE idempotency_key LIKE $1`, idempLeftover+":%",
	).Scan(&sumLeftover, &countLeftover); err != nil {
		die("S7 DB sum/count: %v", err)
	}
	if countLeftover != 4 {
		die("S7 DB count = %d (want 4)", countLeftover)
	}
	if sumLeftover != 25_000_000 {
		die("S7 DB sum = %d (want 25000000)", sumLeftover)
	}
	// And specifically the leftover row hits the leftover Pos:
	var leftoverRowAmt int64
	if err := pool.QueryRow(ctx,
		`SELECT pos_amount FROM transactions WHERE idempotency_key = $1`,
		idempLeftover+":leftover",
	).Scan(&leftoverRowAmt); err != nil {
		die("S7 DB leftover row: %v", err)
	}
	if leftoverRowAmt != 5_000_000 {
		die("S7 leftover row amount = %d (want 5000000)", leftoverRowAmt)
	}
	pass("S7 DB verified: 4 rows sum to 25M; leftover row carries 5M")

	// ── S8 Idempotent re-apply: same idempotency_key → same txn ids ─
	var apply2b map[string]any
	postJSON(
		srv.URL+"/api/v1/income-templates/"+tmplID+"/apply",
		applyBody(stamp, "withleftover", 25_000_000, accID, cpID),
		http.StatusCreated, &apply2b)
	idsA, _ := apply2["transaction_ids"].([]any)
	idsB, _ := apply2b["transaction_ids"].([]any)
	if len(idsA) != len(idsB) {
		die("S8 id count differs: %d vs %d", len(idsA), len(idsB))
	}
	for i := range idsA {
		if idsA[i] != idsB[i] {
			die("S8 id[%d] differs: %v vs %v", i, idsA[i], idsB[i])
		}
	}
	// And the DB still has only 4 rows (no duplicates).
	var afterCount int
	pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE idempotency_key LIKE $1`,
		idempLeftover+":%").Scan(&afterCount)
	if afterCount != 4 {
		die("S8 dup re-apply added rows: now %d (want 4)", afterCount)
	}
	pass("S8 idempotent re-apply → identical ids, DB row count unchanged at 4")

	// ── S9 Strict template (no leftover): apply > sum → 400 ─────────
	var strictTmpl map[string]any
	postJSON(srv.URL+"/api/v1/income-templates", mustJSON(map[string]any{
		"name": fmt.Sprintf("e2e-tpl-%d-strict", stamp),
		"lines": []map[string]any{
			{"pos_id": posIDs["mortgage"], "amount": 12_000_000, "sort_order": 1},
		},
	}), http.StatusCreated, &strictTmpl)
	strictID := strictTmpl["id"].(string)
	postJSONExpectStatus(
		srv.URL+"/api/v1/income-templates/"+strictID+"/apply",
		applyBody(stamp, "strict-over", 15_000_000, accID, cpID),
		400, "validation_failed")
	// Strict apply at exact amount succeeds:
	var strictExact map[string]any
	postJSON(
		srv.URL+"/api/v1/income-templates/"+strictID+"/apply",
		applyBody(stamp, "strict-exact", 12_000_000, accID, cpID),
		http.StatusCreated, &strictExact)
	if ids, _ := strictExact["transaction_ids"].([]any); len(ids) != 1 {
		die("S9 strict-exact transactions: got %d, want 1", len(ids))
	}
	pass("S9 strict template: amount > sum → 400; amount == sum → 201 + 1 txn")

	// ── S10 GET /income-templates list returns both templates ───────
	r, _ := http.NewRequest("GET", srv.URL+"/api/v1/income-templates", nil)
	r.Header.Set("x-api-key", apiKey)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		die("S10 GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var list []map[string]any
	json.Unmarshal(body, &list)
	found := 0
	for _, t := range list {
		name := t["name"].(string)
		if strings.HasPrefix(name, fmt.Sprintf("e2e-tpl-%d-", stamp)) {
			found++
		}
	}
	if found < 2 {
		die("S10 list: found %d e2e templates (want >=2)", found)
	}
	pass("S10 GET /income-templates lists both created templates")

	fmt.Println()
	fmt.Println("PASS — income-template /api/v1 surface works end-to-end")
}

// applyBody builds the apply request. account_id is no longer accepted
// — accounts are reached via pos.account_id (spec §4.2/§5.6). The
// template's lines and pos already determine which account is funded.
func applyBody(stamp int64, suffix string, amount int64, _, cpID string) []byte {
	return mustJSON(map[string]any{
		"amount":          amount,
		"effective_date":  time.Now().Format("2006-01-02"),
		"counterparty_id": cpID,
		"note":            "e2e " + suffix,
		"idempotency_key": fmt.Sprintf("e2e-tpl-%d-%s", stamp, suffix),
	})
}

func postJSON(url string, body []byte, wantStatus int, dst interface{}) {
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		die("do %s: %v", url, err)
	}
	defer r.Body.Close()
	respBody, _ := io.ReadAll(r.Body)
	if r.StatusCode != wantStatus {
		die("POST %s: status=%d want=%d body=%s", url, r.StatusCode, wantStatus, string(respBody))
	}
	if dst != nil {
		if err := json.Unmarshal(respBody, dst); err != nil {
			die("decode %s: %v body=%s", url, err, string(respBody))
		}
	}
}

func postJSONExpectStatus(url string, body []byte, wantStatus int, wantErrCode string) {
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	r, _ := http.DefaultClient.Do(req)
	defer r.Body.Close()
	respBody, _ := io.ReadAll(r.Body)
	if r.StatusCode != wantStatus {
		die("POST %s: status=%d want=%d body=%s", url, r.StatusCode, wantStatus, string(respBody))
	}
	var apiErr struct {
		Error string `json:"error"`
	}
	json.Unmarshal(respBody, &apiErr)
	if apiErr.Error != wantErrCode {
		die("error: got %q want %q", apiErr.Error, wantErrCode)
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
