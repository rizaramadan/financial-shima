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

// IncomeTemplatePreviewPost — Step 1 of the human-in-the-loop apply
// flow. Receives the operator's intent (amount/date/account/
// counterparty), runs the template's allocation logic to produce a
// SUGGESTED breakdown, and renders an editable preview page so the
// human can adjust before approving.
//
// This step has no side effects — no transactions are created, no
// counterparty rows are written. The preview is pure presentation.
//
// Apply itself happens in [IncomeTemplateApplyPost], which receives
// the (possibly user-adjusted) per-row allocation as form fields.
func (h *Handlers) IncomeTemplatePreviewPost(c echo.Context) error {
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
		return flashTo(c, tmplID, "Amount must be a positive whole number.")
	}
	effDateStr := strings.TrimSpace(c.FormValue("effective_date"))
	effDate, err := time.Parse("2006-01-02", effDateStr)
	if err != nil {
		return flashTo(c, tmplID, "Effective date is required (YYYY-MM-DD).")
	}
	accountIDStr := strings.TrimSpace(c.FormValue("account_id"))
	if _, err := uuid.Parse(accountIDStr); err != nil {
		return flashTo(c, tmplID, "Account is required.")
	}
	cpName := strings.TrimSpace(c.FormValue("counterparty_name"))
	if cpName == "" {
		return flashTo(c, tmplID, "Counterparty is required.")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	tmpl, err := q.GetIncomeTemplate(ctx, pgtype.UUID{Bytes: tmplID, Valid: true})
	if err != nil {
		return flashTo(c, tmplID, "Template not found.")
	}
	lineRows, err := q.ListIncomeTemplateLines(ctx, tmpl.ID)
	if err != nil {
		return flashTo(c, tmplID, "Could not load template lines.")
	}

	// Compute the suggestion via the pure logic package.
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
	allocations, allocErr := logictpl.Apply(logicTmpl, amount)
	// allocErr is informational here — even if Apply rejects (e.g.
	// amount > Σ(lines) without leftover), we still render the page
	// so the human can adjust toward a valid total. Show the
	// suggestion notice and let them decide.
	suggestionNotice := ""
	if allocErr != nil {
		switch {
		case errors.Is(allocErr, logictpl.ErrAmountBelowTemplate):
			suggestionNotice = "Heads up: the entered amount is BELOW the template's lines total. Adjust amounts so they sum to your salary, or change the salary amount."
		case errors.Is(allocErr, logictpl.ErrAmountExceedsTemplate):
			suggestionNotice = "Heads up: the entered amount EXCEEDS the template's lines total and there's no leftover Pos configured. Add a row for the surplus before approving."
		default:
			suggestionNotice = allocErr.Error()
		}
		// Fall back to the raw template lines as the starting suggestion.
		allocations = nil
		for _, l := range logicTmpl.Lines {
			allocations = append(allocations, logictpl.Allocation{
				LineID: l.ID, PosID: l.PosID, Amount: l.Amount,
			})
		}
	}

	// Build the suggested rows for rendering. Pad with empty rows so
	// the operator has slots to add more lines without dynamic JS.
	const editableRows = 10
	posByID, posOpts, err := loadPosOptions(ctx, q)
	if err != nil {
		return flashTo(c, tmplID, "Could not load Pos list.")
	}
	rows := make([]template.IncomeAllocationRow, editableRows)
	for i, a := range allocations {
		if i >= editableRows {
			break
		}
		opt := posByID[a.PosID]
		rows[i] = template.IncomeAllocationRow{
			PosID:    a.PosID,
			PosLabel: opt.Name + " (" + opt.Currency + ")",
			Amount:   strconv.FormatInt(a.Amount, 10),
		}
	}

	// Stable idempotency root for this preview → apply trip. Round-
	// trips through a hidden field so re-approving the preview (back
	// button, double-click) dedups on the existing transactions.
	idemKey := strings.TrimSpace(c.FormValue("idempotency_key"))
	if idemKey == "" {
		idemKey = uuid.NewString()
	}

	data := template.IncomeTemplatePreviewData{
		Title:            "Review allocation — " + tmpl.Name,
		DisplayName:      u.DisplayName,
		ID:               tmplID.String(),
		TemplateName:     tmpl.Name,
		Amount:           amount,
		AmountRaw:        rawAmount,
		EffectiveDate:    effDateStr,
		AccountID:        accountIDStr,
		AccountName:      "", // resolved below
		CounterpartyName: cpName,
		IdempotencyKey:   idemKey,
		PosOptions:       posOpts,
		Rows:             rows,
		SuggestionNotice: suggestionNotice,
		UnreadCount:      h.loadBellCount(ctx, c, u.ID),
	}
	if accountID, err := uuid.Parse(accountIDStr); err == nil {
		if acc, err := q.GetAccount(ctx, pgtype.UUID{Bytes: accountID, Valid: true}); err == nil {
			data.AccountName = acc.Name
		}
	}
	// Derive the running expected total. If user-supplied amount is
	// less than Σ(lines) (the rejected case), we still show the lines
	// total alongside so the user can decide which way to converge.
	for _, a := range allocations {
		data.SuggestionTotal += a.Amount
	}
	_ = effDate
	return c.Render(http.StatusOK, "income_template_preview", data)
}

