package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	"github.com/rizaramadan/financial-shima/dependencies/ledger"
	logictpl "github.com/rizaramadan/financial-shima/logic/incometemplate"
	"github.com/rizaramadan/financial-shima/logic/notification"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// IncomeTemplatesGet renders the list of templates.
func (h *Handlers) IncomeTemplatesGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	data := template.IncomeTemplatesListData{
		Title:       "Income templates",
		DisplayName: u.DisplayName,
	}
	if h.DB == nil {
		return c.Render(http.StatusOK, "income_templates", data)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	rows, err := q.ListIncomeTemplates(ctx)
	if err != nil {
		c.Logger().Errorf("list income templates: %v", err)
		data.LoadError = true
		return c.Render(http.StatusOK, "income_templates", data)
	}
	for _, r := range rows {
		t := template.IncomeTemplateRow{
			ID:   uuid.UUID(r.ID.Bytes).String(),
			Name: r.Name,
		}
		total, _ := q.SumIncomeTemplateLines(ctx, r.ID)
		t.Total = total
		data.Items = append(data.Items, t)
	}
	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	return c.Render(http.StatusOK, "income_templates", data)
}

// IncomeTemplateNewGet renders the create form. The form lets the
// operator name the template, optionally set a leftover Pos, and add
// up to 8 lines (Pos + amount). Empty lines are ignored on submit.
func (h *Handlers) IncomeTemplateNewGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	data := template.IncomeTemplateNewData{
		Title:       "New income template",
		DisplayName: u.DisplayName,
	}
	if h.DB == nil {
		return c.Render(http.StatusOK, "income_template_new", data)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	pos, err := q.ListPos(ctx)
	if err != nil {
		c.Logger().Errorf("list pos: %v", err)
		data.LoadError = true
		return c.Render(http.StatusOK, "income_template_new", data)
	}
	for _, p := range pos {
		data.Pos = append(data.Pos, template.PosOption{
			ID:       uuid.UUID(p.ID.Bytes).String(),
			Name:     p.Name,
			Currency: p.Currency,
		})
	}
	data.UnreadCount = h.loadBellCount(ctx, c, u.ID)
	return c.Render(http.StatusOK, "income_template_new", data)
}

