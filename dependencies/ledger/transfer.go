// Package ledger — inter_pos transfer insert (Phase 7).
//
// InsertInterPos atomically writes the transfer header, its legs, any
// borrow-mode obligation rows, and the notification rows in a single
// Postgres transaction, mirroring Service.Insert's spec §10.8
// "transaction exists iff its notifications exist" guarantee.
//
// Idempotency mirrors Insert: ON CONFLICT on idempotency_key returns
// the existing row with was_inserted=false and writes nothing further,
// so a retried POST can never duplicate legs, obligations, or
// notifications. The caller compares the existing row against the
// request to decide between replay (same payload) and 409 (key owned
// by a different transfer).
package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/logic/notification"
	"github.com/rizaramadan/financial-shima/logic/obligation"
)

// InterPosLineInput is one validated leg of a transfer. Amount is
// cents (int64, never float64); sign comes from Direction.
type InterPosLineInput struct {
	PosID     uuid.UUID
	Direction string // "out" | "in"
	Amount    int64  // > 0
	Currency  string // lowercase, matches the pos's currency
}

// InterPosTxnInput is the validated, hydrated shape InsertInterPos
// accepts. The caller has already run logic/transaction.ValidateInterPos
// — the Service re-checks nothing except the borrow leg derivation,
// which needs the inserted transaction ID.
//
// Now is the injected clock for obligation created_at (logic/ is
// deterministic — the Service never calls time.Now itself). IDGen
// mints obligation row IDs (uuid.NewString in production).
type InterPosTxnInput struct {
	Mode           string // "reallocation" | "borrow"
	EffectiveDate  pgtype.Date
	Lines          []InterPosLineInput
	Note           string // empty allowed
	Source         notification.Source
	CreatedBy      *uuid.UUID // nil for seed/api
	IdempotencyKey string
	Now            time.Time
	IDGen          func() string
}

// InsertInterPos atomically inserts the transfer and returns the
// header ID. wasInserted is false on idempotent re-submission (the
// existing row is returned, nothing is written).
func (s *Service) InsertInterPos(ctx context.Context, in InterPosTxnInput) (txnID uuid.UUID, wasInserted bool, err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	q := dbq.New(tx)

	createdBy := pgtype.UUID{}
	if in.CreatedBy != nil {
		createdBy = pgtype.UUID{Bytes: *in.CreatedBy, Valid: true}
	}

	header, err := q.InsertInterPosTransaction(ctx, dbq.InsertInterPosTransactionParams{
		Mode:           ptr(in.Mode),
		EffectiveDate:  in.EffectiveDate,
		Note:           ptrStr(in.Note),
		Source:         dbq.TransactionSource(in.Source),
		CreatedBy:      createdBy,
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("insert inter_pos transaction: %w", err)
	}
	txnID = header.ID.Bytes

	// Idempotent re-submission: the header already existed, so its
	// legs, obligations, and notifications already exist too — same
	// skip rule as Service.Insert (spec §10.8).
	if !header.WasInserted {
		if err := tx.Commit(ctx); err != nil {
			return uuid.Nil, false, fmt.Errorf("commit (idempotent hit): %w", err)
		}
		committed = true
		return txnID, false, nil
	}

	txnPGID := pgtype.UUID{Bytes: txnID, Valid: true}
	for _, l := range in.Lines {
		if _, err := q.InsertInterPosLine(ctx, dbq.InsertInterPosLineParams{
			TransactionID: txnPGID,
			PosID:         pgtype.UUID{Bytes: l.PosID, Valid: true},
			Direction:     l.Direction,
			Amount:        l.Amount,
			Currency:      l.Currency,
		}); err != nil {
			return uuid.Nil, false, fmt.Errorf("insert inter_pos line: %w", err)
		}
	}

	if in.Mode == "borrow" {
		obLines := make([]obligation.Line, 0, len(in.Lines))
		for _, l := range in.Lines {
			dir := obligation.DirOut
			if l.Direction == "in" {
				dir = obligation.DirIn
			}
			obLines = append(obLines, obligation.Line{
				PosID:     l.PosID.String(),
				Currency:  l.Currency,
				Direction: dir,
				Amount:    l.Amount,
			})
		}
		// Single-currency is enforced here: a multi-currency borrow
		// surfaces ErrCrossCurrencyBorrow, which the handler maps to
		// 400 (logic owns the rule; the handler owns the status).
		obs, err := obligation.GenerateBorrowObligations(txnID.String(), obLines, in.Now, in.IDGen)
		if err != nil {
			return uuid.Nil, false, fmt.Errorf("generate borrow obligations: %w", err)
		}
		for _, o := range obs {
			creditorID, cerr := uuid.Parse(o.CreditorPosID)
			if cerr != nil {
				return uuid.Nil, false, fmt.Errorf("obligation creditor id: %w", cerr)
			}
			debtorID, derr := uuid.Parse(o.DebtorPosID)
			if derr != nil {
				return uuid.Nil, false, fmt.Errorf("obligation debtor id: %w", derr)
			}
			if _, err := q.InsertObligation(ctx, dbq.InsertObligationParams{
				TransactionID: txnPGID,
				CreditorPosID: pgtype.UUID{Bytes: creditorID, Valid: true},
				DebtorPosID:   pgtype.UUID{Bytes: debtorID, Valid: true},
				Currency:      o.Currency,
				AmountOwed:    o.Owed,
			}); err != nil {
				return uuid.Nil, false, fmt.Errorf("insert obligation: %w", err)
			}
		}
	}

	// §4.5 recipient rule, same as Service.Insert.
	createdByID := ""
	if in.CreatedBy != nil {
		createdByID = in.CreatedBy.String()
	}
	recipients := notification.RecipientsFor(in.Source, createdByID, s.Users)
	for _, r := range recipients {
		uid, perr := parseUserID(r.ID)
		if perr != nil {
			return uuid.Nil, false, fmt.Errorf("recipient %q: %w", r.ID, perr)
		}
		_, err := q.InsertNotification(ctx, dbq.InsertNotificationParams{
			UserID:               pgtype.UUID{Bytes: uid, Valid: true},
			Type:                 dbq.NotificationTypeTransactionCreated,
			Title:                transferNotificationTitle(in.Mode, r.DisplayName),
			Body:                 ptrStr(in.Note),
			RelatedTransactionID: txnPGID,
		})
		if err != nil {
			return uuid.Nil, false, fmt.Errorf("%w: %v", ErrNotificationWriteFailed, err)
		}
		if s.NotifyHook != nil {
			if err := s.NotifyHook(ctx, q, txnID, r); err != nil {
				return uuid.Nil, false, fmt.Errorf("%w: %v", ErrNotificationWriteFailed, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return txnID, true, nil
}

// transferNotificationTitle mirrors notificationTitle for transfers:
// "Riza logged an inter_pos transfer (borrow)".
func transferNotificationTitle(mode, displayName string) string {
	return displayName + " logged an inter_pos transfer (" + mode + ")"
}
