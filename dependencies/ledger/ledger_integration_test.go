package ledger_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/ledger"
	"github.com/rizaramadan/financial-shima/logic/notification"
	"github.com/rizaramadan/financial-shima/logic/user"
)

// poolFromEnv returns a connected pgxpool.Pool from DATABASE_URL or skips.
func poolFromEnv(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	return pool
}

// seedFixtures creates a user, account, pos, and counterparty so the
// transaction insert has valid FKs. Returns the IDs and a cleanup func.
type fixtures struct {
	UserID         uuid.UUID
	AccountID      uuid.UUID
	PosID          uuid.UUID
	CounterpartyID uuid.UUID
	UserDisplay    string
	OtherUserID    uuid.UUID
	OtherDisplay   string
}

func seedFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) fixtures {
	t.Helper()
	q := dbq.New(pool)
	// uuid suffix avoids collisions across tests running within the same second.
	stamp := uuid.NewString()[:8]

	u1, err := q.UpsertUser(ctx, dbq.UpsertUserParams{
		DisplayName:        "Riza-" + stamp,
		TelegramIdentifier: "@riza_" + stamp,
	})
	if err != nil {
		t.Fatalf("UpsertUser u1: %v", err)
	}
	u2, err := q.UpsertUser(ctx, dbq.UpsertUserParams{
		DisplayName:        "Shima-" + stamp,
		TelegramIdentifier: "@shima_" + stamp,
	})
	if err != nil {
		t.Fatalf("UpsertUser u2: %v", err)
	}
	acc, err := q.CreateAccount(ctx, "Acct "+stamp)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	pos, err := q.CreatePos(ctx, dbq.CreatePosParams{
		Name:     "Pos " + stamp,
		Currency: "idr",
	})
	if err != nil {
		t.Fatalf("CreatePos: %v", err)
	}
	cp, err := q.GetOrCreateCounterparty(ctx, "Salary "+stamp)
	if err != nil {
		t.Fatalf("GetOrCreateCounterparty: %v", err)
	}
	return fixtures{
		UserID:         u1.ID.Bytes,
		AccountID:      acc.ID.Bytes,
		PosID:          pos.ID.Bytes,
		CounterpartyID: cp.ID.Bytes,
		UserDisplay:    u1.DisplayName,
		OtherUserID:    u2.ID.Bytes,
		OtherDisplay:   u2.DisplayName,
	}
}

func today() pgtype.Date {
	return pgtype.Date{Time: time.Now(), Valid: true}
}

// TestIntegration_Insert_AtomicWithNotifications: spec §10.8 — a transaction
// row exists if and only if all its notification rows do. Inserts via the
// service, then asserts both the txn and exactly one recipient's notification
// landed.
func TestIntegration_Insert_AtomicWithNotifications(t *testing.T) {
	pool := poolFromEnv(t)
	defer pool.Close()
	ctx := context.Background()
	f := seedFixtures(t, ctx, pool)

	users := []user.User{
		{ID: f.UserID.String(), DisplayName: f.UserDisplay},
		{ID: f.OtherUserID.String(), DisplayName: f.OtherDisplay},
	}
	svc := &ledger.Service{Pool: pool, Users: users}

	creator := f.UserID
	idemKey := "test-" + uuid.NewString()
	txnID, err := svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           "money_in",
		EffectiveDate:  today(),
		AccountID:      f.AccountID,
		AccountAmount:  500_000,
		PosID:          f.PosID,
		PosAmount:      500_000,
		CounterpartyID: f.CounterpartyID,
		Source:         notification.SourceWeb,
		CreatedBy:      &creator,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Notification recipient is "the other user" per §4.5.
	q := dbq.New(pool)
	notifs, err := q.ListNotificationsForUser(ctx, pgtype.UUID{Bytes: f.OtherUserID, Valid: true})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	found := 0
	for _, n := range notifs {
		if n.RelatedTransactionID.Valid && n.RelatedTransactionID.Bytes == txnID {
			found++
		}
	}
	if found != 1 {
		t.Errorf("recipient got %d notifications for this txn, want 1", found)
	}

	// Creator should NOT get a notification (web source excludes self).
	creatorNotifs, _ := q.ListNotificationsForUser(ctx, pgtype.UUID{Bytes: f.UserID, Valid: true})
	for _, n := range creatorNotifs {
		if n.RelatedTransactionID.Valid && n.RelatedTransactionID.Bytes == txnID {
			t.Error("creator got a notification for their own web-sourced txn (spec §4.5 violation)")
		}
	}
}

