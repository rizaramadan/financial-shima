package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// SearchGet renders /search?q= — the global search results page that backs
// the top-bar box. It matches accounts, pos, transactions, counterparties and
// income templates by case-insensitive substring. Each entity is capped in SQL
// (LIMIT 10) so no single type floods the page. A blank query renders the
// prompt without touching the DB; a per-entity query error is logged and that
// section is simply omitted rather than failing the whole page.
func (h *Handlers) SearchGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	q := strings.TrimSpace(c.QueryParam("q"))
	data := template.SearchData{
		Title:       "Search",
		DisplayName: u.DisplayName,
		Query:       q,
	}
	if h.DB == nil {
		data.Error = "Database is not configured."
		return c.Render(http.StatusOK, "search", data)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 4*time.Second)
	defer cancel()
	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)

	// Empty query → show the prompt only; skip the DB round-trips.
	if q == "" {
		return c.Render(http.StatusOK, "search", data)
	}

	qd := dbq.New(h.DB)

	if rows, err := qd.SearchAccounts(ctx, q); err != nil {
		c.Logger().Errorf("SearchGet SearchAccounts(%q): %v", q, err)
	} else {
		for _, r := range rows {
			data.Accounts = append(data.Accounts, template.SearchAccountRow{
				ID:   uuid.UUID(r.ID.Bytes).String(),
				Name: r.Name,
			})
		}
	}

	if rows, err := qd.SearchPos(ctx, q); err != nil {
		c.Logger().Errorf("SearchGet SearchPos(%q): %v", q, err)
	} else {
		for _, r := range rows {
			data.Pos = append(data.Pos, template.SearchPosRow{
				ID:       uuid.UUID(r.ID.Bytes).String(),
				Name:     r.Name,
				Currency: r.Currency,
			})
		}
	}

	if rows, err := qd.SearchTransactions(ctx, q); err != nil {
		c.Logger().Errorf("SearchGet SearchTransactions(%q): %v", q, err)
	} else {
		for _, r := range rows {
			data.Transactions = append(data.Transactions, flattenSearchTxn(r))
		}
	}

	if rows, err := qd.SearchIncomeTemplates(ctx, q); err != nil {
		c.Logger().Errorf("SearchGet SearchIncomeTemplates(%q): %v", q, err)
	} else {
		for _, r := range rows {
			data.IncomeTemplates = append(data.IncomeTemplates, template.SearchIncomeRow{
				ID:   uuid.UUID(r.ID.Bytes).String(),
				Name: r.Name,
			})
		}
	}

	if rows, err := qd.SearchCounterpartiesContains(ctx, q); err != nil {
		c.Logger().Errorf("SearchGet SearchCounterparties(%q): %v", q, err)
	} else {
		for _, r := range rows {
			data.Counterparties = append(data.Counterparties, template.SearchCounterpartyRow{
				Name: r.Name,
			})
		}
	}

	data.Total = len(data.Accounts) + len(data.Pos) + len(data.Transactions) +
		len(data.IncomeTemplates) + len(data.Counterparties)

	return c.Render(http.StatusOK, "search", data)
}

// flattenSearchTxn resolves the nullable joined columns of a transaction hit
// into the plain display fields the template expects. Amount/Currency stay raw
// so the template formats them via the shared money func.
func flattenSearchTxn(r dbq.SearchTransactionsRow) template.SearchTxnRow {
	row := template.SearchTxnRow{Type: string(r.Type)}
	if r.PosID.Valid {
		row.PosID = uuid.UUID(r.PosID.Bytes).String()
	}
	if r.PosName != nil {
		row.PosName = *r.PosName
	}
	if r.PosCurrency != nil {
		row.Currency = *r.PosCurrency
	}
	if r.PosAmount != nil {
		row.Amount = *r.PosAmount
	}
	if r.Note != nil {
		row.Label = strings.TrimSpace(*r.Note)
	}
	if r.CounterpartyName != nil {
		row.Counterparty = *r.CounterpartyName
	}
	// Headline is the note, falling back to the counterparty, then a stub.
	if row.Label == "" {
		row.Label = row.Counterparty
	}
	if row.Label == "" {
		row.Label = "(no note)"
	}
	if r.EffectiveDate.Valid {
		row.Date = r.EffectiveDate.Time.Format("2 Jan 2006")
	}
	return row
}
