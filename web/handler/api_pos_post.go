package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	logicpos "github.com/rizaramadan/financial-shima/logic/pos"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APIPos is the JSON shape for one Pos in /api/v1 responses. Mirrors
// schema columns 1:1; pgtype is never on the wire.
type APIPos struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Currency  string    `json:"currency"`
	AccountID string    `json:"account_id"`
	Target    *int64    `json:"target"` // null when no budget target set
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
}

// createPosRequest is the JSON body shape for POST /api/v1/pos.
//
// AccountID is required (spec §4.2): every Pos lives in exactly one
// IDR account, including non-IDR Pos (the IDR account that funds it).
type createPosRequest struct {
	Name      string `json:"name"`
	Currency  string `json:"currency"`
	AccountID string `json:"account_id"`
	Target    *int64 `json:"target"` // pointer so JSON null / omitted = no target
}

// updatePosAccountRequest is the JSON body for PATCH /api/v1/pos/:id.
// Per spec §5.6, changing pos.account_id is the canonical "move a Pos"
// op; the change has snapshot semantics (past balances re-attribute).
type updatePosAccountRequest struct {
	AccountID string `json:"account_id"`
}

// APIPosCreate implements POST /api/v1/pos per spec §7.2 / S23.
//
// Validates per logic/pos.Validate (which mirrors the schema CHECK
// constraints). Catches the (name, currency) UNIQUE constraint and
// surfaces it as 409 Conflict so the LLM caller can distinguish "I
// already created this" from "real DB error."
func (h *Handlers) APIPosCreate(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	var req createPosRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	in := logicpos.CreateInput{
		Name:      req.Name,
		Currency:  req.Currency,
		AccountID: req.AccountID,
	}
	if req.Target != nil {
		in.Target = *req.Target
		in.HasTarget = true
	}
	in = logicpos.Normalize(in)
	if errs := logicpos.Validate(in); len(errs) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, errs[0])
	}
	accountUUID, err := uuid.Parse(in.AccountID)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "account_id must be a valid UUID")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	params := dbq.CreatePosParams{
		Name:      in.Name,
		Currency:  in.Currency,
		AccountID: pgtype.UUID{Bytes: accountUUID, Valid: true},
	}
	if in.HasTarget {
		t := in.Target
		params.Target = &t
	}
	q := dbq.New(h.DB)
	row, err := q.CreatePos(ctx, params)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return mw.WriteAPIError(c, http.StatusConflict,
				mw.APIErrorCodeConflict,
				"a Pos with that name and currency already exists")
		}
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			// FK violation: account_id refers to a non-existent account.
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation,
				"account_id does not refer to an existing account")
		}
		c.Logger().Errorf("api create pos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to create pos")
	}
	return c.JSON(http.StatusCreated, APIPos{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		Name:      row.Name,
		Currency:  row.Currency,
		AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		Target:    row.Target,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt.Time,
	})
}

// APIPosUpdateAccount implements PATCH /api/v1/pos/:id per spec §5.6 /
// §7.2. Reassigns the Pos to a different Account; per the snapshot
// semantics in §5.6, every historical money_in / money_out for the
// Pos is re-attributed to the new Account on the next balance read.
//
// Errors:
//   - 503: data layer not configured.
//   - 400: malformed JSON, invalid UUIDs, or missing account_id.
//   - 404: pos not found, or new account_id does not exist.
//   - 500: DB write error.
func (h *Handlers) APIPosUpdateAccount(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	posID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "pos id must be a valid UUID")
	}
	var req updatePosAccountRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	accountUUID, err := uuid.Parse(req.AccountID)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "account_id must be a valid UUID")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	row, err := q.UpdatePosAccount(ctx, dbq.UpdatePosAccountParams{
		ID:        pgtype.UUID{Bytes: posID, Valid: true},
		AccountID: pgtype.UUID{Bytes: accountUUID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "pos not found")
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation,
				"account_id does not refer to an existing account")
		}
		c.Logger().Errorf("api update pos account: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to update pos account")
	}
	return c.JSON(http.StatusOK, APIPos{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		Name:      row.Name,
		Currency:  row.Currency,
		AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		Target:    row.Target,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt.Time,
	})
}