// IncomeTemplateNewPost handles form submission. Reads up to 8 line
// rows (`pos_id_0..pos_id_7` / `amount_0..amount_7`); empty rows are
// ignored. On success redirects to the new template's view page.
func (h *Handlers) IncomeTemplateNewPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	if h.DB == nil {
		return c.String(http.StatusInternalServerError, "database not configured")
	}

	render := func(errs []string, name, leftover string, posIDs []string, amounts []string) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
		defer cancel()
		q := dbq.New(h.DB)
		pos, _ := q.ListPos(ctx)
		var posOpts []template.PosOption
		for _, p := range pos {
			posOpts = append(posOpts, template.PosOption{
				ID: uuid.UUID(p.ID.Bytes).String(), Name: p.Name, Currency: p.Currency,
			})
		}
		// Re-build line rows from raw form values.
		var lines []template.IncomeTemplateLineInput
		for i := range posIDs {
			lines = append(lines, template.IncomeTemplateLineInput{
				PosID: posIDs[i], Amount: amounts[i],
			})
		}
		return c.Render(http.StatusOK, "income_template_new", template.IncomeTemplateNewData{
			Title: "New income template", DisplayName: u.DisplayName,
			Name: name, LeftoverPosID: leftover,
			Pos: posOpts, Lines: lines, Errors: errs,
			UnreadCount: h.loadBellCount(ctx, c, u.ID),
		})
	}

	name := strings.TrimSpace(c.FormValue("name"))
	leftover := strings.TrimSpace(c.FormValue("leftover_pos_id"))

	const maxLines = 8
	posIDs := make([]string, maxLines)
	amounts := make([]string, maxLines)
	for i := 0; i < maxLines; i++ {
		posIDs[i] = strings.TrimSpace(c.FormValue("pos_id_" + strconv.Itoa(i)))
		amounts[i] = strings.TrimSpace(c.FormValue("amount_" + strconv.Itoa(i)))
	}

	var errs []string
	if name == "" {
		errs = append(errs, "Name is required.")
	}

	type lineParsed struct {
		PosID  uuid.UUID
		Amount int64
		Sort   int32
	}
	var parsedLines []lineParsed
	for i := 0; i < maxLines; i++ {
		if posIDs[i] == "" && amounts[i] == "" {
			continue // empty row — skip
		}
		if posIDs[i] == "" {
			errs = append(errs, "Line "+strconv.Itoa(i+1)+": Pos is required.")
			continue
		}
		if amounts[i] == "" {
			errs = append(errs, "Line "+strconv.Itoa(i+1)+": Amount is required.")
			continue
		}
		pid, err := uuid.Parse(posIDs[i])
		if err != nil {
			errs = append(errs, "Line "+strconv.Itoa(i+1)+": invalid Pos.")
			continue
		}
		amt, err := strconv.ParseInt(amounts[i], 10, 64)
		if err != nil || amt <= 0 {
			errs = append(errs, "Line "+strconv.Itoa(i+1)+": amount must be a positive whole number.")
			continue
		}
		parsedLines = append(parsedLines, lineParsed{PosID: pid, Amount: amt, Sort: int32(i)})
	}
	if len(parsedLines) == 0 && len(errs) == 0 {
		errs = append(errs, "Add at least one line (Pos + amount).")
	}

	var leftoverParam pgtype.UUID
	if leftover != "" {
		id, err := uuid.Parse(leftover)
		if err != nil {
			errs = append(errs, "Leftover Pos: invalid id.")
		} else {
			leftoverParam = pgtype.UUID{Bytes: id, Valid: true}
		}
	}

	if len(errs) > 0 {
		return render(errs, name, leftover, posIDs, amounts)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return render([]string{"Database error. Try again."}, name, leftover, posIDs, amounts)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	q := dbq.New(tx)
	tmpl, err := q.CreateIncomeTemplate(ctx, dbq.CreateIncomeTemplateParams{
		Name: name, LeftoverPosID: leftoverParam,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return render([]string{"A template with that name already exists."}, name, leftover, posIDs, amounts)
		}
		c.Logger().Errorf("create template: %v", err)
		return render([]string{"Could not save template."}, name, leftover, posIDs, amounts)
	}
	for _, l := range parsedLines {
		if _, err := q.AddIncomeTemplateLine(ctx, dbq.AddIncomeTemplateLineParams{
			TemplateID: tmpl.ID,
			PosID:      pgtype.UUID{Bytes: l.PosID, Valid: true},
			Amount:     l.Amount,
			SortOrder:  l.Sort,
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return render([]string{"Duplicate Pos in template lines."}, name, leftover, posIDs, amounts)
			}
			c.Logger().Errorf("add line: %v", err)
			return render([]string{"Could not save line."}, name, leftover, posIDs, amounts)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return render([]string{"Database error on commit."}, name, leftover, posIDs, amounts)
	}
	committed = true
	return c.Redirect(http.StatusSeeOther, "/income-templates/"+uuid.UUID(tmpl.ID.Bytes).String())
}

// IncomeTemplateGet renders one template's detail page with its
// allocation lines and the apply form.
func (h *Handlers) IncomeTemplateGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/income-templates")
	}
	if h.DB == nil {
		return c.String(http.StatusInternalServerError, "database not configured")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	tmpl, err := q.GetIncomeTemplate(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Redirect(http.StatusSeeOther, "/income-templates")
		}
		c.Logger().Errorf("get template: %v", err)
		return c.String(http.StatusInternalServerError, "could not load template")
	}
	lineRows, _ := q.ListIncomeTemplateLines(ctx, tmpl.ID)
	posByID := map[string]template.PosOption{}
	pos, _ := q.ListPos(ctx)
	for _, p := range pos {
		posByID[uuid.UUID(p.ID.Bytes).String()] = template.PosOption{
			ID: uuid.UUID(p.ID.Bytes).String(), Name: p.Name, Currency: p.Currency,
		}
	}
	accounts, _ := q.ListAccounts(ctx)
	var accountOpts []template.AccountOption
	for _, a := range accounts {
		accountOpts = append(accountOpts, template.AccountOption{
			ID: uuid.UUID(a.ID.Bytes).String(), Name: a.Name,
		})
	}

	data := template.IncomeTemplateDetailData{
		Title:       tmpl.Name,
		DisplayName: u.DisplayName,
		ID:          uuid.UUID(tmpl.ID.Bytes).String(),
		Name:        tmpl.Name,
		Accounts:    accountOpts,
		UnreadCount: h.loadBellCount(ctx, c, u.ID),
	}
	if tmpl.LeftoverPosID.Valid {
		opt := posByID[uuid.UUID(tmpl.LeftoverPosID.Bytes).String()]
		data.LeftoverPosName = opt.Name
		data.LeftoverPosID = opt.ID
		data.HasLeftoverPos = true
	}
	for _, l := range lineRows {
		opt := posByID[uuid.UUID(l.PosID.Bytes).String()]
		data.Lines = append(data.Lines, template.IncomeTemplateLineRow{
			PosName: opt.Name, PosCurrency: opt.Currency, Amount: l.Amount,
		})
		data.LinesTotal += l.Amount
	}
	// Surface a flash message on apply success / failure
	if msg := c.QueryParam("flash"); msg != "" {
		data.Flash = msg
	}
	return c.Render(http.StatusOK, "income_template_detail", data)
}

