package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APICounterpartiesList implements GET /api/v1/counterparties.
//
// Returns a JSON array ordered by name_lower (case-insensitive).
// Optional `q=` query param returns prefix matches only (delegates
// to the existing SearchCounterparties; capped at 20 rows).
func (h *Handlers) APICounterpartiesList(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	q := dbq.New(h.DB)

	query := strings.TrimSpace(c.QueryParam("q"))
	var rows []dbq.Counterparty
	var err error
	if query != "" {
		rows, err = q.SearchCounterparties(ctx, query)
	} else {
		rows, err = q.ListCounterparties(ctx)
	}
	if err != nil {
		c.Logger().Errorf("api list counterparties: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to list counterparties")
	}

	out := make([]APICounterparty, 0, len(rows))
	for _, r := range rows {
		if !r.ID.Valid {
			continue
		}
		out = append(out, APICounterparty{
			ID:        uuid.UUID(r.ID.Bytes).String(),
			Name:      r.Name,
			CreatedAt: r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}
