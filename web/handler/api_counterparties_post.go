package handler

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// counterpartyNameRegex enforces spec §4.4 (alphanumeric + space +
// underscore + hyphen). Schema CHECK enforces the same; surfacing a
// 400 here saves the round-trip.
var counterpartyNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)

// APICounterparty is the JSON shape for one counterparty.
type APICounterparty struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// createCounterpartyRequest is the JSON body shape for POST.
type createCounterpartyRequest struct {
	Name string `json:"name"`
}

// APICounterpartiesCreate implements POST /api/v1/counterparties.
//
// Idempotent by case-insensitive name: re-POSTing the same name (any
// casing) returns the existing row with the originally-recorded
// casing preserved. Naturally suits the LLM seed flow per spec §4.4.
//
// Returns 200 OK rather than 201 Created when the counterparty
// already existed; both responses carry the same JSON body.
func (h *Handlers) APICounterpartiesCreate(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			"FS-0032", mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	var req createCounterpartyRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			"FS-0033", mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			"FS-0034", mw.APIErrorCodeValidation, "name is required")
	}
	if !counterpartyNameRegex.MatchString(name) {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			"FS-0035", mw.APIErrorCodeValidation,
			"name must contain only letters, digits, spaces, underscore, or hyphen")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	row, err := q.GetOrCreateCounterparty(ctx, name)
	if err != nil {
		mw.LogError(c, "FS-0036", "api get-or-create counterparty: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			"FS-0036", mw.APIErrorCodeInternal, "failed to create counterparty")
	}
	return c.JSON(http.StatusCreated, APICounterparty{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		Name:      row.Name,
		CreatedAt: row.CreatedAt.Time,
	})
}
