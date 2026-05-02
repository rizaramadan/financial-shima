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
// into the web/API surface; until then, balance columns render as em-dashes
// so users don't read a misleading "0".
//
// On a DB error, the page still renders 200 with HomeData.LoadError=true so
// the user sees a transient-failure message rather than a "seed data missing"
// placeholder. The underlying error is logged via c.Logger().
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
			c.Logger().Errorf("[FS-0200] home loadHomeData: %v", err)
			data.LoadError = true
		}
		// Bell badge is server-rendered on every page nav (option 3 —
		// no client-side polling, no third-party JS). One UnreadCount
		// query per home view; the value lives in HomeData.UnreadCount
		// and the layout reads it.
		data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	}

	return c.Render(http.StatusOK, "home", data)
}

// loadHomeData populates HomeData.Accounts and HomeData.PosByCurrency from
// the live store. ListPos already orders rows by (currency, name), so a
// single linear pass groups them — no in-memory sort needed. IDR is pinned
// first because it's the operator's primary unit; remaining currencies sort
// alphabetically.
func (h *Handlers) loadHomeData(ctx context.Context, data *template.HomeData) error {
	q := dbq.New(h.DB)

	// Per-account / per-pos balance maps. A failure here logs but does not
	// abort the page render — accounts / pos still list with zeros.
	accountBal := map[[16]byte]int64{}
	if rows, err := q.SumAccountBalances(ctx); err == nil {
		for _, r := range rows {
			accountBal[r.ID.Bytes] = r.Balance
		}
	} else {
		return err
	}
	posBal := map[[16]byte]int64{}
	if rows, err := q.SumPosCashBalances(ctx); err == nil {
		for _, r := range rows {
			posBal[r.ID.Bytes] = r.Balance
		}
	} else {
		return err
	}

	accounts, err := q.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		data.Accounts = append(data.Accounts, template.AccountRow{
			Name:       a.Name,
			BalanceIDR: accountBal[a.ID.Bytes],
		})
	}

	pos, err := q.ListPos(ctx)
	if err != nil {
		return err
	}
	// SQL returns rows sorted by (currency, name); collect groups in
	// encounter order, then move IDR to the front.
	var groups []template.PosCurrencyGroup
	curIdx := -1
	for _, p := range pos {
		if curIdx == -1 || groups[curIdx].Currency != p.Currency {
			groups = append(groups, template.PosCurrencyGroup{Currency: p.Currency})
			curIdx = len(groups) - 1
		}
		var target int64
		hasTarget := false
		if p.Target != nil {
			target = *p.Target
			hasTarget = true
		}
		groups[curIdx].Items = append(groups[curIdx].Items, template.PosRow{
			Name: p.Name, Cash: posBal[p.ID.Bytes],
			Target: target, HasTarget: hasTarget,
		})
	}
	// Pin IDR first; alpha for the rest. (Stable so IDR's position is
	// deterministic when present.)
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Currency == "idr" {
			return true
		}
		if groups[j].Currency == "idr" {
			return false
		}
		return groups[i].Currency < groups[j].Currency
	})
	data.PosByCurrency = groups
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
