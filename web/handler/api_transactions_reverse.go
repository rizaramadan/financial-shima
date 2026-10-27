package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// reverseTransactionRequest is the JSON body shape for POST
// /api/v1/transactions/:id/reverse. The key is optional: when absent
// or empty the server derives "reverse:<origID>" so a body-less retry
// is idempotent by default.
type reverseTransactionRequest struct {
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// APITransactionsReverse implements POST /api/v1/transactions/:id/reverse.
//
// Append-only correction (spec §10.3): inserts an offsetting money
// transaction with the flipped type, identical amounts, and
// reverses_id pointing at the original. The original row is never
// updated, so balances net to zero and the audit trail keeps both
// rows. GET /api/v1/transactions and the web list surface the pair
// via is_reversal / reverses_id (read-only — untouched here).
//
// Errors:
//   - 503 service_unavailable: DB unwired.
//   - 400 validation_failed: malformed id/JSON, or the original is not
//     a reversible money type.
//   - 404 not_found: unknown :id.
//   - 409 conflict: already reversed, or the idempotency_key belongs to
//     another row.
//   - 500 internal_error: DB write failure.
//
// Success: 201 + the reversal [APITransaction] with reverses_id and an
// "original" _links entry. Same-key re-submission returns the same row
// with was_inserted=false (mirrors POST /transactions).
func (h *Handlers) APITransactionsReverse(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	origID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "id must be a valid UUID")
	}

	// Body is optional (key derived when absent). Read raw so an empty
	// body decodes to zero value instead of an EOF 400; a non-empty
	// body still goes through the strict single-value decoder.
	var req reverseTransactionRequest
	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := decodeJSONStrict(bytes.NewReader(raw), &req); err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
		}
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	// h.Auth.Users has been UUID-resolved at boot
	// (cmd/server.resolveUserIDs), so notification rows insert against
	// the real users.id values rather than the seeded slugs.
	today := handlerNow(h)
	result, violations, err := reverseMoneyTransaction(ctx, h.DB, h.Auth.Users, today, origID, strings.TrimSpace(req.IdempotencyKey))
	if err != nil {
		switch {
		case IsMoneyNotFound(err):
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, err.Error())
		case IsReverseConflict(err):
			return mw.WriteAPIError(c, http.StatusConflict,
				mw.APIErrorCodeConflict, err.Error())
		}
		c.Logger().Errorf("api txn reverse: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to reverse transaction")
	}
	if len(violations) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, violations[0])
	}

	return c.JSON(http.StatusCreated, result)
}
