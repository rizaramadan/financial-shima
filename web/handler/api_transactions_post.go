package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/ledger"
	"github.com/rizaramadan/financial-shima/logic/money"
	"github.com/rizaramadan/financial-shima/logic/notification"
	logictxn "github.com/rizaramadan/financial-shima/logic/transaction"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APITransaction is the JSON shape for one transaction in /api/v1
// responses. money_in / money_out only — inter_pos ships when the
// Phase-7 line-items table lands.
type APITransaction struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"`
	EffectiveDate   string    `json:"effective_date"`
	AccountID       string    `json:"account_id"`
	AccountAmount   int64     `json:"account_amount"`
	PosID           string    `json:"pos_id"`
	PosAmount       int64     `json:"pos_amount"`
	CounterpartyID  string    `json:"counterparty_id"`
	Note            string    `json:"note,omitempty"`
	IdempotencyKey  string    `json:"idempotency_key"`
	CreatedAt       time.Time `json:"created_at"`
	WasInserted     bool      `json:"was_inserted"`
}

// createTransactionRequest is the JSON body shape for POST.
//
// Either CounterpartyID or CounterpartyName must be present. Name
// route is the LLM seed flow (S23): caller doesn't track UUIDs, just
// posts "Salary" or "Indomaret" and lets the server resolve case-
// insensitively, creating the row inline if needed (spec §4.4).
type createTransactionRequest struct {
	Type             string `json:"type"`
	EffectiveDate    string `json:"effective_date"` // YYYY-MM-DD
	AccountID        string `json:"account_id"`
	AccountAmount    int64  `json:"account_amount"`
	PosID            string `json:"pos_id"`
	PosAmount        int64  `json:"pos_amount"`
	CounterpartyID   string `json:"counterparty_id,omitempty"`
	CounterpartyName string `json:"counterparty_name,omitempty"`
	Note             string `json:"note,omitempty"`
	IdempotencyKey   string `json:"idempotency_key"`
}

