package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// setupAccountDeleteEcho wires the minimum middleware/routes needed
// to exercise POST /accounts/:id/delete end-to-end against a real
// Postgres. Mirrors pos_account_integration_test.go's pattern: a
// dummy session injector stands in for real auth so the handler
// passes its CurrentUser check.
func setupAccountDeleteEcho(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	src := strings.NewReader(strings.Repeat("x", 64))
	a := auth.New(user.Seeded(), clock.System{}, src, idgen.Crypto{})
	h := New(a, &assistant.Recorder{}, pool)
	e := echo.New()
	signed := user.User{ID: "test-user", DisplayName: "Tester"}
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, signed)
			return next(c)
		}
	})
	e.POST("/accounts/:id/delete", h.AccountDeletePost)
	return e
}

// TestIntegration_AccountDelete_EmptySucceeds verifies the happy
// path: an account with zero Pos is hard-deleted, the redirect
// carries an acct_flash cookie, and the row is actually gone from
// Postgres.
func TestIntegration_AccountDelete_EmptySucceeds(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL unset; integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("connect %s: %v", dbURL, err)
	}
	defer pool.Close()
	q := dbq.New(pool)

	acc, err := q.CreateAccount(ctx, "test-delete-empty-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	accIDStr := uuid.UUID(acc.ID.Bytes).String()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM accounts WHERE id = $1`, acc.ID)
	})

	e := setupAccountDeleteEcho(t, pool)
	req := httptest.NewRequest(http.MethodPost, "/accounts/"+accIDStr+"/delete", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/accounts" {
		t.Errorf("redirect = %q, want /accounts", got)
	}
	if !hasCookie(rec, "acct_flash") {
		t.Errorf("missing acct_flash cookie; headers=%v", rec.Header())
	}

	if _, err := q.GetAccount(ctx, acc.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("account row still present after delete: err=%v", err)
	}
}

// TestIntegration_AccountDelete_WithPosFails verifies the guard:
// when a Pos references the account, the DELETE refuses, the row
// stays, and the user gets an acct_error flash.
func TestIntegration_AccountDelete_WithPosFails(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL unset; integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("connect %s: %v", dbURL, err)
	}
	defer pool.Close()
	q := dbq.New(pool)

	acc, err := q.CreateAccount(ctx, "test-delete-withpos-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	accIDStr := uuid.UUID(acc.ID.Bytes).String()
	pos, err := q.CreatePos(ctx, dbq.CreatePosParams{
		Name:      "test-delete-pos-" + uuid.NewString()[:8],
		Currency:  "idr",
		AccountID: acc.ID,
	})
	if err != nil {
		t.Fatalf("CreatePos: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM pos WHERE id = $1`, pos.ID)
		_, _ = pool.Exec(bg, `DELETE FROM accounts WHERE id = $1`, acc.ID)
	})

	e := setupAccountDeleteEcho(t, pool)
	req := httptest.NewRequest(http.MethodPost, "/accounts/"+accIDStr+"/delete", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if !hasCookie(rec, "acct_error") {
		t.Errorf("missing acct_error cookie; headers=%v", rec.Header())
	}
	if _, err := q.GetAccount(ctx, acc.ID); err != nil {
		t.Errorf("account row gone despite Pos reference: err=%v", err)
	}
}

// TestIntegration_AccountDelete_MissingReturns404Flash verifies the
// not-found branch: deleting a UUID that doesn't exist yields the
// "Account not found." flash, not the "still has Pos" message.
func TestIntegration_AccountDelete_MissingReturns404Flash(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL unset; integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("connect %s: %v", dbURL, err)
	}
	defer pool.Close()
	// Sanity: confirm the random UUID really isn't in the table.
	q := dbq.New(pool)
	randID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := q.GetAccount(ctx, randID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("seeded UUID collision (err=%v) — rerun", err)
	}

	e := setupAccountDeleteEcho(t, pool)
	req := httptest.NewRequest(http.MethodPost,
		"/accounts/"+uuid.UUID(randID.Bytes).String()+"/delete", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	var got string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "acct_error" {
			got = c.Value
		}
	}
	if !strings.Contains(got, "not found") {
		t.Errorf("acct_error = %q, want substring 'not found'", got)
	}
}

func hasCookie(rec *httptest.ResponseRecorder, name string) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return true
		}
	}
	return false
}