// IncomeTemplateApplyPost handles the apply form submission from the
// detail page. Mirrors the API path but consumes form-encoded input
// and redirects back to the detail page with a flash message.
func (h *Handlers) IncomeTemplateApplyPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	if h.DB == nil {
		return c.String(http.StatusInternalServerError, "database not configured")
	}

	tmplID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/income-templates")
	}

	rawAmount := strings.TrimSpace(c.FormValue("amount"))
	amount, err := strconv.ParseInt(rawAmount, 10, 64)
	if err != nil || amount <= 0 {
		return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
			"?flash="+enc("Amount must be a positive whole number."))
	}
	effDate, err := time.Parse("2006-01-02", strings.TrimSpace(c.FormValue("effective_date")))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
			"?flash="+enc("Effective date is required (YYYY-MM-DD)."))
	}
	accountID, err := uuid.Parse(strings.TrimSpace(c.FormValue("account_id")))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
			"?flash="+enc("Account is required."))
	}
	cpName := strings.TrimSpace(c.FormValue("counterparty_name"))
	if cpName == "" {
		return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
			"?flash="+enc("Counterparty is required."))
	}
	idemKey := strings.TrimSpace(c.FormValue("idempotency_key"))
	if idemKey == "" {
		// Auto-generate when the form omits it (typical for human
		// users who shouldn't be made to think about idempotency).
		idemKey = "web-" + tmplID.String() + "-" + effDate.Format("2006-01-02") + "-" +
			strconv.FormatInt(time.Now().UnixNano(), 36)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	tmpl, err := q.GetIncomeTemplate(ctx, pgtype.UUID{Bytes: tmplID, Valid: true})
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
			"?flash="+enc("Template not found."))
	}
	lineRows, err := q.ListIncomeTemplateLines(ctx, tmpl.ID)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
			"?flash="+enc("Could not load template lines."))
	}
	logicTmpl := logictpl.Template{
		ID:    uuid.UUID(tmpl.ID.Bytes).String(),
		Name:  tmpl.Name,
		Lines: make([]logictpl.Line, 0, len(lineRows)),
	}
	if tmpl.LeftoverPosID.Valid {
		logicTmpl.LeftoverPosID = uuid.UUID(tmpl.LeftoverPosID.Bytes).String()
		logicTmpl.HasLeftoverPos = true
	}
	for _, l := range lineRows {
		logicTmpl.Lines = append(logicTmpl.Lines, logictpl.Line{
			ID:     uuid.UUID(l.ID.Bytes).String(),
			PosID:  uuid.UUID(l.PosID.Bytes).String(),
			Amount: l.Amount,
		})
	}
	allocations, err := logictpl.Apply(logicTmpl, amount)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
			"?flash="+enc(err.Error()))
	}

	cpRow, err := q.GetOrCreateCounterparty(ctx, cpName)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
			"?flash="+enc("Counterparty resolve failed."))
	}
	cpID := uuid.UUID(cpRow.ID.Bytes)

	svc := &ledger.Service{Pool: h.DB, Users: h.Auth.Users}
	for _, a := range allocations {
		posID, _ := uuid.Parse(a.PosID)
		_, err := svc.Insert(ctx, ledger.MoneyTxnInput{
			Type:           "money_in",
			EffectiveDate:  pgtype.Date{Time: effDate, Valid: true},
			AccountID:      accountID,
			AccountAmount:  a.Amount,
			PosID:          posID,
			PosAmount:      a.Amount,
			CounterpartyID: cpID,
			Note:           "",
			Source:         notification.SourceWeb,
			CreatedBy:      parseOptUUID(u.ID),
			IdempotencyKey: idemKey + ":" + a.LineID,
		})
		if err != nil {
			c.Logger().Errorf("apply line %s: %v", a.LineID, err)
			return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
				"?flash="+enc("Partial apply: line "+a.LineID+" failed; retry the same form to complete safely."))
		}
	}

	return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
		"?flash="+enc("Applied "+rawAmount+" — created "+strconv.Itoa(len(allocations))+" transactions."))
}

func enc(s string) string {
	// Echo's c.Redirect doesn't auto-encode the URL; do a minimal
	// encode for the flash query param.
	return strings.NewReplacer(
		" ", "+",
		"\n", "%0A",
		"\"", "%22",
		"&", "%26",
		"?", "%3F",
		"#", "%23",
	).Replace(s)
}

func parseOptUUID(s string) *uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}