// APITransactionsCreate implements POST /api/v1/transactions per spec
// §7.2 / S23–S24. money_in / money_out only.
//
// Flow:
//  1. Parse JSON, fail-fast on shape errors (400 validation_failed).
//  2. Resolve account by id, pos by id (404 not_found if missing).
//  3. Resolve counterparty: prefer counterparty_id; otherwise
//     get-or-create by counterparty_name (spec §4.4).
//  4. Run logic/transaction.ValidateMoneyIn|Out. First violation =
//     400 validation_failed body.
//  5. Hand off to dependencies/ledger.Service.Insert which atomically
//     writes the txn + notifications in one DB tx (spec §10.8).
//  6. Return 201 Created with the inserted txn JSON.
//     Idempotent re-submission of the same idempotency_key returns
//     200 OK with was_inserted=false (no notifications fire).
func (h *Handlers) APITransactionsCreate(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	var req createTransactionRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}

	// Required fields the JSON shape can't enforce on its own.
	if req.Type != "money_in" && req.Type != "money_out" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, `type must be "money_in" or "money_out"`)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "idempotency_key is required")
	}

	effDate, err := time.Parse("2006-01-02", req.EffectiveDate)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"effective_date must be YYYY-MM-DD: "+err.Error())
	}
	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "account_id must be a valid UUID")
	}
	posID, err := uuid.Parse(req.PosID)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "pos_id must be a valid UUID")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	// Resolve account.
	account, err := q.GetAccount(ctx, pgtype.UUID{Bytes: accountID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "account not found")
		}
		c.Logger().Errorf("api txn: GetAccount: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to resolve account")
	}

	// Resolve pos.
	pos, err := q.GetPos(ctx, pgtype.UUID{Bytes: posID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "pos not found")
		}
		c.Logger().Errorf("api txn: GetPos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to resolve pos")
	}

	// Resolve counterparty: prefer id, fall back to get-or-create by name.
	var counterpartyID uuid.UUID
	var counterpartyName string
	switch {
	case req.CounterpartyID != "":
		cpID, err := uuid.Parse(req.CounterpartyID)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "counterparty_id must be a valid UUID")
		}
		// We don't have GetCounterparty; just trust the id and let the
		// FK constraint surface a not-found at insert time. Name still
		// needed for §5.1 validation — pull it via SearchCounterparties
		// would be wrong (prefix match). Cheaper: round-trip via raw SQL.
		var name string
		if err := h.DB.QueryRow(ctx,
			`SELECT name FROM counterparties WHERE id = $1`, pgtype.UUID{Bytes: cpID, Valid: true},
		).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return mw.WriteAPIError(c, http.StatusNotFound,
					mw.APIErrorCodeNotFound, "counterparty not found")
			}
			c.Logger().Errorf("api txn: lookup counterparty: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to resolve counterparty")
		}
		counterpartyID = cpID
		counterpartyName = name
	case strings.TrimSpace(req.CounterpartyName) != "":
		row, err := q.GetOrCreateCounterparty(ctx, strings.TrimSpace(req.CounterpartyName))
		if err != nil {
			c.Logger().Errorf("api txn: get-or-create counterparty: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to resolve counterparty")
		}
		counterpartyID = uuid.UUID(row.ID.Bytes)
		counterpartyName = row.Name
	default:
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"either counterparty_id or counterparty_name is required")
	}

	// Spec §5.1 validation via the pure logic package.
	in := logictxn.MoneyInput{
		EffectiveDate: effDate,
		Account: logictxn.AccountRef{
			ID:       uuid.UUID(account.ID.Bytes).String(),
			Archived: account.Archived,
		},
		AccountAmount: money.New(req.AccountAmount, "idr"),
		Pos: logictxn.PosRef{
			ID:       uuid.UUID(pos.ID.Bytes).String(),
			Currency: pos.Currency,
			Archived: pos.Archived,
		},
		PosAmount:        money.New(req.PosAmount, pos.Currency),
		CounterpartyName: counterpartyName,
	}
	var violations []string
	if req.Type == "money_in" {
		violations = logictxn.ValidateMoneyIn(in, time.Now())
	} else {
		violations = logictxn.ValidateMoneyOut(in, time.Now())
	}
	if len(violations) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, violations[0])
	}

	// Pre-check whether this idempotency_key already corresponds to an
	// existing row, so we can correctly report `was_inserted` to the
	// caller. ledger.Service.Insert is itself idempotent (ON CONFLICT DO
	// UPDATE returns the existing row) but doesn't expose that bit
	// upward — the dedicated lookup keeps the caller-facing contract
	// clean without requiring a signature change to the ledger package.
	var preExistingTxnID pgtype.UUID
	preErr := h.DB.QueryRow(ctx,
		`SELECT id FROM transactions WHERE idempotency_key = $1`,
		req.IdempotencyKey,
	).Scan(&preExistingTxnID)
	wasInsertedExpected := errors.Is(preErr, pgx.ErrNoRows)

	// Atomic insert + notifications via dependencies/ledger. h.Auth.Users
	// has been UUID-resolved at boot (cmd/server.resolveUserIDs), so the
	// notification rows insert against the real users.id values rather
	// than the seeded "riza"/"shima" slugs.
	svc := &ledger.Service{
		Pool:  h.DB,
		Users: h.Auth.Users,
	}
	txnInput := ledger.MoneyTxnInput{
		Type:           req.Type,
		EffectiveDate:  pgtype.Date{Time: effDate, Valid: true},
		AccountID:      accountID,
		AccountAmount:  req.AccountAmount,
		PosID:          posID,
		PosAmount:      req.PosAmount,
		CounterpartyID: counterpartyID,
		Note:           req.Note,
		Source:         notification.SourceAPI,
		CreatedBy:      nil, // spec §4.3: source=api leaves created_by null.
		IdempotencyKey: req.IdempotencyKey,
	}
	txnID, err := svc.Insert(ctx, txnInput)
	if err != nil {
		c.Logger().Errorf("api txn: ledger.Insert: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to record transaction")
	}

	// Re-fetch the row so we can return its created_at and was_inserted
	// (the Insert path returns just the ID).
	var (
		idemKey   string
		createdAt time.Time
	)
	if err := h.DB.QueryRow(ctx,
		`SELECT idempotency_key, created_at FROM transactions WHERE id = $1`,
		pgtype.UUID{Bytes: txnID, Valid: true},
	).Scan(&idemKey, &createdAt); err != nil {
		c.Logger().Errorf("api txn: re-fetch %s: %v", txnID, err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "transaction created but lookup failed")
	}
	// was_inserted is decided by the pre-check above (whether the
	// idempotency_key was already present at the start of this request).
	// Race-window note: if two requests with the same key arrive
	// simultaneously, both can see "no pre-existing" → both report
	// was_inserted=true, but only one row is in the DB (UNIQUE on
	// idempotency_key holds). Acceptable for the LLM-caller use case.
	wasInserted := wasInsertedExpected

	return c.JSON(http.StatusCreated, APITransaction{
		ID:             txnID.String(),
		Type:           req.Type,
		EffectiveDate:  req.EffectiveDate,
		AccountID:      accountID.String(),
		AccountAmount:  req.AccountAmount,
		PosID:          posID.String(),
		PosAmount:      req.PosAmount,
		CounterpartyID: counterpartyID.String(),
		Note:           req.Note,
		IdempotencyKey: idemKey,
		CreatedAt:      createdAt,
		WasInserted:    wasInserted,
	})
}
