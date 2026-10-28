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
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APIPos is the JSON shape for one Pos in /api/v1 responses. Mirrors
// schema columns 1:1; pgtype is never on the wire.
//
// _links/_actions are HATEOAS-lite (same pattern as APITransaction):
// navigable hrefs against existing GETs only, so HTMX pages render as
// thin views of the same JSON the API serves.
type APIPos struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Currency  string            `json:"currency"`
	AccountID string            `json:"account_id"`
	Target    *int64            `json:"target"` // null when no budget target set
	Archived  bool              `json:"archived"`
	CreatedAt time.Time         `json:"created_at"`
	Links     map[string]string `json:"_links,omitempty"`
	Actions   []MoneyAction     `json:"_actions,omitempty"`
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
// constraints). Catches the (name, currency, account_id) UNIQUE constraint and
// surfaces it as 409 Conflict so the LLM caller can distinguish "I
// already created this" from "real DB error." Same name in a different
// account is allowed.
//
// Shared resolve → validate → insert path (pos_service.go) so the web
// form and the API stay behavior-identical by construction.
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
	accountUUID, err := uuid.Parse(strings.TrimSpace(req.AccountID))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "account_id must be a valid UUID")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	result, violations, err := createPos(ctx, h.DB, PosCreateInput{
		Name:      req.Name,
		Currency:  req.Currency,
		AccountID: accountUUID,
		Target:    req.Target,
	})
	if err != nil {
		if IsPosConflict(err) {
			return mw.WriteAPIError(c, http.StatusConflict,
				mw.APIErrorCodeConflict, err.Error())
		}
		if IsPosAccountNotFound(err) {
			// FK violation: account_id refers to a non-existent account.
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, err.Error())
		}
		c.Logger().Errorf("api create pos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to create pos")
	}
	if len(violations) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, violations[0])
	}
	return c.JSON(http.StatusCreated, result)
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
//   - 409: rename/move would collide with another (name, currency, account_id) triple.
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

	// Transport-only parsing: UUID shape is a 400 here so the service
	// only ever sees typed UUIDs.
	var accountID *uuid.UUID
	if req.AccountID != nil {
		parsed, err := uuid.Parse(strings.TrimSpace(*req.AccountID))
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "account_id must be a valid UUID")
		}
		accountID = &parsed
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	// Shared update path (pos_service.go): rename/target via
	// UpdatePosNameAndTarget, move via UpdatePosAccount (snapshot
	// semantics per spec §5.6), fully-populated row back on partial PATCH.
	in := PosUpdateInput{PosID: posID, Name: req.Name, AccountID: accountID}
	if req.Target != nil || req.ClearTarget {
		in.TargetSet = true
		// ClearTarget wins when both are set (matches pre-extract order).
		if req.ClearTarget {
			in.Target = nil
		} else {
			t := *req.Target
			in.Target = &t
		}
	}
	result, violations, err := updatePos(ctx, h.DB, in)
	if err != nil {
		if IsPosNotFound(err) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, err.Error())
		}
		if IsPosConflict(err) {
			return mw.WriteAPIError(c, http.StatusConflict,
				mw.APIErrorCodeConflict, err.Error())
		}
		if IsPosAccountNotFound(err) {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, err.Error())
		}
		c.Logger().Errorf("api update pos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to update pos")
	}
	if len(violations) > 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, violations[0])
	}

	return c.JSON(http.StatusOK, result)
}

// APIPosArchive implements DELETE /api/v1/pos/:id. Soft delete only —
// archived rows are preserved and excluded from default listings (spec
// §10.3 forbids hard delete on ledger-anchored entities).
//
// Shared archive path (pos_service.go) so the web button and the API
// stay behavior-identical by construction.
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
	if err := archivePos(ctx, h.DB, id); err != nil {
		if IsPosNotFound(err) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, err.Error())
		}
		c.Logger().Errorf("api archive pos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to archive pos")
	}
	return c.NoContent(http.StatusNoContent)
}

// posRowToAPI is the dbq.Po → APIPos converter shared by every Pos
// endpoint that emits a single row. Attaches HATEOAS-lite _links/
// _actions so API and HTMX clients share one navigable shape.
func posRowToAPI(row dbq.Po) APIPos {
	id := uuid.UUID(row.ID.Bytes).String()
	accountID := uuid.UUID(row.AccountID.Bytes).String()
	return APIPos{
		ID:        id,
		Name:      row.Name,
		Currency:  row.Currency,
		AccountID: accountID,
		Target:    row.Target,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt.Time,
		Links:     posLinks(id, accountID),
		Actions:   posActions(id),
	}
}
