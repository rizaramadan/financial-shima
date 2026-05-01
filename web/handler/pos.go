package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// PosGet renders the §6.3 single-Pos detail view: name, currency, target,
// receivables (Σ open obligations where this pos is creditor), payables
// (Σ where this pos is debtor), open-obligation list, and a chronological
// transaction list scoped to this Pos.
//
// Cash balance is derived from transactions; until the ledger insert path
// is wired into a creation handler, transactions for new Pos are empty,
// so cash renders as em-dash. The receivables / payables aggregation is
// over pos_obligation rows so the structure is visible immediately.
func (h *Handlers) PosGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	if h.DB == nil {
		// No data layer; render a minimal not-found page rather than
		// pretend the Pos exists.
		return c.Render(http.StatusOK, "pos", template.PosDetailData{
			Title:       "Pos",
			DisplayName: u.DisplayName,
			NotFound:    true,
		})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	pos, err := q.GetPos(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		data := template.PosDetailData{
			Title:       "Pos",
			DisplayName: u.DisplayName,
			UnreadCount: h.loadBellCount(ctx, c, u.ID),
		}
		if errors.Is(err, pgx.ErrNoRows) {
			data.NotFound = true
			return c.Render(http.StatusOK, "pos", data)
		}
		c.Logger().Errorf("GetPos: %v", err)
		data.LoadError = true
		return c.Render(http.StatusOK, "pos", data)
	}

	data := template.PosDetailData{
		Title:       pos.Name,
		DisplayName: u.DisplayName,
		ID:          uuid.UUID(pos.ID.Bytes).String(),
		Name:        pos.Name,
		Currency:    pos.Currency,
		Archived:    pos.Archived,
	}
	if pos.Target != nil {
		data.Target = *pos.Target
		data.HasTarget = true
	}

	// Open obligations → receivables/payables.
	obs, err := q.ListObligationsForPos(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		c.Logger().Errorf("ListObligationsForPos: %v", err)
		data.LoadError = true
	}
	for _, o := range obs {
		// "open" because the SQL filters cleared_at IS NULL — outstanding
		// = owed - repaid.
		outstanding := o.AmountOwed - o.AmountRepaid
		row := template.ObligationRow{
			ID:          uuid.UUID(o.ID.Bytes).String(),
			Currency:    o.Currency,
			Outstanding: outstanding,
			CreatedAt:   o.CreatedAt.Time,
		}
		if o.CreditorPosID.Valid && o.CreditorPosID.Bytes == id {
			row.Direction = "receivable"
			row.OtherPosID = uuid.UUID(o.DebtorPosID.Bytes).String()
			data.Receivables += outstanding
		} else {
			row.Direction = "payable"
			row.OtherPosID = uuid.UUID(o.CreditorPosID.Bytes).String()
			data.Payables += outstanding
		}
		data.Obligations = append(data.Obligations, row)
	}

	txns, err := q.ListTransactionsByPos(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		c.Logger().Errorf("ListTransactionsByPos: %v", err)
		data.LoadError = true
	}
	for _, r := range txns {
		var (
			account, cp, note, revID string
			amount                   int64
			isReversal               bool
		)
		if r.AccountName != nil {
			account = *r.AccountName
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
		data.Transactions = append(data.Transactions, template.PosTransactionRow{
			ID:               uuid.UUID(r.ID.Bytes).String(),
			Type:             string(r.Type),
			EffectiveDate:    r.EffectiveDate.Time.Format(dateLayout),
			Amount:           amount,
			AccountName:      account,
			CounterpartyName: cp,
			Note:             note,
			IsReversal:       isReversal,
			ReversesID:       revID,
		})
	}

	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	return c.Render(http.StatusOK, "pos", data)
}
