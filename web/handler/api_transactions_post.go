package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/logic/notification"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APITransaction is the JSON shape for one transaction in /api/v1
// responses. money_in / money_out only — inter_pos ships when the
// Phase-7 line-items table lands.
//
// AccountID echoes the Pos's *current* account (§5.6 snapshot view) —
// the field is read-only on the wire; transactions don't carry
// account_id of their own (§4.2).
type APITransaction struct {
	ID             string            `json:"id"`
	Type           string            `json:"type"`
	EffectiveDate  string            `json:"effective_date"`
	AccountID      string            `json:"account_id"`
	AccountAmount  int64             `json:"account_amount"`
	PosID          string            `json:"pos_id"`
	PosAmount      int64             `json:"pos_amount"`
	CounterpartyID string            `json:"counterparty_id"`
	Note           string            `json:"note,omitempty"`
	IdempotencyKey string            `json:"idempotency_key"`
	CreatedAt      time.Time         `json:"created_at"`
	WasInserted    bool              `json:"was_inserted"`
	ReversesID     string            `json:"reverses_id,omitempty"`
	Links          map[string]string `json:"_links,omitempty"`
	Actions        []MoneyAction     `json:"_actions,omitempty"`
}

// createTransactionRequest is the JSON body shape for POST.
//
// Either CounterpartyID or CounterpartyName must be present. Name
// route is the LLM seed flow (S23): caller doesn't track UUIDs, just
// posts "Salary" or "Indomaret" and lets the server resolve case-
// insensitively, creating the row inline if needed (spec §4.4).
//
// account_id is no longer accepted — the funding account is the Pos's
// current account_id (spec §4.2). Sending one is rejected as an unknown
// field by decodeJSONStrict.
type createTransactionRequest struct {
	Type             string `json:"type"`
	EffectiveDate    string `json:"effective_date"` // YYYY-MM-DD
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
	posID, err := uuid.Parse(req.PosID)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "pos_id must be a valid UUID")
	}
	var counterpartyID *uuid.UUID
	if strings.TrimSpace(req.CounterpartyID) != "" {
		cpID, err := uuid.Parse(req.CounterpartyID)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "counterparty_id must be a valid UUID")
		}
		counterpartyID = &cpID
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	// Shared resolve → validate → insert path (transaction_service.go).
	// h.Auth.Users has been UUID-resolved at boot
	// (cmd/server.resolveUserIDs), so notification rows insert against
	// the real users.id values rather than the seeded slugs.
	today := handlerNow(h)
	result, violations, err := createMoneyTransaction(ctx, h.DB, h.Auth.Users, today, MoneyCreateInput{
		Type:             req.Type,
		EffectiveDate:    effDate,
		AccountAmount:    req.AccountAmount,
		PosID:            posID,
		PosAmount:        req.PosAmount,
		CounterpartyID:   counterpartyID,
		CounterpartyName: req.CounterpartyName,
		Note:             req.Note,
		Source:           notification.SourceAPI,
		CreatedBy:        nil, // spec §4.3: source=api leaves created_by null.
		IdempotencyKey:   req.IdempotencyKey,
	})
	if err != nil {
		if IsMoneyNotFound(err) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, err.Error())
		}
		c.Logger().Errorf("api txn: createMoneyTransaction: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to record transaction")
	}
	if len(violations) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, violations[0])
	}

	return c.JSON(http.StatusCreated, result)
}
