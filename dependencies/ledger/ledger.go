// Package ledger is the Phase-6 atomic insert path for money_in / money_out
// transactions. It writes the transaction row and its notification rows in a
// single Postgres transaction so spec §10.8's "transaction exists iff its
// notifications exist" invariant holds even under partial-failure conditions.
//
// Spec §10.3 (append-only) is enforced by the surface here: the Service
// exposes only Insert. There is intentionally no Update or Delete — corrections
// must be a separate Insert with `reverses_id` set, never an in-place edit.
package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/logic/notification"
	"github.com/rizaramadan/financial-shima/logic/user"
)

// MoneyTxnInput is the validated, hydrated shape the Service accepts.
// All FKs are present and the caller has already run logic/transaction.Validate*
// on the user-facing input — Service does not re-validate spec §5.1 rules.
type MoneyTxnInput struct {
	Type            string // "money_in" or "money_out"
	EffectiveDate   pgtype.Date
	AccountID       uuid.UUID
	AccountAmount   int64
	PosID           uuid.UUID
	PosAmount       int64
	CounterpartyID  uuid.UUID
	Note            string // empty allowed
	Source          notification.Source
	CreatedBy       *uuid.UUID // nil for seed/api
	IdempotencyKey  string
}

// Pool is the subset of pgxpool.Pool the Service needs. Tests inject a
// pgxmock pool or an interface adapter.
type Pool interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// NotifyHook is a side-channel observer called AFTER each successful
// q.InsertNotification. Returning a non-nil error rolls back the
// transaction (used by tests to drive the §10.8 fault path). Hook does
// NOT replace the real notification write — that runs unconditionally —
// so a hook that returns nil cannot silently break atomicity.
type NotifyHook func(ctx context.Context, q *dbq.Queries, txnID uuid.UUID, recipient user.User) error

// Service wires the Pool with the seeded user list and an optional notify
// hook (for fault injection).
type Service struct {
	Pool       Pool
	Users      []user.User
	NotifyHook NotifyHook
}

// ErrNotificationWriteFailed is wrapped when the notify-hook returns a
// non-nil error during Insert. Callers can errors.Is() it to distinguish
// "the ledger row was rejected" from "we deliberately rolled back".
var ErrNotificationWriteFailed = errors.New("ledger: notification write failed")

// Insert atomically writes the money transaction row and any notification
// rows produced by spec §4.5's recipient rule. Returns the inserted (or
// existing, on idempotent re-submission) transaction ID.
func (s *Service) Insert(ctx context.Context, in MoneyTxnInput) (uuid.UUID, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	q := dbq.New(tx)

	// Convert FKs to pgtype where sqlc expects them; uuid.UUID is the
	// natural input but the generated structs sometimes use pgtype.UUID
	// for nullable columns.
	createdBy := pgtype.UUID{}
	if in.CreatedBy != nil {
		createdBy = pgtype.UUID{Bytes: *in.CreatedBy, Valid: true}
	}

	row, err := q.InsertMoneyTransaction(ctx, dbq.InsertMoneyTransactionParams{
		Type:           dbq.TransactionType(in.Type),
		EffectiveDate:  in.EffectiveDate,
		AccountID:      pgtype.UUID{Bytes: in.AccountID, Valid: true},
		AccountAmount:  ptr(in.AccountAmount),
		PosID:          pgtype.UUID{Bytes: in.PosID, Valid: true},
		PosAmount:      ptr(in.PosAmount),
		CounterpartyID: pgtype.UUID{Bytes: in.CounterpartyID, Valid: true},
		Note:           ptrStr(in.Note),
		Source:         dbq.TransactionSource(in.Source),
		CreatedBy:      createdBy,
		IdempotencyKey: in.IdempotencyKey,
		ReversesID:     pgtype.UUID{},
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert transaction: %w", err)
	}

	// Spec §7.2 idempotent re-submission: when WasInserted is false the
	// row already existed, so its notification rows already exist too —
	// skip the notification loop or we'd duplicate them on every retry,
	// which spec §10.8 forbids. Commit and return the original ID.
	if !row.WasInserted {
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, fmt.Errorf("commit (idempotent hit): %w", err)
		}
		committed = true
		return row.ID.Bytes, nil
	}

	// §4.5 recipient rule. createdByID is the string form for comparison
	// against user.User.ID.
	createdByID := ""
	if in.CreatedBy != nil {
		createdByID = in.CreatedBy.String()
	}
	recipients := notification.RecipientsFor(in.Source, createdByID, s.Users)

	for _, r := range recipients {
		uid, perr := parseUserID(r.ID)
		if perr != nil {
			return uuid.Nil, fmt.Errorf("recipient %q: %w", r.ID, perr)
		}
		title := notificationTitle(in.Type, r.DisplayName)
		_, err := q.InsertNotification(ctx, dbq.InsertNotificationParams{
			UserID:               pgtype.UUID{Bytes: uid, Valid: true},
			Type:                 dbq.NotificationTypeTransactionCreated,
			Title:                title,
			Body:                 ptrStr(in.Note),
			RelatedTransactionID: pgtype.UUID{Bytes: row.ID.Bytes, Valid: true},
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("%w: %v", ErrNotificationWriteFailed, err)
		}
		// Hook runs AFTER the real write — observer or fault injector,
		// never a replacement (Skeet review issue 1).
		if s.NotifyHook != nil {
			if err := s.NotifyHook(ctx, q, row.ID.Bytes, r); err != nil {
				return uuid.Nil, fmt.Errorf("%w: %v", ErrNotificationWriteFailed, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return row.ID.Bytes, nil
}

func notificationTitle(txnType, displayName string) string {
	return displayName + " logged a " + txnType
}

func ptr[T any](v T) *T   { return &v }
func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// parseUserID converts a user.User.ID string to a uuid.UUID. The seed file
// (logic/user) currently uses string IDs like "riza" — those will not parse
// here. Returning an explicit error lets Insert fail fast with a clear
// message (Skeet review issue 5) instead of pushing a NULL through to the
// DB and surfacing a confusing not-null-constraint violation.
func parseUserID(id string) (uuid.UUID, error) {
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ledger: user id %q is not a uuid: %w", id, err)
	}
	return u, nil
}
