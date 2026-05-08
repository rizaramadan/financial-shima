package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/ledger"
	"github.com/rizaramadan/financial-shima/logic/money"
	"github.com/rizaramadan/financial-shima/logic/notification"
	logictxn "github.com/rizaramadan/financial-shima/logic/transaction"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// TransactionNewGet renders the one-off "new income" / "new spending"
// form. Type comes from the `?type=money_in|money_out` query param so
// the same template renders both with a focused title and submit
// label. IDR-only for now (the dominant household case); cross-
// currency support lands when there's a real Pos that needs it.
func (h *Handlers) TransactionNewGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	txnType := strings.TrimSpace(c.QueryParam("type"))
	if txnType != "money_in" && txnType != "money_out" {
		txnType = "money_out" // most common; matches "+ Spending" CTA
	}

	data := template.TransactionNewData{
		Title:          newTxnTitle(txnType),
		DisplayName:    u.DisplayName,
		Type:           txnType,
		EffectiveDate:  time.Now().Format(dateLayout),
		IdempotencyKey: uuid.NewString(),
	}

	if h.DB != nil {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
		defer cancel()
		h.loadTransactionNewOptions(ctx, c, &data)
	}
	data.UnreadCount = h.loadBellCount(context.Background(), c, u.ID)
	return c.Render(http.StatusOK, "transaction_new", data)
}

