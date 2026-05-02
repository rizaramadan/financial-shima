package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// createAccountRequest is the JSON body shape for POST /api/v1/accounts.
//
// Per spec §4.1, an account is just an IDR purse — the only required
// field is `name`. Schema CHECK enforces non-empty trimmed name; we
// surface that as a 400 validation error rather than a raw DB error.
type createAccountRequest struct {
	Name string `json:"name"`
}

// APIAccountsCreate implements POST /api/v1/accounts per spec §7.2 / S23.
//
// The LLM (or any operator with the LLM_API_KEY) hits this once per
// account at initial seed time. There is no UNIQUE constraint on
// accounts.name in the schema (households sometimes have two BCA
// accounts), so dedup is the caller's responsibility.
//
// Errors:
//   - 503: data layer not configured (DB nil).
//   - 400: malformed JSON or empty name (validation_failed).
//   - 500: DB write error (internal_error).
//
// Success: 201 + JSON [APIAccount].
func (h *Handlers) APIAccountsCreate(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	var req createAccountRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "name is required")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	row, err := q.CreateAccount(ctx, name)
	if err != nil {
		c.Logger().Errorf("api create account: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to create account")
	}
	return c.JSON(http.StatusCreated, APIAccount{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		Name:      row.Name,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt.Time,
	})
}

// decodeJSONStrict reads exactly one JSON value, rejecting unknown
// fields and trailing garbage. Used by every /api/v1 POST handler so
// typos in the body don't silently get ignored.
func decodeJSONStrict(r io.Reader, dst interface{}) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after JSON value")
	}
	return nil
}
