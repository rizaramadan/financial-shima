package handler

// Shared money_in / money_out create path (Step 1 of the API-first
// refactor). Both TransactionNewPost (web form) and APITransactionsCreate
// (POST /api/v1/transactions) resolve → validate → insert through
// createMoneyTransaction, so the two entries stay behavior-identical by
// construction. Handlers keep only their transport concerns: parsing
// their own input shape (form fields vs JSON) and rendering their own
// output (redirect vs JSON).
//
// HATEOAS-lite: the result carries _links (self/pos/account/
// counterparty/list) and _actions (reads against existing GETs only —
// no new endpoints in this step) so HTMX pages can render as thin
// views of the same JSON the API serves.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/ledger"
	"github.com/rizaramadan/financial-shima/logic/money"
	"github.com/rizaramadan/financial-shima/logic/notification"
	logictxn "github.com/rizaramadan/financial-shima/logic/transaction"
	"github.com/rizaramadan/financial-shima/logic/user"
)

// MoneyCreateInput is the transport-neutral shape both create entries
// map into. Pos is always by ID here — the web form resolves its typed
// label → ID via ListPos before calling in (label matching is a UI
// concern: IDR-only, case-insensitive). Counterparty follows the API
// rule: prefer ID, else get-or-create by name (spec §4.4).
type MoneyCreateInput struct {
	Type             string
	EffectiveDate    time.Time
	AccountAmount    int64
	PosID            uuid.UUID
	PosAmount        int64
	CounterpartyID   *uuid.UUID
	CounterpartyName string
	Note             string
	Source           notification.Source
	CreatedBy        *uuid.UUID // nil for source=api per spec §4.3
	IdempotencyKey   string
	// ReversesID marks this insert as a correction of another
	// transaction (append-only, spec §10.3). Nil for ordinary inserts.
	ReversesID *uuid.UUID
}

// moneyNotFoundError marks a missing pos/account/counterparty so
// callers can map it to 404 (vs 500 for real DB failures).
type moneyNotFoundError struct{ msg string }

func (e *moneyNotFoundError) Error() string { return e.msg }

// IsMoneyNotFound reports whether err is a missing-resource failure.
func IsMoneyNotFound(err error) bool {
	var nf *moneyNotFoundError
	return errors.As(err, &nf)
}

// reverseConflictError marks a reversal rejected because the original
// is already reversed (or the idempotency key belongs to another row)
// so callers can map it to 409 (vs 404 for a missing original or 500
// for real DB failures).
type reverseConflictError struct{ msg string }

func (e *reverseConflictError) Error() string { return e.msg }

// IsReverseConflict reports whether err is an already-reversed failure.
func IsReverseConflict(err error) bool {
	var ce *reverseConflictError
	return errors.As(err, &ce)
}

// reverseType flips a money type to its offsetting counterpart for an
// append-only reversal. Amounts stay positive — sign comes from the
// type. Pure — unit-tested without a database.
func reverseType(orig string) (string, bool) {
	switch orig {
	case "money_in":
		return "money_out", true
	case "money_out":
		return "money_in", true
	default:
		return "", false
	}
}

// validateMoneyCreateInput runs the fail-fast shape checks that need
// no DB I/O. Pure — unit-tested without a database.
func validateMoneyCreateInput(in MoneyCreateInput) []string {
	if in.Type != "money_in" && in.Type != "money_out" {
		return []string{`type must be "money_in" or "money_out"`}
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return []string{"idempotency_key is required"}
	}
	if in.EffectiveDate.IsZero() {
		return []string{"effective_date is required"}
	}
	if in.CounterpartyID == nil && strings.TrimSpace(in.CounterpartyName) == "" {
		return []string{"either counterparty_id or counterparty_name is required"}
	}
	return nil
}

