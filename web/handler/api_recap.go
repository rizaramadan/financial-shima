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
)

// APIRecapRow is one (currency, bucket) cell for GET /api/v1/recap.
// Period is the bucket start as YYYY-MM-DD; net = inflow - outflow in
// cents (logic/balance.RecapNet).
type APIRecapRow struct {
	Currency string `json:"currency"`
	Period   string `json:"period"`
	Inflow   int64  `json:"inflow"`
	Outflow  int64  `json:"outflow"`
	Net      int64  `json:"net"`
}

// APIRecapTotal rolls one currency across the whole window.
type APIRecapTotal struct {
	Currency string `json:"currency"`
	Inflow   int64  `json:"inflow"`
	Outflow  int64  `json:"outflow"`
	Net      int64  `json:"net"`
}

// APIRecapResponse is the envelope for GET /api/v1/recap. Rows are
// newest-bucket first (matching the SQL ORDER BY); totals carry the
// window-wide per-currency sums. /spending (HTML §6.4) is untouched —
// this is the JSON twin built on the same date_trunc pattern.
type APIRecapResponse struct {
	Bucket  string            `json:"bucket"`
	From    string            `json:"from"`
	To      string            `json:"to"`
	Rows    []APIRecapRow     `json:"rows"`
	Totals  []APIRecapTotal   `json:"totals"`
	Links   map[string]string `json:"_links,omitempty"`
	Actions []MoneyAction     `json:"_actions,omitempty"`
}

// APIRecapGet implements GET /api/v1/recap.
//
// Query parameters:
//
//	bucket=week|month|year  defaults to month
//	from=YYYY-MM-DD         defaults to the first day of (now - 5 months)
//	to=YYYY-MM-DD           defaults to today
//
// Bucketed money_in/money_out sums per currency with in/out/net.
// Validation failures (400) precede the DB-wired check so malformed
// requests fail fast without a database — same ordering as
// APITransactionsList.
func (h *Handlers) APIRecapGet(c echo.Context) error {
	bucketRaw := strings.TrimSpace(c.QueryParam("bucket"))
	if bucketRaw == "" {
		bucketRaw = string(balance.RecapMonth)
	}
	bucket, err := balance.ParseRecapBucket(bucketRaw)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "bucket must be week, month, or year")
	}

	now := time.Now()
	defaultFrom := time.Date(now.Year(), now.Month()-5, 1, 0, 0, 0, 0, now.Location())
	from := defaultFrom
	to := now
	if v := strings.TrimSpace(c.QueryParam("from")); v != "" {
		t, err := time.Parse(dateLayout, v)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "from must be YYYY-MM-DD")
		}
		from = t
	}
	if v := strings.TrimSpace(c.QueryParam("to")); v != "" {
		t, err := time.Parse(dateLayout, v)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "to must be YYYY-MM-DD")
		}
		to = t
	}

	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	q := dbq.New(h.DB)
	arg := dbq.SumMoneyFlowByMonthParams{
		EffectiveDate:   pgtype.Date{Time: from, Valid: true},
		EffectiveDate_2: pgtype.Date{Time: to, Valid: true},
	}
	rows := make([]APIRecapRow, 0)
	totalsByCcy := map[string]*APIRecapTotal{}
	accumulate := func(currency, period string, inflow, outflow int64) {
		rows = append(rows, APIRecapRow{
			Currency: currency,
			Period:   period,
			Inflow:   inflow,
			Outflow:  outflow,
			Net:      balance.RecapNet(inflow, outflow),
		})
		t, ok := totalsByCcy[currency]
		if !ok {
			t = &APIRecapTotal{Currency: currency}
			totalsByCcy[currency] = t
		}
		t.Inflow += inflow
		t.Outflow += outflow
		t.Net = balance.RecapNet(t.Inflow, t.Outflow)
	}
	switch bucket {
	case balance.RecapWeek:
		wrows, err := q.SumMoneyFlowByWeek(ctx, dbq.SumMoneyFlowByWeekParams(arg))
		if err != nil {
			c.Logger().Errorf("api recap week: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to compute recap")
		}
		for _, r := range wrows {
			accumulate(r.Currency, r.Bucket.Time.Format(dateLayout), r.Inflow, r.Outflow)
		}
	case balance.RecapYear:
		yrows, err := q.SumMoneyFlowByYear(ctx, dbq.SumMoneyFlowByYearParams(arg))
		if err != nil {
			c.Logger().Errorf("api recap year: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to compute recap")
		}
		for _, r := range yrows {
			accumulate(r.Currency, r.Bucket.Time.Format(dateLayout), r.Inflow, r.Outflow)
		}
	default:
		mrows, err := q.SumMoneyFlowByMonth(ctx, arg)
		if err != nil {
			c.Logger().Errorf("api recap month: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to compute recap")
		}
		for _, r := range mrows {
			accumulate(r.Currency, r.Bucket.Time.Format(dateLayout), r.Inflow, r.Outflow)
		}
	}

	totals := make([]APIRecapTotal, 0, len(totalsByCcy))
	for _, t := range totalsByCcy {
		totals = append(totals, *t)
	}
	sort.Slice(totals, func(i, j int) bool { return totals[i].Currency < totals[j].Currency })

	self := "/api/v1/recap?bucket=" + string(bucket) +
		"&from=" + from.Format(dateLayout) + "&to=" + to.Format(dateLayout)
	return c.JSON(http.StatusOK, APIRecapResponse{
		Bucket: string(bucket),
		From:   from.Format(dateLayout),
		To:     to.Format(dateLayout),
		Rows:   rows,
		Totals: totals,
		Links: map[string]string{
			"self":         self,
			"transactions": "/api/v1/transactions",
			"balances":     "/api/v1/balances",
			"debts":        "/api/v1/debts",
		},
		Actions: []MoneyAction{
			{Name: "debts", Method: "GET", Href: "/api/v1/debts"},
			{Name: "balances", Method: "GET", Href: "/api/v1/balances"},
		},
	})
}
