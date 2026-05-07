package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/logic/pos"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// PosNewGet renders the "New Pos" form (spec §6.2 / S5–S15 setup path —
// before users can log transactions, the household needs at least one
// Pos to spend from). Auth-gated.
func (h *Handlers) PosNewGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	return c.Render(http.StatusOK, "pos_new", template.PosNewData{
		Title:       "New Pos",
		DisplayName: u.DisplayName,
		Accounts:    loadAccountOptions(c, h),
	})
}

// loadAccountOptions returns non-archived accounts for the form's
// dropdown. Returns nil on DB-not-configured or query error so the form
// still renders (the validation error path will surface the problem).
func loadAccountOptions(c echo.Context, h *Handlers) []template.AccountOption {
	if h.DB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	rows, err := dbq.New(h.DB).ListAccounts(ctx)
	if err != nil {
		c.Logger().Errorf("ListAccounts: %v", err)
		return nil
	}
	out := make([]template.AccountOption, 0, len(rows))
	for _, r := range rows {
		out = append(out, template.AccountOption{
			ID:   uuid.UUID(r.ID.Bytes).String(),
			Name: r.Name,
		})
	}
	return out
}

// PosNewPost handles form submission. Validates via logic/pos.Validate,
// inserts via dbq.CreatePos, then redirects to the new Pos's detail
// page. Validation failures re-render the form with the user's input
// and the list of error messages.
//
// Duplicate-name handling: the DB has a UNIQUE (name, currency)
// constraint; on conflict we surface it as a per-form error rather
// than a 500.
func (h *Handlers) PosNewPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	in := pos.CreateInput{
		Name:      c.FormValue("name"),
		Currency:  c.FormValue("currency"),
		AccountID: c.FormValue("account_id"),
	}
	rerender := func(errs []string) error {
		return c.Render(http.StatusOK, "pos_new", template.PosNewData{
			Title:       "New Pos",
			DisplayName: u.DisplayName,
			Name:        in.Name,
			Currency:    in.Currency,
			AccountID:   in.AccountID,
			Accounts:    loadAccountOptions(c, h),
			TargetRaw:   c.FormValue("target"),
			Errors:      errs,
		})
	}
	if rawTarget := strings.TrimSpace(c.FormValue("target")); rawTarget != "" {
		t, err := strconv.ParseInt(rawTarget, 10, 64)
		if err != nil {
			return rerender([]string{"Target must be a whole number (no decimals)."})
		}
		in.Target = t
		in.HasTarget = true
	}

	in = pos.Normalize(in)
	if errs := pos.Validate(in); len(errs) > 0 {
		return rerender(errs)
	}
	accountUUID, err := uuid.Parse(in.AccountID)
	if err != nil {
		return rerender([]string{"Account is invalid."})
	}

	if h.DB == nil {
		// No data layer wired — render the form with a friendly message
		// rather than 500. (This path is exercised by handler unit tests.)
		return rerender([]string{"Database is not configured. Set DATABASE_URL and restart."})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
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
		// 23505 = unique_violation; the (name, currency) UNIQUE caught a
		// duplicate. Surface it as a form error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return rerender([]string{"A Pos with that name and currency already exists."})
		}
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return rerender([]string{"Selected account no longer exists."})
		}
		c.Logger().Errorf("CreatePos: %v", err)
		return rerender([]string{"Couldn’t create the Pos right now. Try again in a moment."})
	}

	id := uuid.UUID(row.ID.Bytes).String()
	return c.Redirect(http.StatusSeeOther, "/pos/"+id)
}