// moneyLinks builds the HATEOAS-lite _links for a created transaction.
// Pure — unit-tested without a database.
func moneyLinks(txnID, posID, accountID, counterpartyID string) map[string]string {
	return map[string]string{
		"self":         "/api/v1/transactions/" + txnID,
		"list":         "/api/v1/transactions",
		"pos":          "/api/v1/pos/" + posID,
		"account":      "/api/v1/accounts/" + accountID,
		"counterparty": "/api/v1/counterparties/" + counterpartyID,
	}
}

// MoneyAction is one HATEOAS-lite _actions entry: a state-changing or
// navigation affordance the client can follow without hard-coding routes.
type MoneyAction struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Href   string `json:"href"`
}

// moneyActions builds the _actions for a created transaction. Only
// existing GETs are referenced — no new endpoints in this step.
func moneyActions(txnID string) []MoneyAction {
	return []MoneyAction{
		{Name: "list", Method: "GET", Href: "/api/v1/transactions"},
		{Name: "self", Method: "GET", Href: "/api/v1/transactions/" + txnID},
	}
}

// handlerNow returns the handler's injected clock time, falling back
// to the wall clock when Auth is unwired. Production wires
// clock.System via auth.New; tests wire clock.Fixed.
func handlerNow(h *Handlers) time.Time {
	if h != nil && h.Auth != nil && h.Auth.Clock != nil {
		return h.Auth.Clock.Now()
	}
	return time.Now()
}

