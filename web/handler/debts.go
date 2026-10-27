package handler

import (
	"context"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/logic/obligation"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// DebtsGet renders the household debt summary ("Sisa Hutang"): every
// open inter-Pos obligation (cleared_at IS NULL) with its outstanding
// remainder, plus per-currency totals. Same ListOpenObligations rows
// the /api/v1/debts endpoint serves, rendered as HTML.
func (h *Handlers) DebtsGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	data := template.DebtsData{
		Title:       "Sisa Hutang",
		DisplayName: u.DisplayName,
	}

	if h.DB == nil {
		return c.Render(http.StatusOK, "debts", data)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	rows, err := dbq.New(h.DB).ListOpenObligations(ctx)
	if err != nil {
		c.Logger().Errorf("ListOpenObligations: %v", err)
		data.LoadError = true
		data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
		return c.Render(http.StatusOK, "debts", data)
	}

	totals := map[string]int64{}
	for _, r := range rows {
		out := obligation.Outstanding(obligation.Obligation{
			Owed: r.AmountOwed, Repaid: r.AmountRepaid,
		})
		data.Items = append(data.Items, template.DebtRow{
			ID:          uuid.UUID(r.ID.Bytes).String(),
			CreditorPos: r.CreditorPosName,
			DebtorPos:   r.DebtorPosName,
			Currency:    r.Currency,
			Owed:        r.AmountOwed,
			Repaid:      r.AmountRepaid,
			Outstanding: out,
			CreatedAt:   r.CreatedAt.Time,
		})
		totals[r.Currency] += out
	}
	for ccy, out := range totals {
		data.Totals = append(data.Totals, template.DebtTotal{
			Currency: ccy, Outstanding: out,
		})
	}
	sort.Slice(data.Totals, func(i, j int) bool {
		return data.Totals[i].Currency < data.Totals[j].Currency
	})

	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	return c.Render(http.StatusOK, "debts", data)
}
