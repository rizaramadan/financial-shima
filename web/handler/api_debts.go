package handler

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/logic/obligation"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APIDebt is one open borrow-mode debt for GET /api/v1/debts.
// outstanding = owed - repaid in cents (logic/obligation.Outstanding).
type APIDebt struct {
	ID            string            `json:"id"`
	TransactionID string            `json:"transaction_id"`
	CreditorPosID string            `json:"creditor_pos_id"`
	CreditorPos   string            `json:"creditor_pos_name"`
	DebtorPosID   string            `json:"debtor_pos_id"`
	DebtorPos     string            `json:"debtor_pos_name"`
	Currency      string            `json:"currency"`
	Owed          int64             `json:"owed"`
	Repaid        int64             `json:"repaid"`
	Outstanding   int64             `json:"outstanding"`
	CreatedAt     time.Time         `json:"created_at"`
	Links         map[string]string `json:"_links,omitempty"`
}

// APIDebtTotal is the outstanding rollup for one currency.
type APIDebtTotal struct {
	Currency    string `json:"currency"`
	Outstanding int64  `json:"outstanding"`
}

// APIDebtsResponse is the envelope for GET /api/v1/debts. Debts and
// Totals are `[]` (never null) on empty so the LLM caller parses one
// shape. _links/_actions are the HATEOAS-lite affordances: self plus
// the existing GETs related to each row.
type APIDebtsResponse struct {
	Debts   []APIDebt         `json:"debts"`
	Totals  []APIDebtTotal    `json:"totals"`
	Links   map[string]string `json:"_links,omitempty"`
	Actions []MoneyAction     `json:"_actions,omitempty"`
}

// APIDebtsList implements GET /api/v1/debts per spec §4.3/§4.2.
//
// Every pos_obligation row with cleared_at IS NULL, creditor/debtor
// Pos names joined, outstanding computed in logic/obligation (pure).
// Totals roll outstanding up per currency.
//
// Query parameters (all optional; AND-combined):
//
//	creditor_pos_id=<uuid>  debts this Pos is owed
//	debtor_pos_id=<uuid>    debts this Pos owes
//	currency=<code>         e.g. idr, gold-g
//
// The open-debt set is bounded (household scale), so id/currency
// filters apply in Go after the single date-free fetch — same call as
// APITransactionsList's post-fetch filters.
//
// Errors:
//   - 400 validation_failed: malformed creditor_pos_id/debtor_pos_id.
//   - 503 service_unavailable: DB unwired.
//   - 500 internal_error: DB query failure.
func (h *Handlers) APIDebtsList(c echo.Context) error {
	parsePosFilter := func(name string) (uuid.UUID, bool, error) {
		raw := strings.TrimSpace(c.QueryParam(name))
		if raw == "" {
			return uuid.Nil, false, nil
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return uuid.Nil, true, err
		}
		return id, true, nil
	}
	creditorID, hasCreditor, err := parsePosFilter("creditor_pos_id")
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "creditor_pos_id must be a valid UUID")
	}
	debtorID, hasDebtor, err := parsePosFilter("debtor_pos_id")
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "debtor_pos_id must be a valid UUID")
	}
	currency := strings.TrimSpace(c.QueryParam("currency"))

	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	rows, err := dbq.New(h.DB).ListOpenObligations(ctx)
	if err != nil {
		c.Logger().Errorf("api list debts: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to list debts")
	}

	debts := make([]APIDebt, 0, len(rows))
	for _, r := range rows {
		if hasCreditor && r.CreditorPosID.Bytes != creditorID {
			continue
		}
		if hasDebtor && r.DebtorPosID.Bytes != debtorID {
			continue
		}
		if currency != "" && r.Currency != currency {
			continue
		}
		creditor := uuid.UUID(r.CreditorPosID.Bytes).String()
		debtor := uuid.UUID(r.DebtorPosID.Bytes).String()
		debts = append(debts, APIDebt{
			ID:            uuid.UUID(r.ID.Bytes).String(),
			TransactionID: uuid.UUID(r.TransactionID.Bytes).String(),
			CreditorPosID: creditor,
			CreditorPos:   r.CreditorPosName,
			DebtorPosID:   debtor,
			DebtorPos:     r.DebtorPosName,
			Currency:      r.Currency,
			Owed:          r.AmountOwed,
			Repaid:        r.AmountRepaid,
			Outstanding:   obligation.Outstanding(obligation.Obligation{Owed: r.AmountOwed, Repaid: r.AmountRepaid}),
			CreatedAt:     r.CreatedAt.Time,
			Links: map[string]string{
				"creditor_pos": "/api/v1/pos/" + creditor,
				"debtor_pos":   "/api/v1/pos/" + debtor,
				"transactions": "/api/v1/transactions",
			},
		})
	}

	totalsByCcy := map[string]int64{}
	for _, d := range debts {
		totalsByCcy[d.Currency] += d.Outstanding
	}
	totals := make([]APIDebtTotal, 0, len(totalsByCcy))
	for ccy, out := range totalsByCcy {
		totals = append(totals, APIDebtTotal{Currency: ccy, Outstanding: out})
	}
	sort.Slice(totals, func(i, j int) bool { return totals[i].Currency < totals[j].Currency })

	return c.JSON(http.StatusOK, APIDebtsResponse{
		Debts:  debts,
		Totals: totals,
		Links: map[string]string{
			"self":         "/api/v1/debts",
			"transactions": "/api/v1/transactions",
			"balances":     "/api/v1/balances",
		},
		Actions: []MoneyAction{
			{Name: "recap", Method: "GET", Href: "/api/v1/recap"},
			{Name: "balances", Method: "GET", Href: "/api/v1/balances"},
		},
	})
}
