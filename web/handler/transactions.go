package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

const (
	defaultTxnRangeDays = 30
	dateLayout          = "2006-01-02"
)

// TransactionsGet renders the §6.1 list. Date-range filter only in this
// stage; multi-select account/pos/counterparty/type filters land later.
//
// Query params:
//
//	?from=YYYY-MM-DD  defaults to 30 days ago
//	?to=YYYY-MM-DD    defaults to today
//
// Invalid dates fall back to the defaults silently — the user sees what
// they meant rather than a 400.
func (h *Handlers) TransactionsGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	now := time.Now()
	from := now.AddDate(0, 0, -defaultTxnRangeDays)
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

	data := template.TransactionsData{
		Title:       "Transactions",
		DisplayName: u.DisplayName,
		From:        from.Format(dateLayout),
		To:          to.Format(dateLayout),
	}
	if h.DB == nil {
		return c.Render(http.StatusOK, "transactions", data)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	rows, err := q.ListTransactionsByDateRange(ctx, dbq.ListTransactionsByDateRangeParams{
		EffectiveDate:   pgtype.Date{Time: from, Valid: true},
		EffectiveDate_2: pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		c.Logger().Errorf("ListTransactionsByDateRange: %v", err)
		data.LoadError = true
	}
	for _, r := range rows {
		var (
			account, pos, ccy, cp, note, revID string
			isReversal                          bool
			amount                              int64
		)
		if r.AccountName != nil {
			account = *r.AccountName
		}
		if r.PosName != nil {
			pos = *r.PosName
		}
		if r.PosCurrency != nil {
			ccy = *r.PosCurrency
		}
		if r.CounterpartyName != nil {
			cp = *r.CounterpartyName
		}
		if r.Note != nil {
			note = *r.Note
		}
		if r.PosAmount != nil {
			amount = *r.PosAmount
		}
		if r.ReversesID.Valid {
			isReversal = true
			revID = uuid.UUID(r.ReversesID.Bytes).String()
		}
		data.Items = append(data.Items, template.TransactionRow{
			ID:               uuid.UUID(r.ID.Bytes).String(),
			Type:             string(r.Type),
			EffectiveDate:    r.EffectiveDate.Time.Format(dateLayout),
			Amount:           amount,
			Currency:         ccy,
			AccountName:      account,
			PosName:          pos,
			CounterpartyName: cp,
			Note:             note,
			IsReversal:       isReversal,
			ReversesID:       revID,
		})
	}
	if data.LoadError == false && h.DB != nil {
		// Bell badge — same one-shot pattern as Home.
		data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	}
	return c.Render(http.StatusOK, "transactions", data)
}
