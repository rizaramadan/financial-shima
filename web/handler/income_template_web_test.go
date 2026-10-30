package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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

// The allocation form (preview → apply) used to be hard-capped at 10
// rows. A real template holds far more lines (the "Bulanan" template on
// the live deploy has 48), so the preview silently dropped every line
// past the 10th and apply could never sum to the salary. These tests
// pin the round-trip: one editable row per template line, and apply
// accepting every posted row.

// allocFixture is one account, n Pos on it, a template with n fixed
// lines of 100,000 each, and two users with DB-backed UUID ids (the
// ledger's notification insert requires real UUID recipients — the
// hardcoded "riza"/"shima" seed ids are resolved against the users
// table in production).
type allocFixture struct {
	tmplID    string
	posNames  []string
	posIDs    []string
	users     []user.User
	keyPrefix string
}

func seedAllocFixture(t *testing.T, pool *pgxpool.Pool, n int) *allocFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stamp := uuid.NewString()[:8]
	f := &allocFixture{
		keyPrefix: "AllocFix-" + stamp + "-",
	}
	accName := "AllocFix Acct " + stamp

	var accID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (name) VALUES ($1) RETURNING id`, accName,
	).Scan(&accID); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	for _, suffix := range []string{"a", "b"} {
		var uid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (display_name, telegram_identifier) VALUES ($1, $2) RETURNING id`,
			"AllocFix User "+suffix+" "+stamp, "@allocfix_"+suffix+"_"+stamp,
		).Scan(&uid); err != nil {
			t.Fatalf("insert user %s: %v", suffix, err)
		}
		f.users = append(f.users, user.User{ID: uid, DisplayName: "AllocFix User " + suffix})
	}
	f.posNames = make([]string, n)
	f.posIDs = make([]string, n)
	for i := 0; i < n; i++ {
		f.posNames[i] = fmt.Sprintf("AllocFix Pos %s-%02d", stamp, i)
		if err := pool.QueryRow(ctx,
			`INSERT INTO pos (name, currency, account_id) VALUES ($1, 'idr', $2) RETURNING id`,
			f.posNames[i], accID,
		).Scan(&f.posIDs[i]); err != nil {
			t.Fatalf("insert pos %d: %v", i, err)
		}
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO income_template (name) VALUES ($1) RETURNING id`, "AllocFix Tmpl "+stamp,
	).Scan(&f.tmplID); err != nil {
		t.Fatalf("insert template: %v", err)
	}
	for i, pid := range f.posIDs {
		if _, err := pool.Exec(ctx,
			`INSERT INTO income_template_line (template_id, pos_id, amount, sort_order) VALUES ($1, $2, 100000, $3)`,
			f.tmplID, pid, i,
		); err != nil {
			t.Fatalf("insert line %d: %v", i, err)
		}
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx,
			`DELETE FROM notifications WHERE related_transaction_id IN
			 (SELECT id FROM transactions WHERE idempotency_key LIKE $1)`, f.keyPrefix+"%")
		_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE idempotency_key LIKE $1`, f.keyPrefix+"%")
		_, _ = pool.Exec(ctx, `DELETE FROM income_template WHERE id = $1`, f.tmplID)
		_, _ = pool.Exec(ctx, `DELETE FROM pos WHERE name LIKE $1`, "AllocFix Pos "+stamp+"-%")
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE name = $1`, accName)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE display_name LIKE $1`, "AllocFix User % "+stamp)
	})
	return f
}

func newAllocTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newIncomeTemplateTestEcho(t *testing.T, users []user.User, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	a := auth.New(users, clock.System{},
		strings.NewReader("                "),
		idgen.Fixed{Value: "x"})
	h := handler.New(a, &assistant.Recorder{}, pool)
	e := echo.New()
	e.Renderer = template.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, user.User{ID: "test", DisplayName: "Tester"})
			return next(c)
		}
	})
	e.POST("/income-templates/:id/preview", h.IncomeTemplatePreviewPost)
	e.POST("/income-templates/:id/apply", h.IncomeTemplateApplyPost)
	return e
}

// TestIntegration_Preview_RendersOneRowPerTemplateLine: a template with
// 12 lines must render 12 pre-filled allocation rows (each Pos
// selected exactly once) plus at least one empty spare row for adding
// lines. The old 10-row cap dropped lines 11+ entirely.
func TestIntegration_Preview_RendersOneRowPerTemplateLine(t *testing.T) {
	pool := newAllocTestPool(t)
	f := seedAllocFixture(t, pool, 12)
	e := newIncomeTemplateTestEcho(t, f.users, pool)

	form := url.Values{}
	form.Set("amount", "1200000")
	form.Set("effective_date", "2026-10-23")
	form.Set("counterparty_name", "AllocFix Payer")
	req := httptest.NewRequest(http.MethodPost,
		"/income-templates/"+f.tmplID+"/preview", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// One row per line: the 12th row (index 11) must exist at all.
	if !strings.Contains(body, `name="alloc_pos_11"`) {
		t.Error(`body missing alloc_pos_11 — template lines past the 10th are dropped`)
	}
	// Every fixture Pos must be pre-selected in exactly one row. A
	// selected option renders as ' selected>Name (idr, …'; unselected
	// options carry no 'selected' marker, so this count is exact.
	for _, name := range f.posNames {
		if got := strings.Count(body, " selected>"+name+" (idr"); got != 1 {
			t.Errorf("pos %q selected in %d rows, want 1", name, got)
		}
	}
	// Spare empty rows for adding lines: more rows than lines.
	if rows := strings.Count(body, `name="alloc_pos_`); rows <= 12 {
		t.Errorf("rendered %d alloc rows, want > 12 (no spare row to add a line)", rows)
	}
}

// TestIntegration_Apply_ReadsEveryPostedRow: apply must consume every
// allocation row the preview posts — 12 rows here — not just the first
// 10. The sum guard turns the old cap into a hard reject, so the
// ledger stays empty (the bug's safe failure) instead of holding 12
// money_in rows (the fixed behavior).
func TestIntegration_Apply_ReadsEveryPostedRow(t *testing.T) {
	pool := newAllocTestPool(t)
	f := seedAllocFixture(t, pool, 12)
	e := newIncomeTemplateTestEcho(t, f.users, pool)

	form := url.Values{}
	form.Set("amount", "1200000")
	form.Set("effective_date", "2026-10-23")
	form.Set("counterparty_name", "AllocFix Payer")
	form.Set("idempotency_key", f.keyPrefix+"apply")
	for i, pid := range f.posIDs {
		form.Set(fmt.Sprintf("alloc_pos_%d", i), pid)
		form.Set(fmt.Sprintf("alloc_amount_%d", i), "100000")
	}
	req := httptest.NewRequest(http.MethodPost,
		"/income-templates/"+f.tmplID+"/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body:\n%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "flash=Approved") {
		t.Errorf("Location = %q, want success flash", loc)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var count, total int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(pos_amount), 0) FROM transactions
		 WHERE idempotency_key LIKE $1`, f.keyPrefix+"%",
	).Scan(&count, &total); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if count != 12 {
		t.Errorf("created %d transactions, want 12", count)
	}
	if total != 1200000 {
		t.Errorf("transactions sum = %d, want 1200000", total)
	}
}
