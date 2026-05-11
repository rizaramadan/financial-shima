package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APIAccount is the JSON shape for one account in `/api/v1` responses.
//
// Field names match spec §4.1 verbatim. `id` is the canonical
// hyphenated UUID string (RFC 4122). `created_at` is RFC 3339 with
// nanosecond precision, the encoding/json default for time.Time.
//
// The struct is hand-written rather than aliased from dbq.Account so
// pgtype.UUID / pgtype.Timestamptz never leak onto the wire format.
type APIAccount struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
}

// includeArchivedQueryParam is the query parameter that, when set to a
// truthy value (`true`/`1`/`yes`), opts the response into archived
// rows. Same name will be used by every `/api/v1` list endpoint with
// archived rows (`pos`, `counterparties` once it grows the column).
const includeArchivedQueryParam = "include_archived"

// listTimeout caps every `/api/v1` list query. Future list endpoints
// should reuse this constant.
const listTimeout = 5 * time.Second

// APIAccountsList implements `GET /api/v1/accounts` per spec §7.2.
//
// Returns a JSON array of accounts ordered by name. Empty result is
// `[]`, never `null`. Auth is gated by [middleware.APIKey] upstream;
// this handler assumes a valid key.
//
// TODO(sqlc-regen): the underlying SQL orders by `name` only, so order
// is unstable on duplicate names. Tiebreaker `ORDER BY name, id`
// pending sqlc regen alongside parallel in-flight changes.
//
// Query parameters:
//   - `include_archived=true` — include archived accounts in the list.
//     Default omits them per spec §4.1 (archived hidden from default
//     views).
//
// Pagination: `/api/v1/accounts` returns a bare array because the
// dataset is bounded (a household will not exceed dozens of accounts).
// Unbounded list endpoints (e.g. `/api/v1/transactions`) will instead
// return an envelope with a `next_cursor`. Bounded vs unbounded is the
// dividing line; do not change either retroactively.
//
// Errors:
//   - h.DB == nil          → 503 [APIErrorCodeServiceUnavailable]. The
//     binary boots without a Postgres on disk for HTML routes, but
//     `/api/v1` consumers must be told honestly that the server has
//     no data layer rather than fed an empty `[]` they cannot
//     distinguish from "no accounts."
//   - DB query error       → 500 [APIErrorCodeInternal]. The original
//     error is logged via `c.Logger()`; the body carries no internal
//     detail.
func (h *Handlers) APIAccountsList(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()

	// strconv.ParseBool returns an error on garbage input (e.g. "?include_archived=foo");
	// we swallow it intentionally and fall through to the default-omit behavior. Per
	// Postel's law for read endpoints: be liberal in what you accept. If a future round
	// wants strict 400-on-malformed, switch to APIErrorCodeValidation here.
	includeArchived, _ := strconv.ParseBool(c.QueryParam(includeArchivedQueryParam))

	q := dbq.New(h.DB)
	var rows []dbq.Account
	var err error
	if includeArchived {
		rows, err = q.ListAccountsIncludingArchived(ctx)
	} else {
		rows, err = q.ListAccounts(ctx)
	}
	if err != nil {
		c.Logger().Errorf("api list accounts: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal,
			"failed to list accounts")
	}

	out := make([]APIAccount, 0, len(rows))
	for _, r := range rows {
		// accounts.id is `uuid PRIMARY KEY` in the schema (NOT NULL by
		// definition), so r.ID.Valid is true for any row sqlc returns.
		// The defensive guard here exists so a future migration that
		// somehow surfaces an invalid uuid produces a logged warning
		// rather than a "00000000-0000-..." that quietly corrupts the
		// LLM caller's index.
		if !r.ID.Valid {
			c.Logger().Warnf("api list accounts: row with invalid uuid skipped (name=%q)", r.Name)
			continue
		}
		out = append(out, APIAccount{
			ID:        uuid.UUID(r.ID.Bytes).String(),
			Name:      r.Name,
			Archived:  r.Archived,
			CreatedAt: r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}

// APIAccountGet implements GET /api/v1/accounts/:id per spec §7.2.
// Single-row read; 404 when missing.
func (h *Handlers) APIAccountGet(c echo.Context) error {
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
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	row, err := dbq.New(h.DB).GetAccount(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "account not found")
		}
		c.Logger().Errorf("api get account: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load account")
	}
	return c.JSON(http.StatusOK, APIAccount{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		Name:      row.Name,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt.Time,
	})
}

// updateAccountRequest is the body for PATCH /api/v1/accounts/:id.
// Only `name` is mutable per spec §4.1 (no currency, no archived flag —
// archive uses DELETE).
type updateAccountRequest struct {
	Name string `json:"name"`
}

// APIAccountUpdate implements PATCH /api/v1/accounts/:id (rename).
func (h *Handlers) APIAccountUpdate(c echo.Context) error {
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
	var req updateAccountRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "name is required")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	row, err := dbq.New(h.DB).UpdateAccountName(ctx,
		dbq.UpdateAccountNameParams{
			ID:   pgtype.UUID{Bytes: id, Valid: true},
			Name: name,
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "account not found")
		}
		c.Logger().Errorf("api update account: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to update account")
	}
	return c.JSON(http.StatusOK, APIAccount{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		Name:      row.Name,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt.Time,
	})
}

// APIAccountArchive implements DELETE /api/v1/accounts/:id. Soft delete
// only — sets archived=true, preserves the row (spec §10.3 forbids
// hard delete of any ledger-anchored entity, and Pos still reference
// accounts via account_id). Returns 204 on success.
func (h *Handlers) APIAccountArchive(c echo.Context) error {
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
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	q := dbq.New(h.DB)
	// Confirm existence so we can return 404 vs 204 honestly.
	if _, err := q.GetAccount(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "account not found")
		}
		c.Logger().Errorf("api archive account: lookup: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to archive account")
	}
	if err := q.ArchiveAccount(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
		c.Logger().Errorf("api archive account: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to archive account")
	}
	return c.NoContent(http.StatusNoContent)
}
