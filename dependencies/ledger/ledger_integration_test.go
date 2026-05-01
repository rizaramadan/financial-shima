package ledger_test

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
// injection. The hook now runs AFTER InsertNotification, so the rollback
// path covers a notification row that DID land in the txn before the
// trigger error fired. We verify both halves: no transaction row and no
// notification row survive the failed Commit.
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

	// Both halves of §10.8 must hold: no transaction row AND no
	// notification row survives a failed insert.
	var txnCount, notifCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM transactions WHERE idempotency_key = $1", idemKey,
	).Scan(&txnCount); err != nil {
		t.Fatalf("count txns: %v", err)
	}
	if txnCount != 0 {
		t.Errorf("got %d transaction rows after rollback, want 0 (spec §10.8 violated)", txnCount)
	}
	// Recipient is freshly stamped per test (seedFixtures uses a uuid
	// suffix), so any notification on this user_id is from this test —
	// scope by user_id alone, no wall-clock window.
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM notifications WHERE user_id = $1",
		pgtype.UUID{Bytes: f.OtherUserID, Valid: true},
	).Scan(&notifCount); err != nil {
		t.Fatalf("count notifs: %v", err)
	}
	if notifCount != 0 {
		t.Errorf("got %d notification rows after rollback, want 0", notifCount)
	}
}

// TestIntegration_NotifyHookReturningNil_DoesNotBypassNotificationWrite
// pins the contract that the hook is an OBSERVER, not a replacement.
// A nil-returning hook must still allow the actual notification row to
// land — the bug Skeet flagged: a future tracing/metrics hook returning
// nil could otherwise commit txns with zero notifications, silently
// breaking §10.8.
func TestIntegration_NotifyHookReturningNil_DoesNotBypassNotificationWrite(t *testing.T) {
	pool := poolFromEnv(t)
	defer pool.Close()
	ctx := context.Background()
	f := seedFixtures(t, ctx, pool)

	users := []user.User{
		{ID: f.UserID.String(), DisplayName: f.UserDisplay},
		{ID: f.OtherUserID.String(), DisplayName: f.OtherDisplay},
	}
	hookCalls := 0
	svc := &ledger.Service{
		Pool:  pool,
		Users: users,
		NotifyHook: func(ctx context.Context, q *dbq.Queries, txnID uuid.UUID, recipient user.User) error {
			hookCalls++
			return nil // observer — should NOT short-circuit the real write
		},
	}

	creator := f.UserID
	idemKey := "test-hook-nil-" + uuid.NewString()
	txnID, err := svc.Insert(ctx, ledger.MoneyTxnInput{
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
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if hookCalls != 1 {
		t.Errorf("hook called %d times, want 1 (one recipient on web source)", hookCalls)
	}
	// Real notification row must exist for recipient despite hook returning nil.
	q := dbq.New(pool)
	notifs, _ := q.ListNotificationsForUser(ctx, pgtype.UUID{Bytes: f.OtherUserID, Valid: true})
	found := false
	for _, n := range notifs {
		if n.RelatedTransactionID.Valid && n.RelatedTransactionID.Bytes == txnID {
			found = true
			break
		}
	}
	if !found {
		t.Error("notification row missing — hook short-circuited the real write (Skeet R1 regression)")
	}
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
}

// TestIntegration_Insert_Idempotent_NoDuplicateNotifications: spec §10.8 +
// §7.2 interaction — the second submission must NOT spawn extra
// notification rows. Without the WasInserted gate in ledger.go this fails
// (recipient gets one notification per submission). Beck Phase-6 R2.
func TestIntegration_Insert_Idempotent_NoDuplicateNotifications(t *testing.T) {
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
	idemKey := "test-idem-notif-" + uuid.NewString()
	in := ledger.MoneyTxnInput{
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
	}
	txnID, err := svc.Insert(ctx, in)
	if err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	if _, err := svc.Insert(ctx, in); err != nil {
		t.Fatalf("second Insert (idempotent): %v", err)
	}

	// Recipient should have exactly ONE notification for this txn,
	// regardless of how many times we re-submit.
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM notifications
		WHERE user_id = $1 AND related_transaction_id = $2`,
		pgtype.UUID{Bytes: f.OtherUserID, Valid: true},
		pgtype.UUID{Bytes: txnID, Valid: true},
	).Scan(&count); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d notifications for idempotent re-submit, want 1 (spec §10.8 violated)", count)
	}
}

// TestIntegration_Insert_SeedSource_NoNotifications: spec §4.5 — seed
// source produces no notification rows. Phase-1/initial-load operators
// shouldn't ping every user with N transaction notifications. Beck R6.
func TestIntegration_Insert_SeedSource_NoNotifications(t *testing.T) {
	pool := poolFromEnv(t)
	defer pool.Close()
	ctx := context.Background()
	f := seedFixtures(t, ctx, pool)

	users := []user.User{
		{ID: f.UserID.String(), DisplayName: f.UserDisplay},
		{ID: f.OtherUserID.String(), DisplayName: f.OtherDisplay},
	}
	svc := &ledger.Service{Pool: pool, Users: users}

	idemKey := "test-seed-" + uuid.NewString()
	txnID, err := svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           "money_in",
		EffectiveDate:  today(),
		AccountID:      f.AccountID,
		AccountAmount:  100_000,
		PosID:          f.PosID,
		PosAmount:      100_000,
		CounterpartyID: f.CounterpartyID,
		Source:         notification.SourceSeed,
		CreatedBy:      nil,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		t.Fatalf("Insert seed: %v", err)
	}

	// Both users — neither should have a notification for this txn.
	for _, uid := range []uuid.UUID{f.UserID, f.OtherUserID} {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM notifications
			WHERE user_id = $1 AND related_transaction_id = $2`,
			pgtype.UUID{Bytes: uid, Valid: true},
			pgtype.UUID{Bytes: txnID, Valid: true},
		).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Errorf("seed source produced %d notifications for user %v, want 0",
				count, uid)
		}
	}
}

