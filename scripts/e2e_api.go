//go:build ignore

// e2e_api walks every /api/v1 endpoint end-to-end against the real
// handler tree + real Postgres. Hits HTTP exactly the way the LLM
// agent / curl operator would, then queries the DB to verify each
// state change actually landed.
//
//	export DATABASE_URL=postgres://postgres@localhost:5432/financial_shima?sslmode=disable
//	export LLM_API_KEY=test-api-key-for-e2e
//	go run ./scripts/e2e_api.go
//
// Prints a PASS/FAIL line per step. Exits non-zero on first failure.
// Cleans up its own rows on success.
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

	// Resolve seeded users to their DB UUIDs so ledger.Service can write
	// notification rows against real users.id values (cmd/server does the
	// same dance via resolveUserIDs at boot).
	users := user.Seeded()
	for i, u := range users {
		var idStr string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM users WHERE telegram_identifier = $1`,
			u.TelegramIdentifier,
		).Scan(&idStr); err == nil {
			users[i].ID = idStr
		} else {
			die("resolve user %q: %v", u.TelegramIdentifier, err)
		}
	}
	a := auth.New(users, clock.System{}, rand.Reader, idgen.Crypto{})
	h := handler.New(a, &assistant.Recorder{}, pool)

	e := echo.New()
	e.Renderer = template.New()
	api := e.Group("/api/v1", mw.APIKey(apiKey))
	api.GET("/accounts", h.APIAccountsList)
	api.POST("/accounts", h.APIAccountsCreate)
	api.POST("/pos", h.APIPosCreate)
	api.POST("/counterparties", h.APICounterpartiesCreate)
	api.POST("/transactions", h.APITransactionsCreate)

	srv := httptest.NewServer(e)
	defer srv.Close()
	pass("server up at " + srv.URL)

	stamp := time.Now().UnixNano()
	accountName := fmt.Sprintf("e2e-acc-%d", stamp)
	posName := fmt.Sprintf("e2e-pos-%d", stamp)
	cpName := fmt.Sprintf("e2e-cp-%d", stamp)

	defer func() {
		// Cleanup so re-runs stay clean. We don't fail the test on
		// cleanup errors — the assertions already passed.
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM transactions WHERE idempotency_key LIKE $1`,
			fmt.Sprintf("e2e-idem-%d-%%", stamp))
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM pos WHERE name = $1`, posName)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM accounts WHERE name = $1`, accountName)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM counterparties WHERE name = $1`, cpName)
	}()

	// 1. Auth gate: no x-api-key → 401.
	r1, _ := http.Get(srv.URL + "/api/v1/accounts")
	defer r1.Body.Close()
	if r1.StatusCode != 401 {
		die("S1 no x-api-key: status=%d (want 401)", r1.StatusCode)
	}
	pass("S1 GET /api/v1/accounts without x-api-key → 401")

	// 2. Auth gate: wrong x-api-key → 401.
	req2, _ := http.NewRequest("GET", srv.URL+"/api/v1/accounts", nil)
	req2.Header.Set("x-api-key", "wrong")
	r2, _ := http.DefaultClient.Do(req2)
	r2.Body.Close()
	if r2.StatusCode != 401 {
		die("S2 wrong x-api-key: status=%d (want 401)", r2.StatusCode)
	}
	pass("S2 GET /api/v1/accounts with wrong x-api-key → 401")

	// 3. POST /api/v1/accounts (valid).
	body := mustJSON(map[string]string{"name": accountName})
	var account map[string]interface{}
	postJSON(srv.URL+"/api/v1/accounts", body, http.StatusCreated, &account)
	if account["name"].(string) != accountName {
		die("S3 created account name mismatch: %v", account["name"])
	}
	accountID := account["id"].(string)
	pass("S3 POST /api/v1/accounts → 201 + " + accountID)

	// 4. DB verify: account row physically exists.
	var dbAccountName string
	if err := pool.QueryRow(ctx,
		`SELECT name FROM accounts WHERE id = $1`, accountID,
	).Scan(&dbAccountName); err != nil {
		die("S4 DB lookup account: %v", err)
	}
	if dbAccountName != accountName {
		die("S4 DB name mismatch: %q != %q", dbAccountName, accountName)
	}
	pass("S4 DB account row verified")

	// 5. POST /api/v1/accounts (empty name) → 400.
	postJSONExpectStatus(srv.URL+"/api/v1/accounts",
		mustJSON(map[string]string{"name": "  "}), 400, "validation_failed")
	pass("S5 POST /api/v1/accounts empty name → 400 validation_failed")

	// 6. POST /api/v1/pos (valid, with target). Per spec §4.2, account_id
	// is required — every Pos lives in exactly one Account.
	posBody := mustJSON(map[string]interface{}{
		"name": posName, "currency": "idr", "target": 12000000,
		"account_id": accountID,
	})
	var pos map[string]interface{}
	postJSON(srv.URL+"/api/v1/pos", posBody, http.StatusCreated, &pos)
	if pos["currency"].(string) != "idr" {
		die("S6 created pos currency mismatch: %v", pos["currency"])
	}
	posID := pos["id"].(string)
	pass("S6 POST /api/v1/pos → 201 + " + posID)

	// 7. DB verify: pos has target=12M, archived=false.
	var dbTarget *int64
	var dbArchived bool
	if err := pool.QueryRow(ctx,
		`SELECT target, archived FROM pos WHERE id = $1`, posID,
	).Scan(&dbTarget, &dbArchived); err != nil {
		die("S7 DB lookup pos: %v", err)
	}
	if dbTarget == nil || *dbTarget != 12000000 {
		die("S7 DB target mismatch: %v", dbTarget)
	}
	if dbArchived {
		die("S7 DB archived=true on fresh pos")
	}
	pass("S7 DB pos row verified (target=12000000, archived=false)")

	// 8. POST /api/v1/pos (duplicate name+currency) → 409 Conflict.
	postJSONExpectStatus(srv.URL+"/api/v1/pos", posBody, 409, "conflict")
	pass("S8 POST /api/v1/pos duplicate → 409 conflict (UNIQUE held)")

	// 9. POST /api/v1/pos (uppercase currency) → 400 (Normalize rejects).
	postJSONExpectStatus(srv.URL+"/api/v1/pos",
		mustJSON(map[string]interface{}{
			"name": "x" + posName, "currency": "BAD CURRENCY",
			"account_id": accountID,
		}), 400, "validation_failed")
	pass("S9 POST /api/v1/pos invalid currency → 400 validation_failed")

	// 10. POST /api/v1/counterparties (valid, mixed case).
	cpBody := mustJSON(map[string]string{"name": cpName})
	var cp map[string]interface{}
	postJSON(srv.URL+"/api/v1/counterparties", cpBody, http.StatusCreated, &cp)
	cpID := cp["id"].(string)
	pass("S10 POST /api/v1/counterparties → 201 + " + cpID)

	// 11. POST /api/v1/counterparties (same name, uppercased) → idempotent
	//     (case-insensitive UNIQUE on name_lower returns existing row).
	upperBody := mustJSON(map[string]string{"name": strings.ToUpper(cpName)})
	var cp2 map[string]interface{}
	postJSON(srv.URL+"/api/v1/counterparties", upperBody, http.StatusCreated, &cp2)
	if cp2["id"].(string) != cpID {
		die("S11 case-folded counterparty: id changed %q → %q", cpID, cp2["id"])
	}
	if cp2["name"].(string) != cpName {
		die("S11 case-folded counterparty: name = %q, want preserved %q", cp2["name"], cpName)
	}
	pass("S11 POST /api/v1/counterparties same-name uppercase → same id, original casing")

	// 12. POST /api/v1/transactions (money_in, IDR, valid). Account is
	// implicit via pos.account_id (spec §4.2/§5.6); the request body
	// no longer carries account_id.
	idemKey := fmt.Sprintf("e2e-idem-%d-001", stamp)
	txBody := mustJSON(map[string]interface{}{
		"type":            "money_in",
		"effective_date":  time.Now().Format("2006-01-02"),
		"account_amount":  1500000,
		"pos_id":          posID,
		"pos_amount":      1500000,
		"counterparty_id": cpID,
		"note":            "e2e test salary",
		"idempotency_key": idemKey,
	})
	var txn map[string]interface{}
	postJSON(srv.URL+"/api/v1/transactions", txBody, http.StatusCreated, &txn)
	txnID := txn["id"].(string)
	if txn["was_inserted"].(bool) != true {
		die("S12 was_inserted=false on first POST (want true)")
	}
	pass("S12 POST /api/v1/transactions → 201 + was_inserted=true + " + txnID)

	// 13. DB verify: transaction row physically exists with all fields.
	var (
		dbType, dbIdem         string
		dbAccountAmt, dbPosAmt int64
	)
	if err := pool.QueryRow(ctx, `
		SELECT type::text, account_amount, pos_amount, idempotency_key
		FROM transactions WHERE id = $1`, txnID,
	).Scan(&dbType, &dbAccountAmt, &dbPosAmt, &dbIdem); err != nil {
		die("S13 DB lookup transaction: %v", err)
	}
	if dbType != "money_in" || dbAccountAmt != 1500000 || dbPosAmt != 1500000 || dbIdem != idemKey {
		die("S13 DB row mismatch: type=%q acct=%d pos=%d idem=%q",
			dbType, dbAccountAmt, dbPosAmt, dbIdem)
	}
	pass("S13 DB transaction row verified (type=money_in, amounts=1500000, idem matches)")

	// 14. DB verify: notifications fired (spec §4.5: source=api notifies BOTH).
	var notifCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE related_transaction_id = $1`, txnID,
	).Scan(&notifCount); err != nil {
		die("S14 DB count notifications: %v", err)
	}
	if notifCount != 2 {
		die("S14 source=api should notify both users; got %d notifications (want 2)", notifCount)
	}
	pass("S14 DB notifications verified: 2 rows (both users notified per §4.5)")

	// 15. Idempotent re-submit: same idempotency_key → same id, was_inserted=false,
	//     and NO new notifications fire.
	var txn2 map[string]interface{}
	postJSON(srv.URL+"/api/v1/transactions", txBody, http.StatusCreated, &txn2)
	if txn2["id"].(string) != txnID {
		die("S15 idempotent: id changed %q → %q", txnID, txn2["id"])
	}
	if txn2["was_inserted"].(bool) != false {
		die("S15 idempotent: was_inserted=true on dup (want false)")
	}
	var notifCount2 int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE related_transaction_id = $1`, txnID,
	).Scan(&notifCount2)
	if notifCount2 != 2 {
		die("S15 idempotent re-submit added notifications: now %d (want 2)", notifCount2)
	}
	pass("S15 idempotent re-submit → was_inserted=false + notification count stays 2")

	// 16. Counterparty-by-name path: posting with counterparty_name (not _id)
	//     should resolve to the existing row, not create a new one.
	idemKey2 := fmt.Sprintf("e2e-idem-%d-002", stamp)
	txBody2 := mustJSON(map[string]interface{}{
		"type":              "money_out",
		"effective_date":    time.Now().Format("2006-01-02"),
		"account_amount":    250000,
		"pos_id":            posID,
		"pos_amount":        250000,
		"counterparty_name": strings.ToLower(cpName), // different casing on purpose
		"note":              "e2e money_out via cp_name",
		"idempotency_key":   idemKey2,
	})
	var txn3 map[string]interface{}
	postJSON(srv.URL+"/api/v1/transactions", txBody2, http.StatusCreated, &txn3)
	if txn3["counterparty_id"].(string) != cpID {
		die("S16 counterparty_name path created a new row (id %q, want existing %q)",
			txn3["counterparty_id"], cpID)
	}
	pass("S16 POST txn via counterparty_name → resolved to existing id")

	// 17. Validation failure: IDR pos with account_amount != pos_amount.
	postJSONExpectStatus(srv.URL+"/api/v1/transactions",
		mustJSON(map[string]interface{}{
			"type":            "money_in",
			"effective_date":  time.Now().Format("2006-01-02"),
			"account_amount":  100000,
			"pos_id":          posID,
			"pos_amount":      99999, // mismatch on IDR pos
			"counterparty_id": cpID,
			"idempotency_key": fmt.Sprintf("e2e-idem-%d-003", stamp),
		}), 400, "validation_failed")
	pass("S17 IDR pos amount mismatch → 400 validation_failed (spec §5.1)")

	// 18. Validation failure: future effective_date.
	postJSONExpectStatus(srv.URL+"/api/v1/transactions",
		mustJSON(map[string]interface{}{
			"type":            "money_in",
			"effective_date":  time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
			"account_amount":  1000000,
			"pos_id":          posID,
			"pos_amount":      1000000,
			"counterparty_id": cpID,
			"idempotency_key": fmt.Sprintf("e2e-idem-%d-004", stamp),
		}), 400, "validation_failed")
	pass("S18 future effective_date → 400 validation_failed (spec §5.1)")

	// 19. NOT FOUND: unknown pos_id (account is implicit now, so the
	// equivalent failure mode is an invalid pos).
	postJSONExpectStatus(srv.URL+"/api/v1/transactions",
		mustJSON(map[string]interface{}{
			"type":            "money_in",
			"effective_date":  time.Now().Format("2006-01-02"),
			"account_amount":  1000000,
			"pos_id":          "00000000-0000-0000-0000-000000000000",
			"pos_amount":      1000000,
			"counterparty_id": cpID,
			"idempotency_key": fmt.Sprintf("e2e-idem-%d-005", stamp),
		}), 404, "not_found")
	pass("S19 unknown pos_id → 404 not_found")

	// 20. GET /api/v1/accounts (with key) returns the seeded account
	//     in the list (proves create + list see consistent data).
	req20, _ := http.NewRequest("GET", srv.URL+"/api/v1/accounts", nil)
	req20.Header.Set("x-api-key", apiKey)
	r20, _ := http.DefaultClient.Do(req20)
	body20, _ := io.ReadAll(r20.Body)
	r20.Body.Close()
	if r20.StatusCode != 200 {
		die("S20 GET /api/v1/accounts: status=%d", r20.StatusCode)
	}
	if !strings.Contains(string(body20), accountName) {
		die("S20 GET /api/v1/accounts: response missing %q", accountName)
	}
	pass("S20 GET /api/v1/accounts contains seeded " + accountName)

	fmt.Println()
	fmt.Println("PASS — every /api/v1 endpoint works end-to-end (handler + DB consistency)")
}

// --- helpers ---

func postJSON(url string, body []byte, wantStatus int, dst interface{}) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		die("new request: %v", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		die("do: %v", err)
	}
	defer r.Body.Close()
	respBody, _ := io.ReadAll(r.Body)
	if r.StatusCode != wantStatus {
		die("POST %s: status=%d want=%d body=%s", url, r.StatusCode, wantStatus, string(respBody))
	}
	if dst != nil {
		if err := json.Unmarshal(respBody, dst); err != nil {
			die("decode body %s: %v (body=%s)", url, err, string(respBody))
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
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &apiErr); err != nil {
		die("decode error body: %v (body=%s)", err, string(respBody))
	}
	if apiErr.Error != wantErrCode {
		die("error code: got %q want %q", apiErr.Error, wantErrCode)
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
