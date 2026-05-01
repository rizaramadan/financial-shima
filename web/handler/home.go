package handler

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// HomeGet renders the post-sign-in home page (spec §6.2: current balances).
//
// When DB is wired, accounts and pos are listed from the live store. Balance
// computation from the transaction stream lands once an insert path is wired
// into the web/API surface (until then, balances render as zero alongside
// targets so the structure is testable end-to-end).
func (h *Handlers) HomeGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	data := template.HomeData{
		Title:       "Home",
		DisplayName: u.DisplayName,
	}

	if h.DB != nil {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
		defer cancel()
		if err := h.loadHomeData(ctx, &data); err != nil {
			// DB error: log and render the placeholder rather than 500.
			c.Logger().Errorf("home loadHomeData: %v", err)
		}
	}

	return c.Render(http.StatusOK, "home", data)
}

// loadHomeData fills HomeData.Accounts and HomeData.PosByCurrency from the
// live store. Pos rows are grouped by currency and sorted alphabetically
// per spec §6.2 ("Pos — table of non-archived Pos grouped by currency").
func (h *Handlers) loadHomeData(ctx context.Context, data *template.HomeData) error {
	q := dbq.New(h.DB)

	accounts, err := q.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		data.Accounts = append(data.Accounts, template.AccountRow{Name: a.Name})
	}

	pos, err := q.ListPos(ctx)
	if err != nil {
		return err
	}
	groups := map[string][]template.PosRow{}
	for _, p := range pos {
		var target int64
		var hasTarget bool
		if p.Target != nil {
			target = *p.Target
			hasTarget = true
		}
		groups[p.Currency] = append(groups[p.Currency], template.PosRow{
			Name:      p.Name,
			Target:    target,
			HasTarget: hasTarget,
		})
	}
	currencies := make([]string, 0, len(groups))
	for c := range groups {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	for _, ccy := range currencies {
		data.PosByCurrency = append(data.PosByCurrency, template.PosCurrencyGroup{
			Currency: ccy,
			Items:    groups[ccy],
		})
	}
	return nil
}

// LogoutPost revokes the session server-side and clears the cookie.
func (h *Handlers) LogoutPost(c echo.Context) error {
	if cookie, err := c.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		h.Auth.Logout(cookie.Value)
	}
	c.SetCookie(&http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.Request().TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	return c.Redirect(http.StatusSeeOther, "/login")
}