// TransactionNewPost validates and inserts a single money_in /
// money_out via dependencies/ledger.Service, the same atomic path the
// API uses (spec §10.8). Validation failures re-render the form with
// the user's input and the error list. On success redirects to the
// Pos detail view so the user immediately sees the new balance.
func (h *Handlers) TransactionNewPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	in := template.TransactionNewData{
		Title:            newTxnTitle(c.FormValue("type")),
		DisplayName:      u.DisplayName,
		Type:             strings.TrimSpace(c.FormValue("type")),
		EffectiveDate:    strings.TrimSpace(c.FormValue("effective_date")),
		AccountID:        strings.TrimSpace(c.FormValue("account_id")),
		PosID:            strings.TrimSpace(c.FormValue("pos_id")),
		AmountRaw:        strings.TrimSpace(c.FormValue("amount")),
		CounterpartyName: strings.TrimSpace(c.FormValue("counterparty_name")),
		Note:             strings.TrimSpace(c.FormValue("note")),
		IdempotencyKey:   strings.TrimSpace(c.FormValue("idempotency_key")),
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = uuid.NewString()
	}

	rerender := func(errs []string) error {
		in.Errors = errs
		if h.DB != nil {
			ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
			defer cancel()
			h.loadTransactionNewOptions(ctx, c, &in)
		}
		in.UnreadCount = h.loadBellCount(context.Background(), c, u.ID)
		return c.Render(http.StatusOK, "transaction_new", in)
	}

	// Field-level validation first. Each branch reports the first
	// problem and short-circuits — keeps the error list focused.
	if in.Type != "money_in" && in.Type != "money_out" {
		return rerender([]string{"Type must be income or spending."})
	}
	effDate, err := time.Parse(dateLayout, in.EffectiveDate)
	if err != nil {
		return rerender([]string{"Effective date is required (YYYY-MM-DD)."})
	}
	accountID, err := uuid.Parse(in.AccountID)
	if err != nil {
		return rerender([]string{"Account is required."})
	}
	posID, err := uuid.Parse(in.PosID)
	if err != nil {
		return rerender([]string{"Pos is required."})
	}
	amount, err := strconv.ParseInt(in.AmountRaw, 10, 64)
	if err != nil || amount <= 0 {
		return rerender([]string{"Amount must be a positive whole number (smallest unit, e.g. rupiah)."})
	}
	if in.CounterpartyName == "" {
		return rerender([]string{"Counterparty is required."})
	}

	if h.DB == nil {
		return rerender([]string{"Database is not configured. Set DATABASE_URL and restart."})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	// Resolve account + pos so we can run §5.1 validation against real
	// rows (currency, archived) before handing off to ledger.
	account, err := q.GetAccount(ctx, pgtype.UUID{Bytes: accountID, Valid: true})
	if err != nil {
		c.Logger().Errorf("[FS-0270] new txn: GetAccount: %v", err)
		return rerender([]string{"Account not found."})
	}
	pos, err := q.GetPos(ctx, pgtype.UUID{Bytes: posID, Valid: true})
	if err != nil {
		c.Logger().Errorf("[FS-0271] new txn: GetPos: %v", err)
		return rerender([]string{"Pos not found."})
	}
	// Web form is IDR-only for now; reject cross-currency until we
	// design the FX UX. API can still do it directly.
	if account.Archived || pos.Archived {
		return rerender([]string{"Account and Pos must both be active (not archived)."})
	}
	if pos.Currency != "idr" {
		return rerender([]string{"Web form supports IDR only. Use the API for non-IDR Pos."})
	}

	cpRow, err := q.GetOrCreateCounterparty(ctx, in.CounterpartyName)
	if err != nil {
		c.Logger().Errorf("[FS-0272] new txn: get-or-create counterparty: %v", err)
		return rerender([]string{"Couldn’t resolve counterparty. Try again."})
	}

	logicIn := logictxn.MoneyInput{
		EffectiveDate: effDate,
		Account: logictxn.AccountRef{
			ID:       uuid.UUID(account.ID.Bytes).String(),
			Archived: account.Archived,
		},
		AccountAmount: money.New(amount, "idr"),
		Pos: logictxn.PosRef{
			ID:       uuid.UUID(pos.ID.Bytes).String(),
			Currency: pos.Currency,
			Archived: pos.Archived,
		},
		PosAmount:        money.New(amount, "idr"),
		CounterpartyName: cpRow.Name,
	}
	var violations []string
	if in.Type == "money_in" {
		violations = logictxn.ValidateMoneyIn(logicIn, time.Now())
	} else {
		violations = logictxn.ValidateMoneyOut(logicIn, time.Now())
	}
	if len(violations) > 0 {
		return rerender(violations)
	}

	svc := &ledger.Service{Pool: h.DB, Users: h.Auth.Users}
	_, err = svc.Insert(ctx, ledger.MoneyTxnInput{
		Type:           in.Type,
		EffectiveDate:  pgtype.Date{Time: effDate, Valid: true},
		AccountID:      accountID,
		AccountAmount:  amount,
		PosID:          posID,
		PosAmount:      amount,
		CounterpartyID: uuid.UUID(cpRow.ID.Bytes),
		Note:           in.Note,
		Source:         notification.SourceWeb,
		CreatedBy:      parseOptUUID(u.ID),
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		c.Logger().Errorf("[FS-0273] new txn: ledger.Insert: %v", err)
		return rerender([]string{"Couldn’t record the transaction. Try again."})
	}

	return c.Redirect(http.StatusSeeOther, "/pos/"+posID.String())
}

// loadTransactionNewOptions populates the form's account + pos
// selects. Called on both initial render and re-render after a
// validation failure so the user's other selections survive.
func (h *Handlers) loadTransactionNewOptions(ctx context.Context, c echo.Context, data *template.TransactionNewData) {
	q := dbq.New(h.DB)
	if accounts, err := q.ListAccounts(ctx); err == nil {
		for _, a := range accounts {
			data.Accounts = append(data.Accounts, template.AccountOption{
				ID:   uuid.UUID(a.ID.Bytes).String(),
				Name: a.Name,
			})
		}
	} else {
		c.Logger().Errorf("[FS-0274] new txn: ListAccounts: %v", err)
	}
	if rows, err := q.ListPos(ctx); err == nil {
		for _, p := range rows {
			if p.Currency != "idr" {
				continue // form is IDR-only
			}
			data.PosOptions = append(data.PosOptions, template.PosOption{
				ID:       uuid.UUID(p.ID.Bytes).String(),
				Name:     p.Name,
				Currency: p.Currency,
			})
		}
	} else {
		c.Logger().Errorf("[FS-0275] new txn: ListPos: %v", err)
	}
}

func newTxnTitle(t string) string {
	if t == "money_in" {
		return "New income"
	}
	return "New spending"
}