// createMoneyTransaction resolves → validates → inserts one money_in /
// money_out via dependencies/ledger.Service (atomic txn + notifications,
// spec §10.8). today is the validation clock — callers pass
// h.Auth.Clock.Now() so tests inject a fixed time; the service never
// calls time.Now itself.
//
// Returns:
//   - (result, nil, nil) on success. result.WasInserted mirrors the
//     idempotent re-submit contract (false when the idempotency_key
//     already existed).
//   - (zero, violations, nil) when input fails validation — caller maps
//     to 400 with violations[0].
//   - (zero, nil, *moneyNotFoundError) when pos/account/counterparty is
//     missing — caller maps to 404.
//   - (zero, nil, err) on DB failure — caller maps to 500.
func createMoneyTransaction(
	ctx context.Context,
	pool *pgxpool.Pool,
	users []user.User,
	today time.Time,
	in MoneyCreateInput,
) (APITransaction, []string, error) {
	var zero APITransaction

	if violations := validateMoneyCreateInput(in); len(violations) > 0 {
		return zero, violations, nil
	}

	q := dbq.New(pool)

	// Resolve pos. Account is reached via pos.account_id (§4.2/§5.6);
	// we then load that account to know if it's archived (validation
	// surface from spec §5.1).
	pos, err := q.GetPos(ctx, pgtype.UUID{Bytes: in.PosID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil, &moneyNotFoundError{msg: "pos not found"}
		}
		return zero, nil, err
	}
	account, err := q.GetAccount(ctx, pos.AccountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil, &moneyNotFoundError{msg: "pos's account not found"}
		}
		return zero, nil, err
	}

	// Resolve counterparty: prefer id, fall back to get-or-create by name.
	var counterpartyID uuid.UUID
	var counterpartyName string
	if in.CounterpartyID != nil {
		var name string
		if err := pool.QueryRow(ctx,
			`SELECT name FROM counterparties WHERE id = $1`,
			pgtype.UUID{Bytes: *in.CounterpartyID, Valid: true},
		).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, nil, &moneyNotFoundError{msg: "counterparty not found"}
			}
			return zero, nil, err
		}
		counterpartyID = *in.CounterpartyID
		counterpartyName = name
	} else {
		row, err := q.GetOrCreateCounterparty(ctx, strings.TrimSpace(in.CounterpartyName))
		if err != nil {
			return zero, nil, err
		}
		counterpartyID = uuid.UUID(row.ID.Bytes)
		counterpartyName = row.Name
	}

	// Spec §5.1 validation via the pure logic package. Account-archived
	// state rides on PosRef (§4.2/§5.6: account is reached through
	// pos.account_id).
	logicIn := logictxn.MoneyInput{
		EffectiveDate: in.EffectiveDate,
		AccountAmount: money.New(in.AccountAmount, "idr"),
		Pos: logictxn.PosRef{
			ID:              uuid.UUID(pos.ID.Bytes).String(),
			Currency:        pos.Currency,
			Archived:        pos.Archived,
			AccountID:       uuid.UUID(account.ID.Bytes).String(),
			AccountArchived: account.Archived,
		},
		PosAmount:        money.New(in.PosAmount, pos.Currency),
		CounterpartyName: counterpartyName,
	}
	var violations []string
	if in.Type == "money_in" {
		violations = logictxn.ValidateMoneyIn(logicIn, today)
	} else {
		violations = logictxn.ValidateMoneyOut(logicIn, today)
	}
	if len(violations) > 0 {
		return zero, violations, nil
	}

	// Pre-check whether this idempotency_key already corresponds to an
	// existing row, so we can correctly report `was_inserted` to the
	// caller. ledger.Service.Insert is itself idempotent (ON CONFLICT DO
	// UPDATE returns the existing row) but only surfaces the ID — the
	// dedicated lookup keeps the caller-facing contract clean without
	// requiring a signature change to the ledger package.
	var preExistingTxnID pgtype.UUID
	preErr := pool.QueryRow(ctx,
		`SELECT id FROM transactions WHERE idempotency_key = $1`,
		in.IdempotencyKey,
	).Scan(&preExistingTxnID)
	wasInsertedExpected := errors.Is(preErr, pgx.ErrNoRows)

	svc := &ledger.Service{Pool: pool, Users: users}
	txnID, err := svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           in.Type,
		EffectiveDate:  pgtype.Date{Time: in.EffectiveDate, Valid: true},
		AccountAmount:  in.AccountAmount,
		PosID:          in.PosID,
		PosAmount:      in.PosAmount,
		CounterpartyID: counterpartyID,
		Note:           in.Note,
		Source:         in.Source,
		CreatedBy:      in.CreatedBy,
		IdempotencyKey: in.IdempotencyKey,
		ReversesID:     in.ReversesID,
	})
	if err != nil {
		return zero, nil, err
	}

	// Re-fetch the row so we can return its created_at (the Insert path
	// returns just the ID).
	var (
		idemKey   string
		createdAt time.Time
	)
	if err := pool.QueryRow(ctx,
		`SELECT idempotency_key, created_at FROM transactions WHERE id = $1`,
		pgtype.UUID{Bytes: txnID, Valid: true},
	).Scan(&idemKey, &createdAt); err != nil {
		return zero, nil, err
	}

	accountIDStr := uuid.UUID(account.ID.Bytes).String()
	posIDStr := in.PosID.String()
	cpIDStr := counterpartyID.String()
	effDateStr := in.EffectiveDate.Format("2006-01-02")

	result := APITransaction{
		ID:             txnID.String(),
		Type:           in.Type,
		EffectiveDate:  effDateStr,
		AccountID:      accountIDStr,
		AccountAmount:  in.AccountAmount,
		PosID:          posIDStr,
		PosAmount:      in.PosAmount,
		CounterpartyID: cpIDStr,
		Note:           in.Note,
		IdempotencyKey: idemKey,
		CreatedAt:      createdAt,
		WasInserted:    wasInsertedExpected,
		Links:          moneyLinks(txnID.String(), posIDStr, accountIDStr, cpIDStr),
		Actions:        moneyActions(txnID.String()),
	}
	if in.ReversesID != nil {
		origIDStr := in.ReversesID.String()
		result.ReversesID = origIDStr
		result.Links["original"] = "/api/v1/transactions/" + origIDStr
	}
	return result, nil, nil
}

