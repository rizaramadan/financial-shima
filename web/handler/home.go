package handler

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/logic/balance"
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
			c.Logger().Errorf("home loadHomeData: %v", err)
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
//
// Account balances and Pos cash are computed by streaming all money_in /
// money_out rows through logic/balance.State. Reversal rows are stored as
// inverse-direction money rows in the DB so they fold naturally — no
// special-casing here. inter_pos rows are skipped (Phase-7 schema work
// adds the line-items table; without lines they don't affect balance).
func (h *Handlers) loadHomeData(ctx context.Context, data *template.HomeData) error {
	q := dbq.New(h.DB)

	accounts, err := q.ListAccounts(ctx)
	if err != nil {
		return err
	}

	pos, err := q.ListPos(ctx)
	if err != nil {
		return err
	}

	state, err := computeBalanceState(ctx, h)
	if err != nil {
		return err
	}

	for _, a := range accounts {
		idStr := uuid.UUID(a.ID.Bytes).String()
		data.Accounts = append(data.Accounts, template.AccountRow{
			Name:       a.Name,
			BalanceIDR: state.Accounts[idStr],
		})
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
		idStr := uuid.UUID(p.ID.Bytes).String()
		cash := state.Pos[balance.PosKey{PosID: idStr, Currency: p.Currency}]
		groups[curIdx].Items = append(groups[curIdx].Items, template.PosRow{
			Name:      p.Name,
			Cash:      cash,
			Target:    target,
			HasTarget: hasTarget,
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

// computeBalanceState folds every money_in/money_out row through the
// balance package to derive per-account and per-Pos running totals. Pure
// derivation — the spec stores no balance column (§4.2).
func computeBalanceState(ctx context.Context, h *Handlers) (*balance.State, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT t.type, t.account_id, t.account_amount,
		       t.pos_id, t.pos_amount, p.currency
		  FROM transactions t
		  JOIN pos p ON p.id = t.pos_id
		 WHERE t.type IN ('money_in', 'money_out')
		 ORDER BY t.effective_date, t.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	state := balance.New()
	for rows.Next() {
		var (
			txType         string
			accountID      uuid.UUID
			accountAmount  int64
			posID          uuid.UUID
			posAmount      int64
			posCurrency    string
		)
		if err := rows.Scan(&txType, &accountID, &accountAmount, &posID, &posAmount, &posCurrency); err != nil {
			return nil, err
		}
		var ev balance.Event
		switch txType {
		case "money_in":
			ev = balance.MoneyIn{
				AccountID: accountID.String(), AccountIDR: accountAmount,
				PosID: posID.String(), PosCurrency: posCurrency, PosAmount: posAmount,
			}
		case "money_out":
			ev = balance.MoneyOut{
				AccountID: accountID.String(), AccountIDR: accountAmount,
				PosID: posID.String(), PosCurrency: posCurrency, PosAmount: posAmount,
			}
		default:
			continue
		}
		if err := state.Apply(ev); err != nil {
			return nil, err
		}
	}
	return state, rows.Err()
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
