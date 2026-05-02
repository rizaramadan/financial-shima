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
		c.Logger().Errorf("[FS-0210] GetPos: %v", err)
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

	if cash, err := q.GetPosCashBalance(ctx, pgtype.UUID{Bytes: id, Valid: true}); err == nil {
		data.Cash = cash
	} else {
		c.Logger().Errorf("[FS-0211] GetPosCashBalance: %v", err)
		data.LoadError = true
	}

	// Open obligations → receivables/payables. Inline JOIN so each row
	// carries the counterparty Pos name (not just its UUID) for the
	// rendered link text.
	obRows, err := h.DB.Query(ctx, `
		SELECT o.id, o.creditor_pos_id, o.debtor_pos_id, o.currency,
		       o.amount_owed, o.amount_repaid, o.created_at,
		       cred.name AS creditor_name, deb.name AS debtor_name
		  FROM pos_obligation o
		  JOIN pos cred ON cred.id = o.creditor_pos_id
		  JOIN pos deb  ON deb.id  = o.debtor_pos_id
		 WHERE o.cleared_at IS NULL
		   AND (o.creditor_pos_id = $1 OR o.debtor_pos_id = $1)
		 ORDER BY o.created_at DESC`, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		c.Logger().Errorf("[FS-0212] list obligations: %v", err)
		data.LoadError = true
	} else {
		defer obRows.Close()
		for obRows.Next() {
			var (
				obID, credID, debID         pgtype.UUID
				currency, credName, debName string
				amountOwed, amountRepaid    int64
				createdAt                   pgtype.Timestamptz
			)
			if err := obRows.Scan(&obID, &credID, &debID, &currency, &amountOwed, &amountRepaid, &createdAt, &credName, &debName); err != nil {
				c.Logger().Errorf("[FS-0213] scan obligation: %v", err)
				data.LoadError = true
				continue
			}
			outstanding := amountOwed - amountRepaid
			row := template.ObligationRow{
				ID:          uuid.UUID(obID.Bytes).String(),
				Currency:    currency,
				Outstanding: outstanding,
				CreatedAt:   createdAt.Time,
			}
			if credID.Valid && credID.Bytes == id {
				row.Direction = "receivable"
				row.OtherPosID = uuid.UUID(debID.Bytes).String()
				row.OtherPosName = debName
				data.Receivables += outstanding
			} else {
				row.Direction = "payable"
				row.OtherPosID = uuid.UUID(credID.Bytes).String()
				row.OtherPosName = credName
				data.Payables += outstanding
			}
			data.Obligations = append(data.Obligations, row)
		}
	}

	txns, err := q.ListTransactionsByPos(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		c.Logger().Errorf("[FS-0214] ListTransactionsByPos: %v", err)
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
