package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
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

// updatePosRequest is the JSON body for PATCH /api/v1/pos/:id.
// Every field is optional (pointer-typed) — a PATCH body with only
// `account_id` set still works, matching the §5.6 single-purpose
// "move a Pos" use case while also supporting rename / target tweak.
//
// Currency is intentionally absent: changing it would re-bucket every
// past pos_amount and is the kind of balance-mutating UPDATE spec §10.3
// forbids. Operators rename via name+target, or archive and recreate.
type updatePosRequest struct {
	Name        *string `json:"name,omitempty"`
	Target      *int64  `json:"target,omitempty"` // explicit null clears the target
	ClearTarget bool    `json:"clear_target,omitempty"`
	AccountID   *string `json:"account_id,omitempty"`
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

// APIPosGet implements GET /api/v1/pos/:id (single-row read).
func (h *Handlers) APIPosGet(c echo.Context) error {
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
	row, err := dbq.New(h.DB).GetPos(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "pos not found")
		}
		c.Logger().Errorf("api get pos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load pos")
	}
	return c.JSON(http.StatusOK, posRowToAPI(row))
}

// APIPosUpdate implements PATCH /api/v1/pos/:id. Accepts any subset of
// {name, target, account_id}; fields omitted from the body are
// preserved.
//
// Reassigning account_id has snapshot semantics per spec §5.6 — every
// historical money_in / money_out re-attributes to the new Account on
// the next balance read. Renaming or changing target is a plain row
// edit that doesn't move money.
//
// Currency is not editable here: changing it would re-bucket past
// pos_amount values, which spec §10.3 forbids.
//
// Errors:
//   - 503: data layer not configured.
//   - 400: malformed JSON, invalid UUIDs, empty name, negative target.
//   - 404: pos not found, or new account_id does not exist.
//   - 409: rename would collide with another (name, currency) pair.
//   - 500: DB write error.
func (h *Handlers) APIPosUpdate(c echo.Context) error {
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
	var req updatePosRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	if req.Name == nil && req.Target == nil && !req.ClearTarget && req.AccountID == nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"PATCH body must set at least one of name, target, account_id")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	// Load the current row so we can preserve unchanged fields and
	// echo a fully-populated response on partial PATCH.
	current, err := q.GetPos(ctx, pgtype.UUID{Bytes: posID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "pos not found")
		}
		c.Logger().Errorf("api update pos: load: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load pos")
	}

	// Apply name/target update if requested.
	if req.Name != nil || req.Target != nil || req.ClearTarget {
		newName := current.Name
		if req.Name != nil {
			newName = strings.TrimSpace(*req.Name)
			if newName == "" {
				return mw.WriteAPIError(c, http.StatusBadRequest,
					mw.APIErrorCodeValidation, "name must not be empty")
			}
		}
		var newTarget *int64
		switch {
		case req.ClearTarget:
			newTarget = nil
		case req.Target != nil:
			t := *req.Target
			if t < 0 {
				return mw.WriteAPIError(c, http.StatusBadRequest,
					mw.APIErrorCodeValidation, "target must be zero or positive")
			}
			newTarget = &t
		default:
			newTarget = current.Target
		}
		row, err := q.UpdatePosNameAndTarget(ctx, dbq.UpdatePosNameAndTargetParams{
			ID:     pgtype.UUID{Bytes: posID, Valid: true},
			Name:   newName,
			Target: newTarget,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return mw.WriteAPIError(c, http.StatusConflict,
					mw.APIErrorCodeConflict,
					"a Pos with that name and currency already exists")
			}
			c.Logger().Errorf("api update pos: name/target: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to update pos")
		}
		current = row
	}

	// Apply account update if requested.
	if req.AccountID != nil {
		accountUUID, err := uuid.Parse(*req.AccountID)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "account_id must be a valid UUID")
		}
		row, err := q.UpdatePosAccount(ctx, dbq.UpdatePosAccountParams{
			ID:        pgtype.UUID{Bytes: posID, Valid: true},
			AccountID: pgtype.UUID{Bytes: accountUUID, Valid: true},
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return mw.WriteAPIError(c, http.StatusBadRequest,
					mw.APIErrorCodeValidation,
					"account_id does not refer to an existing account")
			}
			c.Logger().Errorf("api update pos: account: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to update pos account")
		}
		current = row
	}

	return c.JSON(http.StatusOK, posRowToAPI(current))
}

// APIPosArchive implements DELETE /api/v1/pos/:id. Soft delete only —
// archived rows are preserved and excluded from default listings (spec
// §10.3 forbids hard delete on ledger-anchored entities).
func (h *Handlers) APIPosArchive(c echo.Context) error {
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
	q := dbq.New(h.DB)
	if _, err := q.GetPos(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "pos not found")
		}
		c.Logger().Errorf("api archive pos: lookup: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to archive pos")
	}
	if err := q.ArchivePos(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
		c.Logger().Errorf("api archive pos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to archive pos")
	}
	return c.NoContent(http.StatusNoContent)
}

// posRowToAPI is the dbq.Po → APIPos converter shared by every Pos
// endpoint that emits a single row.
func posRowToAPI(row dbq.Po) APIPos {
	return APIPos{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		Name:      row.Name,
		Currency:  row.Currency,
		AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		Target:    row.Target,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt.Time,
	}
}
