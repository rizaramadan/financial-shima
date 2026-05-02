package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APIPosList implements GET /api/v1/pos per spec §7.2.
//
// Returns a JSON array ordered by (currency, name). Empty result is
// `[]`, never `null`. Auth gated by [middleware.APIKey] upstream.
//
// Query parameters:
//   - `include_archived=true` — include archived Pos. Default omits
//     them per the same convention as /api/v1/accounts.
//
// Errors mirror /api/v1/accounts:
//   - 503 service_unavailable — DB unwired.
//   - 500 internal_error — DB query failed; underlying logged.
func (h *Handlers) APIPosList(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()

	includeArchived, _ := strconv.ParseBool(c.QueryParam(includeArchivedQueryParam))

	q := dbq.New(h.DB)
	var rows []dbq.Po
	var err error
	if includeArchived {
		rows, err = q.ListPosIncludingArchived(ctx)
	} else {
		rows, err = q.ListPos(ctx)
	}
	if err != nil {
		c.Logger().Errorf("api list pos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to list pos")
	}

	out := make([]APIPos, 0, len(rows))
	for _, r := range rows {
		if !r.ID.Valid {
			c.Logger().Warnf("api list pos: row with invalid uuid skipped (name=%q)", r.Name)
			continue
		}
		out = append(out, APIPos{
			ID:        uuid.UUID(r.ID.Bytes).String(),
			Name:      r.Name,
			Currency:  r.Currency,
			Target:    r.Target,
			Archived:  r.Archived,
			CreatedAt: r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}
