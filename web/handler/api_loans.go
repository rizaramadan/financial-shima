package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// createLoanRequest is the JSON body shape for POST /api/v1/loans.
// Amount is whole rupiah (int64, never float64) — loans are IDR-only
// in v1, matching the web setup form's parseRupiah.
type createLoanRequest struct {
	Name         string `json:"name"`
	BorrowerName string `json:"borrower_name"`
	Amount       int64  `json:"amount"`
	AccountID    string `json:"account_id"`
	FundedFrom   string `json:"funded_from"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

// createLoanPaymentRequest is the JSON body shape for
// POST /api/v1/loans/:id/payments. EffectiveDate is YYYY-MM-DD.
type createLoanPaymentRequest struct {
	PayerName     string `json:"payer_name"`
	Amount        int64  `json:"amount"`
	EffectiveDate string `json:"effective_date"`
	Note          string `json:"note,omitempty"`
}

// rejectLoanRequest is the JSON body shape for
// POST /api/v1/loan-submissions/:sid/reject. The whole body is
// optional — an empty body rejects without a reason.
type rejectLoanRequest struct {
	Reason string `json:"reason,omitempty"`
}

// APILoanCreate implements POST /api/v1/loans: the family setup —
// Pos (target = amount, is_loan) + funding money_in + disbursing
// money_out (net balance 0) + borrower login — through the shared
// setupLoan path so the API and the web form stay
// behavior-identical by construction.
//
// Errors:
//   - 503 service_unavailable: DB unwired.
//   - 400 validation_failed: malformed JSON/shape, failed domain
//     validation, unknown account_id shape.
//   - 404 not_found: account_id refers to no account.
//   - 409 conflict: borrower username taken, duplicate loan name.
//   - 500 internal_error: DB write failure.
func (h *Handlers) APILoanCreate(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	var req createLoanRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	accountID, err := uuid.Parse(strings.TrimSpace(req.AccountID))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "account_id must be a valid UUID")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 8*time.Second)
	defer cancel()
	result, violations, err := setupLoan(ctx, h.DB, h.Auth.Users, LoanSetupInput{
		Name:         req.Name,
		BorrowerName: req.BorrowerName,
		Amount:       req.Amount,
		AccountID:    accountID,
		FundedFrom:   req.FundedFrom,
		Username:     req.Username,
		Password:     req.Password,
		Now:          handlerNow(h),
	})
	if err != nil {
		if IsLoanConflict(err) {
			return mw.WriteAPIError(c, http.StatusConflict,
				mw.APIErrorCodeConflict, err.Error())
		}
		if IsLoanNotFound(err) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, err.Error())
		}
		c.Logger().Errorf("api create loan: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to set up loan")
	}
	if len(violations) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, violations[0])
	}
	return c.JSON(http.StatusCreated, result)
}

// APILoanGet implements GET /api/v1/loans/:id: outstanding balance,
// repayment progress, and submission history for one loan.
//
// Errors:
//   - 503 service_unavailable: DB unwired.
//   - 400 validation_failed: id is not a UUID.
//   - 404 not_found: unknown id, or a pos that is not a loan.
//   - 500 internal_error: DB read failure.
func (h *Handlers) APILoanGet(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "id must be a valid UUID")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	result, err := getLoanDetail(ctx, h.DB, id)
	if err != nil {
		if IsLoanNotFound(err) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, err.Error())
		}
		c.Logger().Errorf("api get loan: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load loan")
	}
	return c.JSON(http.StatusOK, result)
}

// APILoanPaymentCreate implements POST /api/v1/loans/:id/payments:
// records a repayment submission (status=pending). Pending rows move
// no money — the balance moves on approve.
//
// Errors:
//   - 503 service_unavailable: DB unwired.
//   - 400 validation_failed: malformed JSON/shape, bad id, bad fields.
//   - 404 not_found: unknown id, or a pos that is not a loan.
//   - 500 internal_error: DB write failure.
func (h *Handlers) APILoanPaymentCreate(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	posID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "id must be a valid UUID")
	}
	var req createLoanPaymentRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	effDate, err := time.Parse("2006-01-02", strings.TrimSpace(req.EffectiveDate))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "effective_date must be YYYY-MM-DD")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	result, violations, err := submitLoanPayment(ctx, h.DB, LoanPaymentInput{
		PosID:         posID,
		PayerName:     req.PayerName,
		Amount:        req.Amount,
		EffectiveDate: effDate,
		Note:          req.Note,
	})
	if err != nil {
		if IsLoanNotFound(err) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, err.Error())
		}
		c.Logger().Errorf("api loan payment: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to submit payment")
	}
	if len(violations) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, violations[0])
	}
	return c.JSON(http.StatusCreated, result)
}

// APILoanPaymentCancel implements
// POST /api/v1/loans/:id/payments/:sid/cancel: cancels a pending
// submission. The :id scopes the cancel — a submission from another
// loan 404s, mirroring the borrower-session pos check.
//
// Errors:
//   - 503 service_unavailable: DB unwired.
//   - 400 validation_failed: malformed ids.
//   - 404 not_found: unknown submission, or one from another loan.
//   - 409 conflict: already decided.
//   - 500 internal_error: DB write failure.
func (h *Handlers) APILoanPaymentCancel(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	posID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "id must be a valid UUID")
	}
	subID, err := uuid.Parse(c.Param("sid"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "sid must be a valid UUID")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	result, err := cancelLoanPayment(ctx, h.DB, posID, subID)
	if err != nil {
		return loanDecisionError(c, err, "failed to cancel payment")
	}
	return c.JSON(http.StatusOK, result)
}

// APILoanApprove implements POST /api/v1/loan-submissions/:sid/approve:
// approves a pending repayment, appending the money_in (idempotent on
// the submission id) and linking it to the submission.
//
// Errors:
//   - 503 service_unavailable: DB unwired.
//   - 400 validation_failed: malformed sid.
//   - 404 not_found: unknown submission.
//   - 409 conflict: already decided.
//   - 500 internal_error: DB write failure.
func (h *Handlers) APILoanApprove(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	subID, err := uuid.Parse(c.Param("sid"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "sid must be a valid UUID")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 8*time.Second)
	defer cancel()
	result, err := approveLoanSubmission(ctx, h.DB, h.Auth.Users, subID)
	if err != nil {
		return loanDecisionError(c, err, "failed to approve payment")
	}
	return c.JSON(http.StatusOK, result)
}

// APILoanReject implements POST /api/v1/loan-submissions/:sid/reject:
// rejects a pending repayment with an optional reason. No ledger
// entry is written. The body itself is optional — an empty body
// rejects without a reason.
//
// Errors: same shape as APILoanApprove.
func (h *Handlers) APILoanReject(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	subID, err := uuid.Parse(c.Param("sid"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "sid must be a valid UUID")
	}
	var reason *string
	if raw, _ := io.ReadAll(c.Request().Body); len(bytes.TrimSpace(raw)) > 0 {
		var req rejectLoanRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
		}
		if dec.More() {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "invalid JSON body: trailing data after JSON value")
		}
		reason = ptrOrNil(strings.TrimSpace(req.Reason))
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	result, err := rejectLoanSubmission(ctx, h.DB, subID, reason)
	if err != nil {
		return loanDecisionError(c, err, "failed to reject payment")
	}
	return c.JSON(http.StatusOK, result)
}

// loanDecisionError maps the shared approve/reject/cancel sentinels to
// status codes so the three decision handlers stay one-liners.
func loanDecisionError(c echo.Context, err error, logMsg string) error {
	if IsLoanNotFound(err) {
		return mw.WriteAPIError(c, http.StatusNotFound,
			mw.APIErrorCodeNotFound, err.Error())
	}
	if IsLoanConflict(err) {
		return mw.WriteAPIError(c, http.StatusConflict,
			mw.APIErrorCodeConflict, err.Error())
	}
	c.Logger().Errorf("api loan decide: %s: %v", logMsg, err)
	return mw.WriteAPIError(c, http.StatusInternalServerError,
		mw.APIErrorCodeInternal, logMsg)
}
