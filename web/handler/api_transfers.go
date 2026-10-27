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

// transferLineRequest is one leg of the POST /api/v1/transfers body.
// amount is cents (int64); the sign comes from direction.
type transferLineRequest struct {
	PosID     string `json:"pos_id"`
	Direction string `json:"direction"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
}

// createTransferRequest is the JSON body shape for POST /api/v1/transfers.
type createTransferRequest struct {
	Mode           string                `json:"mode"`
	Lines          []transferLineRequest `json:"lines"`
	EffectiveDate  string                `json:"effective_date"` // YYYY-MM-DD
	IdempotencyKey string                `json:"idempotency_key"`
	Note           string                `json:"note,omitempty"`
}

// APITransfersCreate implements POST /api/v1/transfers per spec
// §4.3 / §7.2: an atomic inter_pos transfer.
//
// Flow:
//  1. Parse JSON, fail-fast on shape errors (400 validation_failed).
//  2. Resolve every leg's pos by id (404 not_found if missing).
//  3. Run logic/transaction.ValidateInterPos (400 on first violation;
//     per-currency Σ(out)==Σ(in), §10.6).
//  4. Hand off to dependencies/ledger.Service.InsertInterPos which
//     atomically writes the header + legs + (borrow mode only)
//     pos_obligation rows via logic/obligation.GenerateBorrowObligations
//     + notifications in one DB tx (spec §10.8).
//  5. Return 201 Created with the transfer JSON. Idempotent
//     re-submission of the same idempotency_key with the same payload
//     returns the same row with was_inserted=false; the same key with
//     a different payload is a 409 conflict.
//
// Errors:
//   - 503 service_unavailable: DB unwired.
//   - 400 validation_failed: malformed JSON/shape, failed domain
//     validation, or multi-currency borrow.
//   - 404 not_found: unknown pos_id.
//   - 409 conflict: idempotency_key owned by another row.
//   - 500 internal_error: DB write failure.
func (h *Handlers) APITransfersCreate(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	var req createTransferRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}

	effDate, err := time.Parse("2006-01-02", strings.TrimSpace(req.EffectiveDate))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"effective_date must be YYYY-MM-DD: "+err.Error())
	}
	lines := make([]TransferLineInput, 0, len(req.Lines))
	for i, l := range req.Lines {
		posID, err := uuid.Parse(strings.TrimSpace(l.PosID))
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation,
				"lines["+itoaSimple(i)+"].pos_id must be a valid UUID")
		}
		lines = append(lines, TransferLineInput{
			PosID:     posID,
			Direction: strings.TrimSpace(l.Direction),
			Amount:    l.Amount,
			Currency:  strings.ToLower(strings.TrimSpace(l.Currency)),
		})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	// h.Auth.Users has been UUID-resolved at boot
	// (cmd/server.resolveUserIDs), so notification rows insert against
	// the real users.id values rather than the seeded slugs.
	today := handlerNow(h)
	result, violations, err := createTransfer(ctx, h.DB, h.Auth.Users, today, TransferCreateInput{
		Mode:           strings.TrimSpace(req.Mode),
		EffectiveDate:  effDate,
		Lines:          lines,
		Note:           req.Note,
		Source:         notification.SourceAPI,
		CreatedBy:      nil, // spec §4.3: source=api leaves created_by null.
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
	})
	if err != nil {
		switch {
		case IsMoneyNotFound(err):
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, err.Error())
		case IsTransferConflict(err):
			return mw.WriteAPIError(c, http.StatusConflict,
				mw.APIErrorCodeConflict, err.Error())
		}
		c.Logger().Errorf("api transfer: createTransfer: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to record transfer")
	}
	if len(violations) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, violations[0])
	}

	return c.JSON(http.StatusCreated, result)
}