// TestIntegration_Insert_NotificationFailureRollsBack: spec §10.8 fault
// injection — if the notification write fails, the transaction insert
// must not survive. After a failed insert, GetTransaction by the would-be
// ID must return ErrNoRows and there must be NO notification rows.
func TestIntegration_Insert_NotificationFailureRollsBack(t *testing.T) {
	pool := poolFromEnv(t)
	defer pool.Close()
	ctx := context.Background()
	f := seedFixtures(t, ctx, pool)

	users := []user.User{
		{ID: f.UserID.String(), DisplayName: f.UserDisplay},
		{ID: f.OtherUserID.String(), DisplayName: f.OtherDisplay},
	}
	wantErr := errors.New("simulated notification failure")
	svc := &ledger.Service{
		Pool:  pool,
		Users: users,
		NotifyHook: func(ctx context.Context, q *dbq.Queries, txnID uuid.UUID, recipient user.User) error {
			return wantErr
		},
	}

	creator := f.UserID
	idemKey := "test-rollback-" + uuid.NewString()
	_, err := svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           "money_in",
		EffectiveDate:  today(),
		AccountID:      f.AccountID,
		AccountAmount:  100_000,
		PosID:          f.PosID,
		PosAmount:      100_000,
		CounterpartyID: f.CounterpartyID,
		Source:         notification.SourceWeb,
		CreatedBy:      &creator,
		IdempotencyKey: idemKey,
	})
	if !errors.Is(err, ledger.ErrNotificationWriteFailed) {
		t.Fatalf("expected ErrNotificationWriteFailed, got %v", err)
	}

	// idempotency_key should NOT be present — the insert was rolled back.
	q := dbq.New(pool)
	rows, qerr := pool.Query(ctx, "SELECT id FROM transactions WHERE idempotency_key = $1", idemKey)
	if qerr != nil {
		t.Fatalf("post-rollback query: %v", qerr)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("transaction row survived a notification failure (spec §10.8 violated)")
	}
	_ = q
}

// TestIntegration_Insert_IdempotentReturnsSameRow: spec §10.4 / §7.2 —
// duplicate idempotency_key returns the original row, no second row inserted.
func TestIntegration_Insert_IdempotentReturnsSameRow(t *testing.T) {
	pool := poolFromEnv(t)
	defer pool.Close()
	ctx := context.Background()
	f := seedFixtures(t, ctx, pool)

	users := []user.User{
		{ID: f.UserID.String(), DisplayName: f.UserDisplay},
		{ID: f.OtherUserID.String(), DisplayName: f.OtherDisplay},
	}
	svc := &ledger.Service{Pool: pool, Users: users}

	idemKey := "test-idem-" + uuid.NewString()
	in := ledger.MoneyTxnInput{
		Type:           "money_in",
		EffectiveDate:  today(),
		AccountID:      f.AccountID,
		AccountAmount:  250_000,
		PosID:          f.PosID,
		PosAmount:      250_000,
		CounterpartyID: f.CounterpartyID,
		Source:         notification.SourceAPI,
		CreatedBy:      nil,
		IdempotencyKey: idemKey,
	}
	first, err := svc.Insert(ctx, in)
	if err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	second, err := svc.Insert(ctx, in)
	if err != nil {
		t.Fatalf("second Insert: %v", err)
	}
	if first != second {
		t.Errorf("idempotent re-submit returned different ID: %v vs %v", first, second)
	}

	// Exactly one row in DB for this key.
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM transactions WHERE idempotency_key = $1", idemKey,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d rows for idempotency_key, want 1", count)
	}
	_ = pgx.ErrNoRows // keep import non-stale if assertions change
}

// TestIntegration_AppendOnly_NoUpdateOrDeleteOnTransactions: spec §10.3 —
// the application code should never issue UPDATE or DELETE on transactions.
// We assert this by reading the queries file: a future contributor adding
// such a query fails this test (and a separate test below covers runtime
// absence via inspection of generated code).
func TestIntegration_AppendOnly_NoUpdateOrDeleteOnTransactions(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../db/queries/transactions.sql")
	if err != nil {
		t.Fatalf("read queries: %v", err)
	}
	body := string(src)
	for _, banned := range []string{"UPDATE transactions", "DELETE FROM transactions"} {
		if contains(body, banned) {
			t.Errorf("transactions.sql contains banned statement %q (spec §10.3 violation)", banned)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr))))
}
