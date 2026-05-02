package handler

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APIBalances is the §10.5 sanity surface — per-account and per-Pos
// derived balances, plus a per-currency reconciliation summary the
// LLM can use to verify its seed without building the same
// computation locally.
type APIBalances struct {
	Accounts       []APIAccountBalance       `json:"accounts"`
	Pos            []APIPosBalance           `json:"pos"`
	Reconciliation []APICurrencyReconciliation `json:"reconciliation"`
}

// APIAccountBalance is one row of the account-side balance sheet.
type APIAccountBalance struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BalanceIDR int64  `json:"balance_idr"`
}

// APIPosBalance is one row of the Pos-side balance sheet.
type APIPosBalance struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
	Cash     int64  `json:"cash"`
}

// APICurrencyReconciliation pins spec §10.5: per currency,
// Σ(Account.balance) must equal Σ(Pos.cash). Currently only IDR has
// account-side numbers (per spec §4.1 accounts are IDR-only); other
// currencies show only the Pos-side total. The `equal` boolean is
// the operator's at-a-glance sanity bit.
type APICurrencyReconciliation struct {
	Currency      string `json:"currency"`
	AccountTotal  int64  `json:"account_total"`
	PosTotal      int64  `json:"pos_total"`
	Difference    int64  `json:"difference"` // account_total - pos_total
	Equal         bool   `json:"equal"`
}

// APIBalancesGet implements GET /api/v1/balances.
//
// Pulls the same per-account / per-Pos aggregates the home page uses,
// joins with names, and emits the JSON shape above. The whole payload
// is bounded by the household's account + Pos count (dozens at most),
// so a bare struct (no pagination) is the right shape.
func (h *Handlers) APIBalancesGet(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	q := dbq.New(h.DB)

	accountBal := map[[16]byte]int64{}
	if rows, err := q.SumAccountBalances(ctx); err == nil {
		for _, r := range rows {
			accountBal[r.ID.Bytes] = r.Balance
		}
	} else {
		c.Logger().Errorf("balances: SumAccountBalances: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to compute account balances")
	}
	posBal := map[[16]byte]int64{}
	if rows, err := q.SumPosCashBalances(ctx); err == nil {
		for _, r := range rows {
			posBal[r.ID.Bytes] = r.Balance
		}
	} else {
		c.Logger().Errorf("balances: SumPosCashBalances: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to compute pos balances")
	}
	// Per-currency account-side flow restricted to that currency's
	// pos transactions — this is what §10.5 actually compares against
	// pos cash. Summing ALL account flows would over-count cross-
	// currency outlays (e.g., IDR spent on a USD savings Pos appears
	// as IDR account flow but does NOT appear as IDR pos cash).
	accFlowByCcy := map[string]int64{}
	if rows, err := q.SumAccountBalancesByPosCurrency(ctx); err == nil {
		for _, r := range rows {
			accFlowByCcy[r.Currency] = r.Total
		}
	} else {
		c.Logger().Errorf("balances: SumAccountBalancesByPosCurrency: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to compute reconciliation totals")
	}

	accounts, err := q.ListAccounts(ctx)
	if err != nil {
		c.Logger().Errorf("balances: ListAccounts: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to list accounts")
	}
	pos, err := q.ListPos(ctx)
	if err != nil {
		c.Logger().Errorf("balances: ListPos: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to list pos")
	}

	out := APIBalances{
		Accounts: make([]APIAccountBalance, 0, len(accounts)),
		Pos:      make([]APIPosBalance, 0, len(pos)),
	}

	for _, a := range accounts {
		out.Accounts = append(out.Accounts, APIAccountBalance{
			ID:         uuid.UUID(a.ID.Bytes).String(),
			Name:       a.Name,
			BalanceIDR: accountBal[a.ID.Bytes],
		})
	}

	posTotalsByCcy := map[string]int64{}
	for _, p := range pos {
		cash := posBal[p.ID.Bytes]
		posTotalsByCcy[p.Currency] += cash
		out.Pos = append(out.Pos, APIPosBalance{
			ID:       uuid.UUID(p.ID.Bytes).String(),
			Name:     p.Name,
			Currency: p.Currency,
			Cash:     cash,
		})
	}

	// Reconciliation per spec §10.5: per-currency, the account-side
	// flow restricted to that currency's pos == pos-side cash.
	//   For currency=idr — the canonical invariant; equal must hold.
	//   For non-IDR     — account_total reflects historical IDR outlay,
	//                      pos_total is in the pos's own unit; they
	//                      are NOT directly comparable, so equal=false.
	seen := map[string]bool{}
	for ccy, posTotal := range posTotalsByCcy {
		accTotal := accFlowByCcy[ccy]
		out.Reconciliation = append(out.Reconciliation, APICurrencyReconciliation{
			Currency:     ccy,
			AccountTotal: accTotal,
			PosTotal:     posTotal,
			Difference:   accTotal - posTotal,
			Equal:        ccy == "idr" && accTotal == posTotal,
		})
		seen[ccy] = true
	}
	// Always emit an IDR row so the LLM can unconditionally check it.
	if !seen["idr"] {
		idrAcc := accFlowByCcy["idr"]
		out.Reconciliation = append(out.Reconciliation, APICurrencyReconciliation{
			Currency:     "idr",
			AccountTotal: idrAcc,
			PosTotal:     0,
			Difference:   idrAcc,
			Equal:        idrAcc == 0,
		})
	}

	return c.JSON(http.StatusOK, out)
}