// TestIntegration_Insert_NonUUIDUserID_FailsFast: parseUserID rejects
// non-UUID user IDs with a clear error. Without this guard the failure
// mode is a confusing not-null-constraint violation downstream. Beck R7
// (Skeet Phase-2-7 R5 paired with the test that pins the contract).
func TestIntegration_Insert_NonUUIDUserID_FailsFast(t *testing.T) {
	pool := poolFromEnv(t)
	defer pool.Close()
	ctx := context.Background()
	f := seedFixtures(t, ctx, pool)

	// Inject a user with a non-UUID ID — the seeded user list in
	// logic/user uses string IDs like "riza", which would not parse.
	users := []user.User{
		{ID: "riza", DisplayName: "Riza"}, // non-UUID
		{ID: f.OtherUserID.String(), DisplayName: f.OtherDisplay},
	}
	svc := &ledger.Service{Pool: pool, Users: users}

	creator := f.UserID
	idemKey := "test-bad-uid-" + uuid.NewString()
	_, err := svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           "money_in",
		EffectiveDate:  today(),
		AccountID:      f.AccountID,
		AccountAmount:  1,
		PosID:          f.PosID,
		PosAmount:      1,
		CounterpartyID: f.CounterpartyID,
		Source:         notification.SourceWeb,
		CreatedBy:      &creator,
		IdempotencyKey: idemKey,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "is not a uuid") {
		t.Errorf("err = %v, want it to mention 'is not a uuid'", err)
	}
	// And the txn must NOT have committed.
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM transactions WHERE idempotency_key = $1",
		idemKey).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("transaction committed despite bad user ID (got %d rows)", count)
	}
}

// TestLint_AppendOnly_NoBannedStatementsAcrossSQL is a static lint that
// scans every SQL file under db/queries/ and db/migrations/ for any
// UPDATE/DELETE statement targeting the transactions table. The previous
// version was substring-on-one-file with whitespace fragility. Now it
// uses a case-insensitive regex with \s+ between keyword and table, and
// walks the whole SQL corpus. The DO UPDATE SET inside the upsert is
// excluded by anchoring `UPDATE\s+transactions\b` (no SET keyword).
func TestLint_AppendOnly_NoBannedStatementsAcrossSQL(t *testing.T) {
	t.Parallel()
	bannedRE := regexp.MustCompile(`(?i)\b(?:update|delete\s+from)\s+transactions\b`)
	roots := []string{"../../db/queries", "../../db/migrations"}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("read %s: %v", root, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			path := root + "/" + e.Name()
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for _, m := range bannedRE.FindAllString(string(body), -1) {
				t.Errorf("%s contains banned %q (spec §10.3 forbids UPDATE/DELETE on transactions)",
					path, m)
			}
		}
	}
}
