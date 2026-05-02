package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
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

// listTimeout caps every `/api/v1` list query. 5s is generous for a
// small household-scale dataset; tighter than the default echo timeout.
const listTimeout = 5 * time.Second

// APIAccountsList implements `GET /api/v1/accounts` per spec §7.2.
//
// Returns a JSON array of accounts ordered by name. Empty result is
// `[]`, never `null`. Auth is gated by [middleware.APIKey] upstream;
// this handler assumes a valid key.
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
		out = append(out, APIAccount{
			ID:        uuid.UUID(r.ID.Bytes).String(),
			Name:      r.Name,
			Archived:  r.Archived,
			CreatedAt: r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}
