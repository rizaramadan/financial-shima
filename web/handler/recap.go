package handler

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/logic/balance"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// RecapGet renders the adjustable recap ("Rekap"): bucketed
// money_in/money_out sums per currency with in/out/net, switchable
// between week, month, and year grains via ?bucket=. Same
// SumMoneyFlowBy{Week,Month,Year} queries the /api/v1/recap endpoint
// serves, rendered as HTML. An invalid bucket re-renders with an
// error (human page — never a bare 400).
func (h *Handlers) RecapGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	bucketRaw := strings.TrimSpace(c.QueryParam("bucket"))
	if bucketRaw == "" {
		bucketRaw = string(balance.RecapMonth)
	}
	bucket, bucketErr := balance.ParseRecapBucket(bucketRaw)

	now := time.Now()
	defaultFrom := time.Date(now.Year(), now.Month()-5, 1, 0, 0, 0, 0, now.Location())
	from, fromErr := parseRecapDate(c.QueryParam("from"), defaultFrom)
	to, toErr := parseRecapDate(c.QueryParam("to"), now)

	data := template.RecapData{
		Title:       "Rekap",
		DisplayName: u.DisplayName,
		Bucket:      bucketRaw,
		From:        from.Format(dateLayout),
		To:          to.Format(dateLayout),
	}
	if bucketErr != nil {
		data.Errors = []string{"Bucket must be week, month, or year."}
	}
	if fromErr != nil {
		data.Errors = append(data.Errors, "From must be YYYY-MM-DD.")
	}
	if toErr != nil {
		data.Errors = append(data.Errors, "To must be YYYY-MM-DD.")
	}
	if len(data.Errors) == 0 {
		if msg := reversedRangeError(from, to); msg != "" {
			data.Errors = append(data.Errors, msg)
		}
	}

	if h.DB == nil || len(data.Errors) > 0 {
		return c.Render(http.StatusOK, "recap", data)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	q := dbq.New(h.DB)
	arg := dbq.SumMoneyFlowByMonthParams{
		EffectiveDate:   pgtype.Date{Time: from, Valid: true},
		EffectiveDate_2: pgtype.Date{Time: to, Valid: true},
	}
	accumulate := func(currency, period string, inflow, outflow int64) {
		t := data.TotalByCurrency(currency)
		t.Inflow += inflow
		t.Outflow += outflow
		t.Net = balance.RecapNet(t.Inflow, t.Outflow)
		t.Rows = append(t.Rows, template.RecapRow{
			Period:  period,
			Inflow:  inflow,
			Outflow: outflow,
			Net:     balance.RecapNet(inflow, outflow),
		})
	}
	var queryErr error
	switch bucket {
	case balance.RecapWeek:
		var wrows []dbq.SumMoneyFlowByWeekRow
		wrows, queryErr = q.SumMoneyFlowByWeek(ctx, dbq.SumMoneyFlowByWeekParams(arg))
		for _, r := range wrows {
			accumulate(r.Currency, r.Bucket.Time.Format(recapWeekLabel), r.Inflow, r.Outflow)
		}
	case balance.RecapYear:
		var yrows []dbq.SumMoneyFlowByYearRow
		yrows, queryErr = q.SumMoneyFlowByYear(ctx, dbq.SumMoneyFlowByYearParams(arg))
		for _, r := range yrows {
			accumulate(r.Currency, r.Bucket.Time.Format("2006"), r.Inflow, r.Outflow)
		}
	default:
		var mrows []dbq.SumMoneyFlowByMonthRow
		mrows, queryErr = q.SumMoneyFlowByMonth(ctx, arg)
		for _, r := range mrows {
			accumulate(r.Currency, r.Bucket.Time.Format("Jan 2006"), r.Inflow, r.Outflow)
		}
	}
	if queryErr != nil {
		c.Logger().Errorf("recap %s: %v", bucket, queryErr)
		data.LoadError = true
		data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
		return c.Render(http.StatusOK, "recap", data)
	}

	sort.Slice(data.Totals, func(i, j int) bool {
		return data.Totals[i].Currency < data.Totals[j].Currency
	})
	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	return c.Render(http.StatusOK, "recap", data)
}

// parseRecapDate parses a YYYY-MM-DD query param, falling back to the
// given default when blank. A parse failure returns the fallback plus
// the error so the page can re-render with a message.
func parseRecapDate(raw string, fallback time.Time) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	t, err := time.Parse(dateLayout, strings.TrimSpace(raw))
	if err != nil {
		return fallback, err
	}
	return t, nil
}

// recapWeekLabel renders a week-bucket start date ("17 Mar").
const recapWeekLabel = "02 Jan"
