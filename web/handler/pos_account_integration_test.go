package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

// TestIntegration_PosAccount_RenderAndPATCH exercises the change-account
// loop end-to-end against a real Postgres: GET /pos/:id renders the
// dropdown with the current account selected; POST /pos/:id/account
// updates the row and the redirect target's HTML reflects the change.
//
// Requires DATABASE_URL pointing at a DB seeded via db/seed/demo.sql.
// Skips otherwise.
func TestIntegration_PosAccount_RenderAndPATCH(t *testing.T) {
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
	posList, err := q.ListPos(ctx)
	if err != nil || len(posList) == 0 {
		t.Skipf("no Pos in DB; seed via db/seed/demo.sql first (err=%v)", err)
	}
	accs, err := q.ListAccounts(ctx)
	if err != nil || len(accs) < 2 {
		t.Skipf("need >=2 accounts; got %d (err=%v)", len(accs), err)
	}

	target := posList[0]
	otherAccount := accs[0]
	if otherAccount.ID == target.AccountID {
		otherAccount = accs[1]
	}

	src := bytes.NewReader(make([]byte, 64))
	a := auth.New(user.Seeded(), clock.System{}, src, idgen.Crypto{})
	h := New(a, &assistant.Recorder{}, pool)
	e := echo.New()
	e.Renderer = tplpkg.New()
	signed := user.User{ID: "test-user", DisplayName: "Tester"}
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(mw.SessionContextKey, signed)
			return next(c)
		}
	})
	e.GET("/pos/:id", h.PosGet)
	e.POST("/pos/:id/account", h.PosUpdateAccountPost)

	posIDStr := uuid.UUID(target.ID.Bytes).String()

	// 1. GET renders the change-account form with current account selected.
	req := httptest.NewRequest(http.MethodGet, "/pos/"+posIDStr, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Funding account") {
		t.Error("rendered HTML missing 'Funding account' section")
	}
	currentAccID := uuid.UUID(target.AccountID.Bytes).String()
	if !strings.Contains(body, `option value="`+currentAccID+`" selected`) {
		t.Errorf("rendered <select> doesn't have current account %s selected", currentAccID)
	}

	// 2. POST moves the Pos to a different account.
	form := url.Values{"account_id": {uuid.UUID(otherAccount.ID.Bytes).String()}}
	req = httptest.NewRequest(http.MethodPost, "/pos/"+posIDStr+"/account",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST status = %d, want 303", rec.Code)
	}

	// 3. Verify DB row mutated.
	updated, err := q.GetPos(ctx, target.ID)
	if err != nil {
		t.Fatalf("GetPos after PATCH: %v", err)
	}
	if updated.AccountID != otherAccount.ID {
		t.Fatalf("account_id not updated: got %v, want %v",
			uuid.UUID(updated.AccountID.Bytes), uuid.UUID(otherAccount.ID.Bytes))
	}

	// 4. GET again confirms the new account is selected.
	req = httptest.NewRequest(http.MethodGet, "/pos/"+posIDStr, nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	body = rec.Body.String()
	newAccID := uuid.UUID(otherAccount.ID.Bytes).String()
	if !strings.Contains(body, `option value="`+newAccID+`" selected`) {
		t.Errorf("after PATCH, new account %s not selected in dropdown", newAccID)
	}

	// 5. Restore so the test is rerunnable.
	if _, err := q.UpdatePosAccount(ctx, dbq.UpdatePosAccountParams{
		ID:        target.ID,
		AccountID: pgtype.UUID{Bytes: target.AccountID.Bytes, Valid: true},
	}); err != nil {
		t.Logf("restore failed (next run will pick up the moved Pos): %v", err)
	}
}