// reverseMoneyTransaction creates the offsetting entry for a posted
// money_in / money_out (append-only correction, spec §10.3). The
// original row is never updated: the reversal carries the flipped type
// with identical positive amounts plus reverses_id pointing at the
// original, so balances net to zero.
//
// idemKey is client-supplied when non-empty, else derived as
// "reverse:<origID>" so a body-less retry is idempotent by default.
// Same-key re-submission returns the existing reversal
// (was_inserted=false); a different key against an already-reversed
// original is a 409-style conflict — callers map via
// IsMoneyNotFound / IsReverseConflict.
//
// Returns (like createMoneyTransaction):
//   - (result, nil, nil) on success or idempotent re-submit.
//   - (zero, violations, nil) when the original's type/amounts fail
//     validation — caller maps to 400 with violations[0].
//   - (zero, nil, *moneyNotFoundError) when the original is missing.
//   - (zero, nil, *reverseConflictError) when already reversed.
//   - (zero, nil, err) on DB failure — caller maps to 500.
func reverseMoneyTransaction(
	ctx context.Context,
	pool *pgxpool.Pool,
	users []user.User,
	today time.Time,
	origID uuid.UUID,
	idemKey string,
) (APITransaction, []string, error) {
	var zero APITransaction

	q := dbq.New(pool)
	orig, err := q.GetTransaction(ctx, pgtype.UUID{Bytes: origID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil, &moneyNotFoundError{msg: "transaction not found"}
		}
		return zero, nil, err
	}

	revType, ok := reverseType(string(orig.Type))
	if !ok {
		return zero, []string{"only money_in and money_out transactions can be reversed"}, nil
	}
	if orig.AccountAmount == nil || orig.PosAmount == nil {
		return zero, []string{"original transaction has no amounts to reverse"}, nil
	}
	if strings.TrimSpace(idemKey) == "" {
		idemKey = "reverse:" + origID.String()
	}

	// Idempotent re-submit: the same key already produced the reversal
	// for this original — fall through to createMoneyTransaction, whose
	// ON CONFLICT path returns the existing row with
	// was_inserted=false. A key owned by any other row is a conflict.
	var hitID, hitReverses pgtype.UUID
	hitErr := pool.QueryRow(ctx,
		`SELECT id, reverses_id FROM transactions WHERE idempotency_key = $1`,
		idemKey,
	).Scan(&hitID, &hitReverses)
	if hitErr == nil {
		if !hitReverses.Valid || uuid.UUID(hitReverses.Bytes) != origID {
			return zero, nil, &reverseConflictError{msg: "idempotency_key already used by another transaction"}
		}
	} else if !errors.Is(hitErr, pgx.ErrNoRows) {
		return zero, nil, hitErr
	}

	// Fresh key but the original already has a reversal — reject so a
	// miscorrection can't silently stack a second offsetting entry.
	if errors.Is(hitErr, pgx.ErrNoRows) {
		var existingRev pgtype.UUID
		revErr := pool.QueryRow(ctx,
			`SELECT id FROM transactions WHERE reverses_id = $1 LIMIT 1`,
			pgtype.UUID{Bytes: origID, Valid: true},
		).Scan(&existingRev)
		if revErr == nil {
			return zero, nil, &reverseConflictError{msg: "transaction already reversed"}
		} else if !errors.Is(revErr, pgx.ErrNoRows) {
			return zero, nil, revErr
		}
	}

	var note string
	if orig.Note != nil {
		note = *orig.Note
	}
	cpID := uuid.UUID(orig.CounterpartyID.Bytes)
	return createMoneyTransaction(ctx, pool, users, today, MoneyCreateInput{
		Type:           revType,
		EffectiveDate:  orig.EffectiveDate.Time,
		AccountAmount:  *orig.AccountAmount,
		PosID:          uuid.UUID(orig.PosID.Bytes),
		PosAmount:      *orig.PosAmount,
		CounterpartyID: &cpID,
		Note:           note,
		Source:         notification.SourceAPI,
		CreatedBy:      nil, // spec §4.3: source=api leaves created_by null.
		IdempotencyKey: idemKey,
		ReversesID:     &origID,
	})
}