// loadPosOptions returns Pos info indexed by id (for display) and as
// a slice of options (for <select> rendering).
func loadPosOptions(ctx context.Context, q *dbq.Queries) (map[string]template.PosOption, []template.PosOption, error) {
	pos, err := q.ListPos(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]template.PosOption{}
	opts := make([]template.PosOption, 0, len(pos))
	for _, p := range pos {
		o := template.PosOption{
			ID:       uuid.UUID(p.ID.Bytes).String(),
			Name:     p.Name,
			Currency: p.Currency,
		}
		byID[o.ID] = o
		opts = append(opts, o)
	}
	return byID, opts, nil
}

func flashTo(c echo.Context, tmplID uuid.UUID, msg string) error {
	return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
		"?flash="+enc(msg))
}

// IncomeTemplateApplyPost — Step 2 of the human-in-the-loop apply
// flow. Receives the (possibly user-adjusted) per-row allocation
// from the preview form's hidden + visible fields and creates one
// money_in row per non-empty allocation row, atomically per row,
// with idempotency keys derived from the preview-stamped key + the
// row's pos_id.
//
// Validation:
//   - Σ(rows) must equal the entered amount (else re-render preview).
//   - Each filled row needs a valid pos_id (non-empty UUID) and amount > 0.
//   - No duplicate pos_id across rows (the per-pos idempotency key
//     would collide).
//   - At least one filled row.
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
		return flashTo(c, tmplID, "Amount must be a positive whole number.")
	}
	effDate, err := time.Parse("2006-01-02", strings.TrimSpace(c.FormValue("effective_date")))
	if err != nil {
		return flashTo(c, tmplID, "Effective date is required (YYYY-MM-DD).")
	}
	// Account is no longer accepted on the income-template apply form;
	// each generated row credits the Pos's *current* account_id
	// (spec §4.2/§5.6). The form will drop the account picker too.
	cpName := strings.TrimSpace(c.FormValue("counterparty_name"))
	if cpName == "" {
		return flashTo(c, tmplID, "Counterparty is required.")
	}
	idemKey := strings.TrimSpace(c.FormValue("idempotency_key"))
	if idemKey == "" {
		idemKey = uuid.NewString()
	}

	// Read the editable allocation rows.
	const maxRows = 10
	type row struct {
		PosID  uuid.UUID
		Amount int64
	}
	var rows []row
	seenPos := map[uuid.UUID]bool{}
	var sum int64
	for i := 0; i < maxRows; i++ {
		idx := strconv.Itoa(i)
		posStr := strings.TrimSpace(c.FormValue("alloc_pos_" + idx))
		amtStr := strings.TrimSpace(c.FormValue("alloc_amount_" + idx))
		if posStr == "" && amtStr == "" {
			continue
		}
		if posStr == "" || amtStr == "" {
			return flashTo(c, tmplID, "Row "+strconv.Itoa(i+1)+": Pos and amount are both required (or leave the row empty).")
		}
		pid, err := uuid.Parse(posStr)
		if err != nil {
			return flashTo(c, tmplID, "Row "+strconv.Itoa(i+1)+": invalid Pos.")
		}
		amt, err := strconv.ParseInt(amtStr, 10, 64)
		if err != nil || amt <= 0 {
			return flashTo(c, tmplID, "Row "+strconv.Itoa(i+1)+": amount must be a positive whole number.")
		}
		if seenPos[pid] {
			return flashTo(c, tmplID, "Row "+strconv.Itoa(i+1)+": this Pos already appears earlier — combine the amounts.")
		}
		seenPos[pid] = true
		rows = append(rows, row{PosID: pid, Amount: amt})
		sum += amt
	}
	if len(rows) == 0 {
		return flashTo(c, tmplID, "At least one allocation row is required.")
	}
	if sum != amount {
		return flashTo(c, tmplID,
			"Allocation sum ("+strconv.FormatInt(sum, 10)+") must equal the salary amount ("+rawAmount+"). Adjust rows and try again.")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	cpRow, err := q.GetOrCreateCounterparty(ctx, cpName)
	if err != nil {
		return flashTo(c, tmplID, "Counterparty resolve failed.")
	}
	cpID := uuid.UUID(cpRow.ID.Bytes)

	svc := &ledger.Service{Pool: h.DB, Users: h.Auth.Users}
	for _, r := range rows {
		_, err := svc.Insert(ctx, ledger.MoneyTxnInput{
			Type:           "money_in",
			EffectiveDate:  pgtype.Date{Time: effDate, Valid: true},
			AccountAmount:  r.Amount,
			PosID:          r.PosID,
			PosAmount:      r.Amount,
			CounterpartyID: cpID,
			Note:           "",
			Source:         notification.SourceWeb,
			CreatedBy:      parseOptUUID(u.ID),
			// Per-pos idempotency: re-approving the same preview yields
			// the same derived keys, so retries dedup on transactions.
			IdempotencyKey: idemKey + ":" + r.PosID.String(),
		})
		if err != nil {
			c.Logger().Errorf("apply row %s: %v", r.PosID, err)
			return flashTo(c, tmplID,
				"Partial apply: row for pos "+r.PosID.String()[:8]+
					" failed; re-approve with the same form to complete safely.")
		}
	}

	return c.Redirect(http.StatusSeeOther, "/income-templates/"+tmplID.String()+
		"?flash="+enc("Approved & applied "+rawAmount+" across "+strconv.Itoa(len(rows))+" Pos."))
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
