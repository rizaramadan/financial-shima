package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	logicpos "github.com/rizaramadan/financial-shima/logic/pos"
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

// PosNewPost handles form submission. Validates via logic/pos.Validate
// (inside the shared createPos path), inserts via dbq.CreatePos, then
// redirects to the new Pos's detail page. Validation failures
// re-render the form with the user's input and the list of error
// messages.
//
// Duplicate-name handling: the DB has a UNIQUE (name, currency)
// constraint; on conflict we surface it as a per-form error rather
// than a 500. Shared create path (pos_service.go) so the web form and
// the API stay behavior-identical by construction.
func (h *Handlers) PosNewPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	rawName := c.FormValue("name")
	rawCurrency := c.FormValue("currency")
	rawAccountID := c.FormValue("account_id")
	rawTarget := strings.TrimSpace(c.FormValue("target"))
	// Echo normalized values so the re-render matches the previous
	// trimming/lowercasing behavior (Normalize is idempotent; the
	// service normalizes again before persistence).
	dispName := strings.TrimSpace(rawName)
	dispCurrency := strings.ToLower(strings.TrimSpace(rawCurrency))
	dispAccountID := strings.TrimSpace(rawAccountID)
	rerender := func(errs []string) error {
		return c.Render(http.StatusOK, "pos_new", template.PosNewData{
			Title:       "New Pos",
			DisplayName: u.DisplayName,
			Name:        dispName,
			Currency:    dispCurrency,
			AccountID:   dispAccountID,
			Accounts:    loadAccountOptions(c, h),
			TargetRaw:   c.FormValue("target"),
			Errors:      errs,
		})
	}
	var target *int64
	if rawTarget != "" {
		t, err := strconv.ParseInt(rawTarget, 10, 64)
		if err != nil {
			return rerender([]string{"Target must be a whole number (no decimals)."})
		}
		target = &t
	}

	// Transport-order-preserving pre-check: field validation (via
	// logic/pos, same messages as before) runs before the account UUID
	// parse so combined bad-name + bad-account input still reports the
	// name error first. The service re-validates after parsing; the
	// second pass always succeeds here.
	preIn := logicpos.Normalize(logicpos.CreateInput{
		Name:      rawName,
		Currency:  rawCurrency,
		AccountID: dispAccountID,
		Target:    derefTarget(target),
		HasTarget: target != nil,
	})
	if errs := logicpos.Validate(preIn); len(errs) > 0 {
		return rerender(errs)
	}

	accountUUID, err := uuid.Parse(dispAccountID)
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

	// Shared create path (pos_service.go): normalize → validate via
	// logic/pos → insert. Transport-only parsing (target number,
	// account UUID) stays here so form wording never changes.
	result, violations, err := createPos(ctx, h.DB, PosCreateInput{
		Name:      rawName,
		Currency:  rawCurrency,
		AccountID: accountUUID,
		Target:    target,
	})
	if err != nil {
		if IsPosConflict(err) {
			return rerender([]string{"A Pos with that name and currency already exists."})
		}
		if IsPosAccountNotFound(err) {
			return rerender([]string{"Selected account no longer exists."})
		}
		c.Logger().Errorf("CreatePos: %v", err)
		return rerender([]string{"Couldn’t create the Pos right now. Try again in a moment."})
	}
	if len(violations) > 0 {
		return rerender(violations)
	}

	return c.Redirect(http.StatusSeeOther, "/pos/"+result.ID)
}
