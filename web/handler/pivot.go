package handler

import (
	"context"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	pivotlogic "github.com/rizaramadan/financial-shima/logic/pivot"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// PivotGet renders the roll-forward report ("Pivot"): one screen showing
// how each savings position reached its current balance, grouped by
// (Akun, Kategori) with subtotals per Kategori, totals per Akun and a
// grand total.
//
// Server-side it ships the full movement list (money_in / money_out,
// signed) plus the account/position catalogue as embedded JSON; all
// filtering, grouping and total recomputation happens client-side so
// every change re-renders without a reload, with the filter state kept
// in the URL. Identity is the (account, pos) pair — two positions that
// share a name under different accounts are never merged.
func (h *Handlers) PivotGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	data := template.PivotData{
		Title:       "Pivot",
		DisplayName: u.DisplayName,
	}

	if h.DB == nil {
		return c.Render(http.StatusOK, "pivot", data)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	q := dbq.New(h.DB)

	accRows, err := q.ListAccounts(ctx)
	if err != nil {
		c.Logger().Errorf("pivot ListAccounts: %v", err)
		data.LoadError = true
		data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
		return c.Render(http.StatusOK, "pivot", data)
	}
	for _, a := range accRows {
		data.Accounts = append(data.Accounts, template.PivotAccount{
			ID:   uuid.UUID(a.ID.Bytes).String(),
			Name: a.Name,
		})
	}

	posRows, err := q.ListPos(ctx)
	if err != nil {
		c.Logger().Errorf("pivot ListPos: %v", err)
		data.LoadError = true
		data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
		return c.Render(http.StatusOK, "pivot", data)
	}
	accNameByID := make(map[[16]byte]string, len(accRows))
	for _, a := range accRows {
		accNameByID[a.ID.Bytes] = a.Name
	}
	for _, p := range posRows {
		data.Positions = append(data.Positions, template.PivotPosition{
			PosID:       uuid.UUID(p.ID.Bytes).String(),
			Name:        p.Name,
			AccountID:   uuid.UUID(p.AccountID.Bytes).String(),
			AccountName: accNameByID[p.AccountID.Bytes],
		})
	}
	// Deterministic filter order: Akun by name, Kategori by Akun then
	// name. The (Akun, Kategori) pair stays the grouping key throughout.
	sort.SliceStable(data.Positions, func(i, j int) bool {
		if data.Positions[i].AccountName != data.Positions[j].AccountName {
			return data.Positions[i].AccountName < data.Positions[j].AccountName
		}
		if data.Positions[i].Name != data.Positions[j].Name {
			return data.Positions[i].Name < data.Positions[j].Name
		}
		return data.Positions[i].PosID < data.Positions[j].PosID
	})
	for _, p := range data.Positions {
		g := data.GroupFor(p.AccountID)
		g.AccountName = p.AccountName
		g.Positions = append(g.Positions, p)
	}

	rows, err := h.DB.Query(ctx, `
		SELECT t.id, t.type::text, t.effective_date, t.pos_amount, t.note,
		       cp.name AS cp_name,
		       p.id AS pos_id, p.name AS pos_name,
		       a.id AS account_id, a.name AS account_name
		  FROM transactions t
		  JOIN pos p ON p.id = t.pos_id
		  JOIN accounts a ON a.id = p.account_id
		  LEFT JOIN counterparties cp ON cp.id = t.counterparty_id
		 WHERE t.type IN ('money_in', 'money_out')
		 ORDER BY t.effective_date DESC, t.created_at DESC, t.id DESC
		 LIMIT 10000`)
	if err != nil {
		c.Logger().Errorf("pivot movements: %v", err)
		data.LoadError = true
		data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
		return c.Render(http.StatusOK, "pivot", data)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, posID, accID     pgtype.UUID
			typ                  string
			effDate              pgtype.Date
			posAmount            *int64
			note, cpName         *string
			posName, accountName string
		)
		if err := rows.Scan(&id, &typ, &effDate, &posAmount, &note,
			&cpName, &posID, &posName, &accID, &accountName); err != nil {
			c.Logger().Errorf("pivot scan: %v", err)
			data.LoadError = true
			continue
		}
		var amount int64
		if posAmount != nil {
			amount = *posAmount
		}
		if typ == "money_out" {
			amount = -amount
		}
		var noteStr, cpStr string
		if note != nil {
			noteStr = *note
		}
		if cpName != nil {
			cpStr = *cpName
		}
		data.Movements = append(data.Movements, template.PivotMovement{
			ID:          uuid.UUID(id.Bytes).String(),
			PosID:       uuid.UUID(posID.Bytes).String(),
			AccountID:   uuid.UUID(accID.Bytes).String(),
			AccountName: accountName,
			PosName:     posName,
			Nama:        pivotlogic.Label(noteStr, cpStr, effDate.Time),
			Date:        effDate.Time.Format(dateLayout),
			Amount:      amount,
			Type:        typ,
		})
	}
	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	return c.Render(http.StatusOK, "pivot", data)
}
