package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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
	Target    *int64    `json:"target"` // null when no budget target set
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
}

// createPosRequest is the JSON body shape for POST /api/v1/pos.
type createPosRequest struct {
	Name     string `json:"name"`
	Currency string `json:"currency"`
	Target   *int64 `json:"target"` // pointer so JSON null / omitted = no target
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
		Name:     req.Name,
		Currency: req.Currency,
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

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	params := dbq.CreatePosParams{Name: in.Name, Currency: in.Currency}
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
		c.Logger().Errorf("api create pos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to create pos")
	}
	return c.JSON(http.StatusCreated, APIPos{
		ID:        uuid.UUID(row.ID.Bytes).String(),
		Name:      row.Name,
		Currency:  row.Currency,
		Target:    row.Target,
		Archived:  row.Archived,
		CreatedAt: row.CreatedAt.Time,
	})
}
