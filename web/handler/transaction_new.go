package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/logic/notification"
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
		PosLabel:         strings.TrimSpace(c.FormValue("pos_label")),
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
	if in.PosLabel == "" {
		return rerender([]string{"Pos is required."})
	}
	amount, err := parseAmount(in.AmountRaw)
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

	// Resolve the typed Pos label → row. Web form is IDR-only and the
	// (name, currency) UNIQUE index makes the name unambiguous within
	// IDR. Match case-insensitively so the typeahead value doesn't have
	// to round-trip exact casing.
	posList, err := q.ListPos(ctx)
	if err != nil {
		c.Logger().Errorf("[FS-0276] new txn: ListPos: %v", err)
		return rerender([]string{"Couldn’t look up Pos. Try again."})
	}
	var pos dbq.Po
	var found bool
	for _, p := range posList {
		if p.Currency == "idr" && strings.EqualFold(p.Name, in.PosLabel) {
			pos = p
			found = true
			break
		}
	}
	if !found {
		return rerender([]string{"Pos not found. Pick one from the suggestions."})
	}
	posID := uuid.UUID(pos.ID.Bytes)
	account, err := q.GetAccount(ctx, pos.AccountID)
	if err != nil {
		c.Logger().Errorf("[FS-0270] new txn: GetAccount via pos: %v", err)
		return rerender([]string{"Account not found for this Pos."})
	}
	// Web form is IDR-only for now; reject cross-currency until we
	// design the FX UX. API can still do it directly.
	if account.Archived || pos.Archived {
		return rerender([]string{"Account and Pos must both be active (not archived)."})
	}
	if pos.Currency != "idr" {
		return rerender([]string{"Web form supports IDR only. Use the API for non-IDR Pos."})
	}

	// Shared resolve → validate → insert path (transaction_service.go):
	// the service re-resolves pos/account/counterparty by ID and runs
	// the same spec §5.1 validation the API uses, so the two entries
	// stay behavior-identical by construction.
	today := handlerNow(h)
	result, violations, err := createMoneyTransaction(ctx, h.DB, h.Auth.Users, today, MoneyCreateInput{
		Type:             in.Type,
		EffectiveDate:    effDate,
		AccountAmount:    amount,
		PosID:            posID,
		PosAmount:        amount,
		CounterpartyName: in.CounterpartyName,
		Note:             in.Note,
		Source:           notification.SourceWeb,
		CreatedBy:        parseOptUUID(u.ID),
		IdempotencyKey:   in.IdempotencyKey,
	})
	if err != nil {
		if IsMoneyNotFound(err) {
			return rerender([]string{err.Error()})
		}
		c.Logger().Errorf("[FS-0273] new txn: createMoneyTransaction: %v", err)
		return rerender([]string{"Couldn’t record the transaction. Try again."})
	}
	if len(violations) > 0 {
		return rerender(violations)
	}

	return c.Redirect(http.StatusSeeOther, "/pos/"+result.PosID)
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
	if data.Type == "money_in" {
		if tmpls, err := q.ListIncomeTemplates(ctx); err == nil {
			for _, t := range tmpls {
				data.Templates = append(data.Templates, template.IncomeTemplateOption{
					ID:   uuid.UUID(t.ID.Bytes).String(),
					Name: t.Name,
				})
			}
		} else {
			c.Logger().Errorf("[FS-0276] new txn: ListIncomeTemplates: %v", err)
		}
	}
}

func newTxnTitle(t string) string {
	if t == "money_in" {
		return "New income"
	}
	return "New spending"
}
