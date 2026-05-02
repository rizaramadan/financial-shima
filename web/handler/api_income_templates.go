package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	"github.com/rizaramadan/financial-shima/logic/money"
	"github.com/rizaramadan/financial-shima/logic/notification"
	logictxn "github.com/rizaramadan/financial-shima/logic/transaction"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APIIncomeTemplate is the JSON shape for one template + its lines.
type APIIncomeTemplate struct {
	ID            string                  `json:"id"`
	Name          string                  `json:"name"`
	LeftoverPosID string                  `json:"leftover_pos_id,omitempty"`
	Archived      bool                    `json:"archived"`
	CreatedAt     time.Time               `json:"created_at"`
	Lines         []APIIncomeTemplateLine `json:"lines"`
}

// APIIncomeTemplateLine is one allocation entry on a template.
type APIIncomeTemplateLine struct {
	ID        string    `json:"id"`
	PosID     string    `json:"pos_id"`
	Amount    int64     `json:"amount"`
	SortOrder int32     `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
}

// createIncomeTemplateRequest is the body for POST. Lines are inline
// so the operator creates a complete template in one round-trip.
type createIncomeTemplateRequest struct {
	Name          string                          `json:"name"`
	LeftoverPosID string                          `json:"leftover_pos_id,omitempty"`
	Lines         []createIncomeTemplateLineInput `json:"lines"`
}

type createIncomeTemplateLineInput struct {
	PosID     string `json:"pos_id"`
	Amount    int64  `json:"amount"`
	SortOrder int32  `json:"sort_order"`
}

// applyIncomeTemplateRequest is the body for POST /apply. The
// "incoming" event the template will fan out into N money_in rows.
type applyIncomeTemplateRequest struct {
	Amount           int64  `json:"amount"`
	EffectiveDate    string `json:"effective_date"` // YYYY-MM-DD
	AccountID        string `json:"account_id"`
	CounterpartyID   string `json:"counterparty_id,omitempty"`
	CounterpartyName string `json:"counterparty_name,omitempty"`
	Note             string `json:"note,omitempty"`
	IdempotencyKey   string `json:"idempotency_key"`
}

// APIIncomeTemplatesList implements GET /api/v1/income-templates.
func (h *Handlers) APIIncomeTemplatesList(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	rows, err := q.ListIncomeTemplates(ctx)
	if err != nil {
		c.Logger().Errorf("api list income-templates: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to list income templates")
	}
	out := make([]APIIncomeTemplate, 0, len(rows))
	for _, r := range rows {
		t := APIIncomeTemplate{
			ID:        uuid.UUID(r.ID.Bytes).String(),
			Name:      r.Name,
			Archived:  r.Archived,
			CreatedAt: r.CreatedAt.Time,
			Lines:     []APIIncomeTemplateLine{},
		}
		if r.LeftoverPosID.Valid {
			t.LeftoverPosID = uuid.UUID(r.LeftoverPosID.Bytes).String()
		}
		lineRows, err := q.ListIncomeTemplateLines(ctx, r.ID)
		if err != nil {
			c.Logger().Errorf("list lines for %s: %v", t.ID, err)
		} else {
			for _, l := range lineRows {
				t.Lines = append(t.Lines, APIIncomeTemplateLine{
					ID:        uuid.UUID(l.ID.Bytes).String(),
					PosID:     uuid.UUID(l.PosID.Bytes).String(),
					Amount:    l.Amount,
					SortOrder: l.SortOrder,
					CreatedAt: l.CreatedAt.Time,
				})
			}
		}
		out = append(out, t)
	}
	return c.JSON(http.StatusOK, out)
}

// APIIncomeTemplatesCreate implements POST /api/v1/income-templates.
//
// Creates the template + all its lines in one DB transaction so
// partial creates can't leave a template with no lines (which Apply
// would reject as ErrEmptyTemplate anyway, but cleaner to never
// reach that state).
func (h *Handlers) APIIncomeTemplatesCreate(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	var req createIncomeTemplateRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "name is required")
	}
	if len(req.Lines) == 0 {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"at least one line is required")
	}
	for i, l := range req.Lines {
		if l.Amount <= 0 {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation,
				"line "+itoa(i+1)+": amount must be positive")
		}
		if _, err := uuid.Parse(l.PosID); err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation,
				"line "+itoa(i+1)+": pos_id must be a valid UUID")
		}
	}

	var leftoverParam pgtype.UUID
	if req.LeftoverPosID != "" {
		id, err := uuid.Parse(req.LeftoverPosID)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "leftover_pos_id must be a valid UUID")
		}
		leftoverParam = pgtype.UUID{Bytes: id, Valid: true}
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		c.Logger().Errorf("begin tx: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "could not start transaction")
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	q := dbq.New(tx)

	tmpl, err := q.CreateIncomeTemplate(ctx, dbq.CreateIncomeTemplateParams{
		Name:          name,
		LeftoverPosID: leftoverParam,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return mw.WriteAPIError(c, http.StatusConflict,
				mw.APIErrorCodeConflict,
				"an income template with that name already exists")
		}
		c.Logger().Errorf("api create income-template: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to create income template")
	}

	out := APIIncomeTemplate{
		ID:        uuid.UUID(tmpl.ID.Bytes).String(),
		Name:      tmpl.Name,
		Archived:  tmpl.Archived,
		CreatedAt: tmpl.CreatedAt.Time,
		Lines:     []APIIncomeTemplateLine{},
	}
	if tmpl.LeftoverPosID.Valid {
		out.LeftoverPosID = uuid.UUID(tmpl.LeftoverPosID.Bytes).String()
	}

	for _, l := range req.Lines {
		posID, _ := uuid.Parse(l.PosID)
		row, err := q.AddIncomeTemplateLine(ctx, dbq.AddIncomeTemplateLineParams{
			TemplateID: tmpl.ID,
			PosID:      pgtype.UUID{Bytes: posID, Valid: true},
			Amount:     l.Amount,
			SortOrder:  l.SortOrder,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return mw.WriteAPIError(c, http.StatusConflict,
					mw.APIErrorCodeConflict,
					"duplicate Pos in template lines")
			}
			c.Logger().Errorf("add line: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to add template line")
		}
		out.Lines = append(out.Lines, APIIncomeTemplateLine{
			ID:        uuid.UUID(row.ID.Bytes).String(),
			PosID:     uuid.UUID(row.PosID.Bytes).String(),
			Amount:    row.Amount,
			SortOrder: row.SortOrder,
			CreatedAt: row.CreatedAt.Time,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		c.Logger().Errorf("commit: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to commit template")
	}
	committed = true
	return c.JSON(http.StatusCreated, out)
}

// APIIncomeTemplateApply implements POST /api/v1/income-templates/:id/apply.
//
// Expands the template into N money_in rows in a single DB
// transaction. Each generated row gets an idempotency key derived
// from the request key + the line id (or "leftover"), so
// re-submitting the same apply request returns identical IDs and
// fires no duplicate notifications.
func (h *Handlers) APIIncomeTemplateApply(c echo.Context) error {
	if h.DB == nil {
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	templateID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "id must be a valid UUID")
	}

	var req applyIncomeTemplateRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "idempotency_key is required")
	}
	effDate, err := time.Parse("2006-01-02", req.EffectiveDate)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"effective_date must be YYYY-MM-DD: "+err.Error())
	}
	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "account_id must be a valid UUID")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	tmpl, err := q.GetIncomeTemplate(ctx, pgtype.UUID{Bytes: templateID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "income template not found")
		}
		c.Logger().Errorf("api apply: get template: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load template")
	}
	lineRows, err := q.ListIncomeTemplateLines(ctx, tmpl.ID)
	if err != nil {
		c.Logger().Errorf("api apply: list lines: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load template lines")
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
	posCurrencies := map[string]string{}
	for _, l := range lineRows {
		posID := uuid.UUID(l.PosID.Bytes).String()
		logicTmpl.Lines = append(logicTmpl.Lines, logictpl.Line{
			ID:     uuid.UUID(l.ID.Bytes).String(),
			PosID:  posID,
			Amount: l.Amount,
		})
	}

	allocations, err := logictpl.Apply(logicTmpl, req.Amount)
	if err != nil {
		switch {
		case errors.Is(err, logictpl.ErrAmountBelowTemplate):
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation,
				"amount is less than the sum of template lines")
		case errors.Is(err, logictpl.ErrAmountExceedsTemplate):
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation,
				"amount exceeds template total and template has no leftover Pos configured")
		case errors.Is(err, logictpl.ErrEmptyTemplate):
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "template has no lines")
		case errors.Is(err, logictpl.ErrNonPositiveAmount):
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "amount must be positive")
		default:
			c.Logger().Errorf("apply: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "apply failed")
		}
	}

	// Resolve every Pos so we know its currency for §5.1 validation
	// per line. (Bulk-fetch to keep round-trips bounded.)
	for _, a := range allocations {
		if _, ok := posCurrencies[a.PosID]; ok {
			continue
		}
		posID, _ := uuid.Parse(a.PosID)
		pos, err := q.GetPos(ctx, pgtype.UUID{Bytes: posID, Valid: true})
		if err != nil {
			c.Logger().Errorf("apply: get pos %s: %v", a.PosID, err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to resolve Pos for allocation")
		}
		posCurrencies[a.PosID] = pos.Currency
	}

	// Resolve account.
	account, err := q.GetAccount(ctx, pgtype.UUID{Bytes: accountID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "account not found")
		}
		c.Logger().Errorf("apply: get account: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to resolve account")
	}

	// Resolve counterparty (id or name).
	var counterpartyID uuid.UUID
	var counterpartyName string
	switch {
	case req.CounterpartyID != "":
		cpID, err := uuid.Parse(req.CounterpartyID)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "counterparty_id must be a valid UUID")
		}
		var name string
		if err := h.DB.QueryRow(ctx,
			`SELECT name FROM counterparties WHERE id = $1`,
			pgtype.UUID{Bytes: cpID, Valid: true},
		).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return mw.WriteAPIError(c, http.StatusNotFound,
					mw.APIErrorCodeNotFound, "counterparty not found")
			}
			c.Logger().Errorf("apply: lookup cp: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to resolve counterparty")
		}
		counterpartyID = cpID
		counterpartyName = name
	case strings.TrimSpace(req.CounterpartyName) != "":
		row, err := q.GetOrCreateCounterparty(ctx, strings.TrimSpace(req.CounterpartyName))
		if err != nil {
			c.Logger().Errorf("apply: get-or-create cp: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to resolve counterparty")
		}
		counterpartyID = uuid.UUID(row.ID.Bytes)
		counterpartyName = row.Name
	default:
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"either counterparty_id or counterparty_name is required")
	}

	// Validate each generated allocation against §5.1 BEFORE inserting,
	// so a partially-applied template never lands in the DB. Account
	// is IDR per §4.1; for IDR-Pos lines we additionally need
	// account_amount == pos_amount (already true by construction since
	// we don't do FX conversion in the template). Non-IDR Pos lines
	// would require a separate FX path — out of scope; reject them
	// with a clear error rather than silently miscompute.
	for _, a := range allocations {
		ccy := posCurrencies[a.PosID]
		if ccy != "idr" {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation,
				"income templates currently support IDR Pos only; line for non-IDR Pos rejected")
		}
		// AccountID and PosID stable; AccountAmount == PosAmount for IDR.
		in := logictxn.MoneyInput{
			EffectiveDate: effDate,
			Account: logictxn.AccountRef{
				ID:       uuid.UUID(account.ID.Bytes).String(),
				Archived: account.Archived,
			},
			AccountAmount:    money.New(a.Amount, "idr"),
			Pos:              logictxn.PosRef{ID: a.PosID, Currency: "idr"},
			PosAmount:        money.New(a.Amount, "idr"),
			CounterpartyName: counterpartyName,
		}
		if violations := logictxn.ValidateMoneyIn(in, time.Now()); len(violations) > 0 {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, violations[0])
		}
	}

	// All allocations pass; expand into ledger inserts. ledger.Insert
	// runs each in its own DB tx (with notifications); a failure
	// half-way leaves earlier rows committed. To get all-or-nothing,
	// we'd extend ledger with InsertBatch; deferred. Idempotency keys
	// keep retries safe — re-applying the same request returns
	// identical txn IDs.
	svc := &ledger.Service{Pool: h.DB, Users: h.Auth.Users}
	insertedIDs := make([]string, 0, len(allocations))
	for _, a := range allocations {
		posID, _ := uuid.Parse(a.PosID)
		txnInput := ledger.MoneyTxnInput{
			Type:           "money_in",
			EffectiveDate:  pgtype.Date{Time: effDate, Valid: true},
			AccountID:      accountID,
			AccountAmount:  a.Amount,
			PosID:          posID,
			PosAmount:      a.Amount,
			CounterpartyID: counterpartyID,
			Note:           req.Note,
			Source:         notification.SourceAPI,
			CreatedBy:      nil,
			IdempotencyKey: req.IdempotencyKey + ":" + a.LineID,
		}
		txnID, err := svc.Insert(ctx, txnInput)
		if err != nil {
			c.Logger().Errorf("apply: ledger insert (line %s): %v", a.LineID, err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal,
				"partial apply: line "+a.LineID+" failed; check idempotency_key to retry safely")
		}
		insertedIDs = append(insertedIDs, txnID.String())
	}

	return c.JSON(http.StatusCreated, map[string]any{
		"template_id":     logicTmpl.ID,
		"applied_amount":  req.Amount,
		"transaction_ids": insertedIDs,
		"allocations":     allocations,
	})
}

// itoa avoids strconv import for the small per-line index we render.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// silence the json import if no other consumer references it
var _ = json.Marshal
