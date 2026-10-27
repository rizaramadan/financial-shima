package handler

// Shared inter_pos transfer create path for POST /api/v1/transfers.
// Mirrors transaction_service.go: resolve each leg's Pos → run
// logic/transaction.ValidateInterPos → hand off to
// dependencies/ledger.Service.InsertInterPos (atomic header + legs +
// borrow obligations + notifications, spec §10.8).
//
// HATEOAS-lite: the result carries _links (self/pos/transactions/
// debts) and _actions (reads against existing GETs only) so clients
// render as thin views of the same JSON the API serves.

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
	"github.com/rizaramadan/financial-shima/logic/obligation"
	logictxn "github.com/rizaramadan/financial-shima/logic/transaction"
	"github.com/rizaramadan/financial-shima/logic/user"
)

// TransferLineInput is one transport-neutral transfer leg. Amount is
// cents (int64, never float64); the sign comes from Direction.
type TransferLineInput struct {
	PosID     uuid.UUID
	Direction string
	Amount    int64
	Currency  string
}

// TransferCreateInput is the transport-neutral shape the API handler
// maps into. Source is always notification.SourceAPI here;
// CreatedBy stays nil per spec §4.3.
type TransferCreateInput struct {
	Mode           string
	EffectiveDate  time.Time
	Lines          []TransferLineInput
	Note           string
	Source         notification.Source
	CreatedBy      *uuid.UUID
	IdempotencyKey string
}

