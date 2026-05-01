package handler

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

const (
	defaultSpendingMonths = 6
	defaultTopN           = 5
)

// SpendingGet renders the §6.4 spending view: rows are months, columns
// are the top-N Pos by money_out volume across the date range.
//
// Query params:
//
//	?from=YYYY-MM-DD  defaults to first day of (now - 5 months)
//	?to=YYYY-MM-DD    defaults to today
//	?n=…              defaults to 5
//
// The query returns one row per (pos, month). The handler:
//  1. Sums per pos across the range to find the top-N.
//  2. Pivots to a months-row × pos-column table, filling missing cells
//     with zero so the visual rhythm stays continuous.
func (h *Handlers) SpendingGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	now := time.Now()
	defaultFrom := time.Date(
		now.Year(), now.Month()-time.Month(defaultSpendingMonths-1), 1,
		0, 0, 0, 0, now.Location(),
	)
	from := defaultFrom
	to := now

	if v := c.QueryParam("from"); v != "" {
		if t, err := time.Parse(dateLayout, v); err == nil {
			from = t
		}
	}
	if v := c.QueryParam("to"); v != "" {
		if t, err := time.Parse(dateLayout, v); err == nil {
			to = t
		}
	}

	data := template.SpendingData{
		Title:       "Spending",
		DisplayName: u.DisplayName,
		From:        from.Format(dateLayout),
		To:          to.Format(dateLayout),
		TopN:        defaultTopN,
	}

	if h.DB == nil {
		return c.Render(http.StatusOK, "spending", data)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	rows, err := q.SumMoneyOutByPosMonth(ctx, dbq.SumMoneyOutByPosMonthParams{
		EffectiveDate:   pgtype.Date{Time: from, Valid: true},
		EffectiveDate_2: pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		c.Logger().Errorf("SumMoneyOutByPosMonth: %v", err)
		data.LoadError = true
		data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
		return c.Render(http.StatusOK, "spending", data)
	}

	type posKey struct {
		ID, Name, Currency string
	}
	posByID := map[string]posKey{}
	totals := map[string]int64{}
	cells := map[string]map[string]int64{} // month → posID → spent

	for _, r := range rows {
		pid := uuid.UUID(r.PosID.Bytes).String()
		posByID[pid] = posKey{
			ID:       pid,
			Name:     r.PosName,
			Currency: r.PosCurrency,
		}
		totals[pid] += r.Spent
		monthKey := r.Month.Time.Format("2006-01")
		if cells[monthKey] == nil {
			cells[monthKey] = map[string]int64{}
		}
		cells[monthKey][pid] += r.Spent
	}

	// Top-N pos by total spent, ties broken by name.
	type posTotal struct {
		Pos   posKey
		Total int64
	}
	allPos := make([]posTotal, 0, len(totals))
	for pid, t := range totals {
		allPos = append(allPos, posTotal{Pos: posByID[pid], Total: t})
	}
	sort.Slice(allPos, func(i, j int) bool {
		if allPos[i].Total != allPos[j].Total {
			return allPos[i].Total > allPos[j].Total
		}
		return allPos[i].Pos.Name < allPos[j].Pos.Name
	})
	topN := defaultTopN
	if len(allPos) < topN {
		topN = len(allPos)
	}
	for i := 0; i < topN; i++ {
		data.Columns = append(data.Columns, template.SpendingColumn{
			PosID:    allPos[i].Pos.ID,
			Name:     allPos[i].Pos.Name,
			Currency: allPos[i].Pos.Currency,
			Total:    allPos[i].Total,
		})
	}

	// Months in [from, to] inclusive — render even empty rows for
	// visual rhythm continuity.
	monthsInRange := []time.Time{}
	cursor := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location())
	end := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, to.Location())
	for !cursor.After(end) {
		monthsInRange = append(monthsInRange, cursor)
		cursor = cursor.AddDate(0, 1, 0)
	}
	// Render newest first, matching the SQL's ORDER BY DESC.
	sort.Slice(monthsInRange, func(i, j int) bool {
		return monthsInRange[i].After(monthsInRange[j])
	})

	for _, m := range monthsInRange {
		monthKey := m.Format("2006-01")
		row := template.SpendingRow{Month: m.Format("Jan 2006")}
		var rowTotal int64
		for _, col := range data.Columns {
			cell := cells[monthKey][col.PosID]
			row.Cells = append(row.Cells, cell)
			rowTotal += cell
		}
		row.Total = rowTotal
		data.Rows = append(data.Rows, row)
	}

	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	return c.Render(http.StatusOK, "spending", data)
}
