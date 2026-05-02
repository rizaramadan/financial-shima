package handler

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APIAccount is the JSON shape for one account in `/api/v1` responses.
// All fields are present on every row; absent timestamps serialize as
// the zero time per encoding/json defaults — accounts always have
// `created_at` set, so this never matters in practice.
type APIAccount struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
}

// APIAccountsList implements `GET /api/v1/accounts` per spec §7.2.
//
// Returns a JSON array of non-archived accounts ordered by name. Empty
// result is `[]`, never `null`. Auth is gated by [middleware.APIKey]
// upstream; this handler assumes a valid key.
//
// When `h.DB` is nil (dev boot without `DATABASE_URL`), returns `[]` —
// matching the rest of the project's nil-DB tolerance so the binary
// boots without a Postgres on disk.
func (h *Handlers) APIAccountsList(c echo.Context) error {
	if h.DB == nil {
		return c.JSON(http.StatusOK, []APIAccount{})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	rows, err := dbq.New(h.DB).ListAccounts(ctx)
	if err != nil {
		log.Printf("api: list accounts: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError, "internal_error", "failed to list accounts")
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