// APITransferLine is one leg on the wire. PosName rides along so the
// caller never round-trips per leg to resolve it.
type APITransferLine struct {
	PosID     string `json:"pos_id"`
	PosName   string `json:"pos_name"`
	Direction string `json:"direction"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
}

// APITransfer is the JSON shape for a transfer in /api/v1 responses.
type APITransfer struct {
	ID             string            `json:"id"`
	Mode           string            `json:"mode"`
	EffectiveDate  string            `json:"effective_date"`
	Lines          []APITransferLine `json:"lines"`
	Note           string            `json:"note,omitempty"`
	IdempotencyKey string            `json:"idempotency_key"`
	CreatedAt      time.Time         `json:"created_at"`
	WasInserted    bool              `json:"was_inserted"`
	Links          map[string]string `json:"_links,omitempty"`
	Actions        []MoneyAction     `json:"_actions,omitempty"`
}

// transferConflictError marks an idempotency_key owned by a different
// transfer (or a non-transfer row) so callers map it to 409.
type transferConflictError struct{ msg string }

func (e *transferConflictError) Error() string { return e.msg }

// IsTransferConflict reports whether err is a key-ownership conflict.
func IsTransferConflict(err error) bool {
	var ce *transferConflictError
	return errors.As(err, &ce)
}

// validateTransferCreateInput runs the fail-fast shape checks that need
// no DB I/O. Pure — unit-tested without a database.
func validateTransferCreateInput(in TransferCreateInput) []string {
	if in.Mode != "reallocation" && in.Mode != "borrow" {
		return []string{`mode must be "reallocation" or "borrow"`}
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return []string{"idempotency_key is required"}
	}
	if in.EffectiveDate.IsZero() {
		return []string{"effective_date is required"}
	}
	if len(in.Lines) < 2 {
		return []string{"at least two lines are required (one 'out', one 'in')"}
	}
	for i, l := range in.Lines {
		if l.Direction != "out" && l.Direction != "in" {
			return []string{"line " + itoaSimple(i+1) + ": direction must be 'out' or 'in'"}
		}
		if l.Amount <= 0 {
			return []string{"line " + itoaSimple(i+1) + ": amount must be positive (sign comes from direction)"}
		}
		if strings.TrimSpace(l.Currency) == "" {
			return []string{"line " + itoaSimple(i+1) + ": currency is required"}
		}
	}
	return nil
}

// itoaSimple formats a small line number without importing strconv at
// every call site — mirrors logic/transaction's local itoa.
func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// transferLinks builds the HATEOAS-lite _links for a transfer. Pure.
func transferLinks(txnID string) map[string]string {
	return map[string]string{
		"self":         "/api/v1/transfers/" + txnID,
		"pos":          "/api/v1/pos",
		"transactions": "/api/v1/transactions",
		"debts":        "/api/v1/debts",
	}
}

// transferActions builds the _actions for a transfer. Only existing
// GETs are referenced — no new endpoints in this step.
func transferActions(txnID string) []MoneyAction {
	_ = txnID
	return []MoneyAction{
		{Name: "transactions", Method: "GET", Href: "/api/v1/transactions"},
		{Name: "debts", Method: "GET", Href: "/api/v1/debts"},
		{Name: "balances", Method: "GET", Href: "/api/v1/balances"},
	}
}

// createTransfer resolves → validates → inserts one inter_pos transfer
// via dependencies/ledger.Service (atomic, spec §10.8). today is the
// validation clock — callers pass handlerNow.
//
// Returns:
//   - (result, nil, nil) on success. result.WasInserted mirrors the
//     idempotent re-submit contract (false when the idempotency_key
//     already existed with the same payload).
//   - (zero, violations, nil) when input fails validation — caller maps
//     to 400 with violations[0].
//   - (zero, nil, *moneyNotFoundError) when a leg's pos is missing —
//     caller maps to 404.
//   - (zero, nil, *transferConflictError) when the idempotency_key is
//     owned by a different row — caller maps to 409.
//   - (zero, nil, err) on DB failure — caller maps to 500.
func createTransfer(
	ctx context.Context,
	pool *pgxpool.Pool,
	users []user.User,
	today time.Time,
	in TransferCreateInput,
) (APITransfer, []string, error) {
	var zero APITransfer

	if violations := validateTransferCreateInput(in); len(violations) > 0 {
		return zero, violations, nil
	}

	q := dbq.New(pool)

	// Resolve every leg's Pos (currency + archived state ride into
	// validation; names ride into the response).
	type resolvedPos struct {
		name     string
		currency string
		archived bool
	}
	resolved := make([]resolvedPos, len(in.Lines))
	for i, l := range in.Lines {
		p, err := q.GetPos(ctx, pgtype.UUID{Bytes: l.PosID, Valid: true})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, nil, &moneyNotFoundError{msg: "pos not found"}
			}
			return zero, nil, err
		}
		resolved[i] = resolvedPos{name: p.Name, currency: p.Currency, archived: p.Archived}
	}

	// Spec §5.1 validation via the pure logic package.
	logicLines := make([]logictxn.InterPosLine, 0, len(in.Lines))
	for i, l := range in.Lines {
		var dir logictxn.Direction
		if l.Direction == "in" {
			dir = logictxn.DirIn
		} else {
			dir = logictxn.DirOut
		}
		logicLines = append(logicLines, logictxn.InterPosLine{
			Pos:       logictxn.PosRef{ID: l.PosID.String(), Currency: resolved[i].currency, Archived: resolved[i].archived},
			Direction: dir,
			Amount:    money.New(l.Amount, l.Currency),
		})
	}
	var mode logictxn.Mode
	if in.Mode == "borrow" {
		mode = logictxn.ModeBorrow
	} else {
		mode = logictxn.ModeReallocation
	}
	if violations := logictxn.ValidateInterPos(logictxn.InterPosInput{
		EffectiveDate: in.EffectiveDate,
		Mode:          mode,
		Lines:         logicLines,
	}, today); len(violations) > 0 {
		return zero, violations, nil
	}

	ledgerLines := make([]ledger.InterPosLineInput, 0, len(in.Lines))
	for _, l := range in.Lines {
		ledgerLines = append(ledgerLines, ledger.InterPosLineInput{
			PosID:     l.PosID,
			Direction: l.Direction,
			Amount:    l.Amount,
			Currency:  l.Currency,
		})
	}
	svc := &ledger.Service{Pool: pool, Users: users}
	txnID, wasInserted, err := svc.InsertInterPos(ctx, ledger.InterPosTxnInput{
		Mode:           in.Mode,
		EffectiveDate:  pgtype.Date{Time: in.EffectiveDate, Valid: true},
		Lines:          ledgerLines,
		Note:           in.Note,
		Source:         in.Source,
		CreatedBy:      in.CreatedBy,
		IdempotencyKey: in.IdempotencyKey,
		Now:            today,
		IDGen:          uuid.NewString,
	})
	if err != nil {
		// Borrow is single-currency only: the obligation layer owns
		// that rule (ErrCrossCurrencyBorrow) and it surfaces as a
		// 400 validation failure, not a 500.
		if errors.Is(err, obligation.ErrCrossCurrencyBorrow) {
			return zero, []string{"borrow must be single-currency"}, nil
		}
		return zero, nil, err
	}

	// Re-fetch the authoritative header + legs: on replay the insert
	// was a no-op and the response must echo the STORED row (which we
	// then compare against the request for the 409 decision).
	header, err := q.GetTransaction(ctx, pgtype.UUID{Bytes: txnID, Valid: true})
	if err != nil {
		return zero, nil, err
	}
	storedLines, err := q.ListInterPosLinesByTransaction(ctx, pgtype.UUID{Bytes: txnID, Valid: true})
	if err != nil {
		return zero, nil, err
	}
	if !wasInserted && !transferMatches(header, storedLines, in) {
		return zero, nil, &transferConflictError{msg: "idempotency_key already used by another transfer"}
	}

	apiLines := make([]APITransferLine, 0, len(storedLines))
	for _, sl := range storedLines {
		posID := uuid.UUID(sl.PosID.Bytes).String()
		name := ""
		for i, l := range in.Lines {
			if l.PosID.String() == posID && resolved[i].name != "" && name == "" {
				name = resolved[i].name
			}
		}
		if name == "" {
			// Replay path: resolve names for legs whose pos wasn't in
			// this request (defensive — matches() guarantees same set).
			if p, err := q.GetPos(ctx, sl.PosID); err == nil {
				name = p.Name
			}
		}
		apiLines = append(apiLines, APITransferLine{
			PosID:     posID,
			PosName:   name,
			Direction: sl.Direction,
			Amount:    sl.Amount,
			Currency:  sl.Currency,
		})
	}

	return APITransfer{
		ID:             txnID.String(),
		Mode:           in.Mode,
		EffectiveDate:  in.EffectiveDate.Format("2006-01-02"),
		Lines:          apiLines,
		Note:           in.Note,
		IdempotencyKey: header.IdempotencyKey,
		CreatedAt:      header.CreatedAt.Time,
		WasInserted:    wasInserted,
		Links:          transferLinks(txnID.String()),
		Actions:        transferActions(txnID.String()),
	}, nil, nil
}

// transferMatches reports whether the stored header + legs equal the
// request payload (order-insensitive on legs). Pure.
func transferMatches(header dbq.Transaction, stored []dbq.InterPosLine, in TransferCreateInput) bool {
	if string(header.Type) != "inter_pos" {
		return false
	}
	if header.Mode == nil || *header.Mode != in.Mode {
		return false
	}
	hdrDay := header.EffectiveDate.Time.Format("2006-01-02")
	if hdrDay != in.EffectiveDate.Format("2006-01-02") {
		return false
	}
	var hdrNote string
	if header.Note != nil {
		hdrNote = *header.Note
	}
	if hdrNote != in.Note {
		return false
	}
	if len(stored) != len(in.Lines) {
		return false
	}
	counts := map[string]int{}
	for _, sl := range stored {
		key := uuid.UUID(sl.PosID.Bytes).String() + "\x00" + sl.Direction +
			"\x00" + itoaSimple(int(sl.Amount)) + "\x00" + sl.Currency
		counts[key]++
	}
	for _, l := range in.Lines {
		key := l.PosID.String() + "\x00" + l.Direction +
			"\x00" + itoaSimple(int(l.Amount)) + "\x00" + l.Currency
		counts[key]--
		if counts[key] < 0 {
			return false
		}
	}
	return true
}
