package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// AccountsGet renders /accounts — the manage list view. Includes
// archived accounts so the operator can audit what's been retired.
func (h *Handlers) AccountsGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	data := template.AccountsData{
		Title:       "Accounts",
		DisplayName: u.DisplayName,
	}
	if flash, _ := c.Cookie("acct_flash"); flash != nil && flash.Value != "" {
		data.Flash = flash.Value
		c.SetCookie(&http.Cookie{Name: "acct_flash", Value: "", Path: "/", MaxAge: -1})
	}
	if errCookie, _ := c.Cookie("acct_error"); errCookie != nil && errCookie.Value != "" {
		data.Error = errCookie.Value
		c.SetCookie(&http.Cookie{Name: "acct_error", Value: "", Path: "/", MaxAge: -1})
	}

	if h.DB == nil {
		data.Error = "Database is not configured."
		return c.Render(http.StatusOK, "accounts", data)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	rows, err := dbq.New(h.DB).ListAccountsIncludingArchived(ctx)
	if err != nil {
		c.Logger().Errorf("AccountsGet ListAccounts: %v", err)
		data.Error = "Couldn’t load accounts."
		return c.Render(http.StatusOK, "accounts", data)
	}
	for _, r := range rows {
		data.Accounts = append(data.Accounts, template.AccountManageRow{
			ID:       uuid.UUID(r.ID.Bytes).String(),
			Name:     r.Name,
			Archived: r.Archived,
		})
	}
	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	return c.Render(http.StatusOK, "accounts", data)
}

// AccountNewGet renders the create-account form.
func (h *Handlers) AccountNewGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	return c.Render(http.StatusOK, "account_new", template.AccountNewData{
		Title:       "New Account",
		DisplayName: u.DisplayName,
	})
}

// AccountNewPost handles create-account submission. POSTs to /accounts
// (matches the LLM API surface) and redirects to /accounts on success.
func (h *Handlers) AccountNewPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	name := strings.TrimSpace(c.FormValue("name"))
	rerender := func(errs []string) error {
		return c.Render(http.StatusOK, "account_new", template.AccountNewData{
			Title:       "New Account",
			DisplayName: u.DisplayName,
			Name:        name,
			Errors:      errs,
		})
	}
	if name == "" {
		return rerender([]string{"Name is required."})
	}
	if h.DB == nil {
		return rerender([]string{"Database is not configured. Set DATABASE_URL and restart."})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	if _, err := dbq.New(h.DB).CreateAccount(ctx, name); err != nil {
		c.Logger().Errorf("CreateAccount: %v", err)
		return rerender([]string{"Couldn’t create the account right now. Try again."})
	}
	c.SetCookie(&http.Cookie{Name: "acct_flash", Value: "Account created.", Path: "/", MaxAge: 30})
	return c.Redirect(http.StatusSeeOther, "/accounts")
}

// AccountRenamePost handles rename submission from the manage list.
func (h *Handlers) AccountRenamePost(c echo.Context) error {
	if _, ok := mw.CurrentUser(c); !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		c.SetCookie(&http.Cookie{Name: "acct_error", Value: "Name is required.", Path: "/", MaxAge: 30})
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	if h.DB == nil {
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	if _, err := dbq.New(h.DB).UpdateAccountName(ctx, dbq.UpdateAccountNameParams{
		ID:   pgtype.UUID{Bytes: id, Valid: true},
		Name: name,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.SetCookie(&http.Cookie{Name: "acct_error", Value: "Account not found.", Path: "/", MaxAge: 30})
		} else {
			c.Logger().Errorf("UpdateAccountName: %v", err)
			c.SetCookie(&http.Cookie{Name: "acct_error", Value: "Couldn’t rename the account.", Path: "/", MaxAge: 30})
		}
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	c.SetCookie(&http.Cookie{Name: "acct_flash", Value: "Account renamed.", Path: "/", MaxAge: 30})
	return c.Redirect(http.StatusSeeOther, "/accounts")
}

// AccountDeletePost hard-deletes an account when no Pos references it.
// Distinct from AccountArchivePost: archive is the spec-required soft
// delete (§10.3 forbids hard-delete of `transactions`, but accounts
// have no inbound FK from transactions since 0005 — `pos.account_id`
// is the only reference, so a Pos-free account is safe to drop).
// The DB query is atomic (NOT EXISTS guard inside the DELETE), so no
// TOCTOU race against a concurrent Pos creation.
func (h *Handlers) AccountDeletePost(c echo.Context) error {
	if _, ok := mw.CurrentUser(c); !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	if h.DB == nil {
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	uid := pgtype.UUID{Bytes: id, Valid: true}
	rows, err := q.DeleteAccountIfUnused(ctx, uid)
	if err != nil {
		c.Logger().Errorf("DeleteAccountIfUnused: %v", err)
		c.SetCookie(&http.Cookie{Name: "acct_error", Value: "Couldn’t delete the account.", Path: "/", MaxAge: 30})
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	if rows == 0 {
		// Either the row never existed or a Pos still points at it.
		// One follow-up read tells us which, so we can pick the
		// right user-facing message.
		if _, err := q.GetAccount(ctx, uid); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				c.SetCookie(&http.Cookie{Name: "acct_error", Value: "Account not found.", Path: "/", MaxAge: 30})
			} else {
				c.Logger().Errorf("DeleteAccountIfUnused lookup: %v", err)
				c.SetCookie(&http.Cookie{Name: "acct_error", Value: "Couldn’t delete the account.", Path: "/", MaxAge: 30})
			}
			return c.Redirect(http.StatusSeeOther, "/accounts")
		}
		c.SetCookie(&http.Cookie{Name: "acct_error", Value: "Can’t delete: a Pos still points at this account. Reassign or archive its Pos first.", Path: "/", MaxAge: 30})
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	c.SetCookie(&http.Cookie{Name: "acct_flash", Value: "Account deleted.", Path: "/", MaxAge: 30})
	return c.Redirect(http.StatusSeeOther, "/accounts")
}

// AccountArchivePost handles archive submission. Per spec §10.3, this is
// a soft delete — the row is preserved.
func (h *Handlers) AccountArchivePost(c echo.Context) error {
	if _, ok := mw.CurrentUser(c); !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	if h.DB == nil {
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	if err := dbq.New(h.DB).ArchiveAccount(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
		c.Logger().Errorf("ArchiveAccount: %v", err)
		c.SetCookie(&http.Cookie{Name: "acct_error", Value: "Couldn’t archive the account.", Path: "/", MaxAge: 30})
		return c.Redirect(http.StatusSeeOther, "/accounts")
	}
	c.SetCookie(&http.Cookie{Name: "acct_flash", Value: "Account archived.", Path: "/", MaxAge: 30})
	return c.Redirect(http.StatusSeeOther, "/accounts")
}
