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
	})
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
		Name:     c.FormValue("name"),
		Currency: c.FormValue("currency"),
	}
	if rawTarget := strings.TrimSpace(c.FormValue("target")); rawTarget != "" {
		t, err := strconv.ParseInt(rawTarget, 10, 64)
		if err != nil {
			return c.Render(http.StatusOK, "pos_new", template.PosNewData{
				Title:       "New Pos",
				DisplayName: u.DisplayName,
				Name:        in.Name,
				Currency:    in.Currency,
				TargetRaw:   rawTarget,
				Errors:      []string{"Target must be a whole number (no decimals)."},
			})
		}
		in.Target = t
		in.HasTarget = true
	}

	in = pos.Normalize(in)
	if errs := pos.Validate(in); len(errs) > 0 {
		return c.Render(http.StatusOK, "pos_new", template.PosNewData{
			Title:       "New Pos",
			DisplayName: u.DisplayName,
			Name:        in.Name,
			Currency:    in.Currency,
			TargetRaw:   c.FormValue("target"),
			Errors:      errs,
		})
	}

	if h.DB == nil {
		// No data layer wired — render the form with a friendly message
		// rather than 500. (This path is exercised by handler unit tests.)
		return c.Render(http.StatusOK, "pos_new", template.PosNewData{
			Title:       "New Pos",
			DisplayName: u.DisplayName,
			Name:        in.Name,
			Currency:    in.Currency,
			TargetRaw:   c.FormValue("target"),
			Errors:      []string{"Database is not configured. Set DATABASE_URL and restart."},
		})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()

	params := dbq.CreatePosParams{Name: in.Name, Currency: in.Currency}
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
			return c.Render(http.StatusOK, "pos_new", template.PosNewData{
				Title:       "New Pos",
				DisplayName: u.DisplayName,
				Name:        in.Name,
				Currency:    in.Currency,
				TargetRaw:   c.FormValue("target"),
				Errors:      []string{"A Pos with that name and currency already exists."},
			})
		}
		c.Logger().Errorf("[FS-0220] CreatePos: %v", err)
		return c.Render(http.StatusOK, "pos_new", template.PosNewData{
			Title:       "New Pos",
			DisplayName: u.DisplayName,
			Name:        in.Name,
			Currency:    in.Currency,
			TargetRaw:   c.FormValue("target"),
			Errors:      []string{"Couldn’t create the Pos right now. Try again in a moment."},
		})
	}

	id := uuid.UUID(row.ID.Bytes).String()
	return c.Redirect(http.StatusSeeOther, "/pos/"+id)
}
