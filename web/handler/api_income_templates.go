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
// PercentBps is nil for fixed-only lines; 1..10000 (basis points
// of the incoming amount) for percent lines.
type APIIncomeTemplateLine struct {
	ID         string    `json:"id"`
	PosID      string    `json:"pos_id"`
	Amount     int64     `json:"amount"`
	PercentBps *int32    `json:"percent_bps,omitempty"`
	SortOrder  int32     `json:"sort_order"`
	CreatedAt  time.Time `json:"created_at"`
}

// createIncomeTemplateRequest is the body for POST. Lines are inline
// so the operator creates a complete template in one round-trip.
type createIncomeTemplateRequest struct {
	Name          string                          `json:"name"`
	LeftoverPosID string                          `json:"leftover_pos_id,omitempty"`
	Lines         []createIncomeTemplateLineInput `json:"lines"`
}

type createIncomeTemplateLineInput struct {
	PosID      string `json:"pos_id"`
	Amount     int64  `json:"amount"`
	PercentBps *int32 `json:"percent_bps"`
	SortOrder  int32  `json:"sort_order"`
}

// validateTemplateLines enforces the per-line shape (fixed, percent,
// or both — never neither) and the Σ(percents) ≤ 100% invariant, so
// POST and PATCH share one rule. Returns the total basis points.
func validateTemplateLines(lines []createIncomeTemplateLineInput) (int64, *string) {
	var totalBps int64
	for i, l := range lines {
		if _, err := uuid.Parse(l.PosID); err != nil {
			msg := "line " + itoa(i+1) + ": pos_id must be a valid UUID"
			return 0, &msg
		}
		if l.Amount < 0 {
			msg := "line " + itoa(i+1) + ": amount must not be negative"
			return 0, &msg
		}
		hasPercent := l.PercentBps != nil
		if hasPercent && (*l.PercentBps < 1 || *l.PercentBps > 10000) {
			msg := "line " + itoa(i+1) + ": percent_bps must be between 1 and 10000"
			return 0, &msg
		}
		if l.Amount == 0 && !hasPercent {
			msg := "line " + itoa(i+1) + ": amount must be positive for fixed lines (or set percent_bps)"
			return 0, &msg
		}
		if hasPercent {
			totalBps += int64(*l.PercentBps)
		}
	}
	if totalBps > 10000 {
		msg := "total percent_bps exceeds 10000 (100%)"
		return 0, &msg
	}
	return totalBps, nil
}

// applyIncomeTemplateRequest is the body for POST /apply. The
// "incoming" event the template will fan out into N money_in rows.
//
// account_id is no longer accepted — each generated row credits the
// Pos's *current* account_id (spec §4.2/§5.6). If the operator wants
// "this salary into BCA", they ensure each line's Pos points at BCA
// via PATCH /api/v1/pos/:id beforehand.
type applyIncomeTemplateRequest struct {
	Amount           int64  `json:"amount"`
	EffectiveDate    string `json:"effective_date"` // YYYY-MM-DD
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
					ID:         uuid.UUID(l.ID.Bytes).String(),
					PosID:      uuid.UUID(l.PosID.Bytes).String(),
					Amount:     l.Amount,
					PercentBps: l.PercentBps,
					SortOrder:  l.SortOrder,
					CreatedAt:  l.CreatedAt.Time,
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
	if _, msg := validateTemplateLines(req.Lines); msg != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, *msg)
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
			PercentBps: l.PercentBps,
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
			ID:         uuid.UUID(row.ID.Bytes).String(),
			PosID:      uuid.UUID(row.PosID.Bytes).String(),
			Amount:     row.Amount,
			PercentBps: row.PercentBps,
			SortOrder:  row.SortOrder,
			CreatedAt:  row.CreatedAt.Time,
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

	logicTmpl := logicTemplateFromRows(tmpl, lineRows)
	posCurrencies := map[string]string{}

	allocations, err := logictpl.Apply(logicTmpl, req.Amount)
	if err != nil {
		return applyLogicError(c, err)
	}

	// Resolve every Pos so we know its currency AND its current funding
	// account (§4.2: each Pos has its own account_id). Each generated
	// money_in row credits the Pos's account, not a single shared
	// account — accounts may differ across the template's lines.
	posAccountID := map[string]pgtype.UUID{}
	posAccountArchived := map[string]bool{}
	posAccountUUID := map[string]uuid.UUID{}
	accountCache := map[pgtype.UUID]dbq.Account{}
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
		posAccountID[a.PosID] = pos.AccountID
		acc, ok := accountCache[pos.AccountID]
		if !ok {
			acc, err = q.GetAccount(ctx, pos.AccountID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return mw.WriteAPIError(c, http.StatusNotFound,
						mw.APIErrorCodeNotFound, "pos's account not found")
				}
				c.Logger().Errorf("apply: get pos account: %v", err)
				return mw.WriteAPIError(c, http.StatusInternalServerError,
					mw.APIErrorCodeInternal, "failed to resolve pos's account")
			}
			accountCache[pos.AccountID] = acc
		}
		posAccountArchived[a.PosID] = acc.Archived
		posAccountUUID[a.PosID] = uuid.UUID(acc.ID.Bytes)
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
		// AccountAmount == PosAmount for IDR. Account flows through
		// the Pos's account_id (§4.2/§5.6).
		in := logictxn.MoneyInput{
			EffectiveDate: effDate,
			AccountAmount: money.New(a.Amount, "idr"),
			Pos: logictxn.PosRef{
				ID:              a.PosID,
				Currency:        "idr",
				AccountID:       posAccountUUID[a.PosID].String(),
				AccountArchived: posAccountArchived[a.PosID],
			},
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

// updateIncomeTemplateRequest is the body for PATCH
// /api/v1/income-templates/:id. Every field is optional; omitted
// fields keep their current value. lines, when present, replaces
// the whole line set (same delete + re-insert as the web edit
// flow). leftover_pos_id "" clears the leftover Pos.
type updateIncomeTemplateRequest struct {
	Name          *string                          `json:"name"`
	LeftoverPosID *string                          `json:"leftover_pos_id"`
	Lines         *[]createIncomeTemplateLineInput `json:"lines"`
}

// APIIncomeTemplateUpdate implements PATCH /api/v1/income-templates/:id.
func (h *Handlers) APIIncomeTemplateUpdate(c echo.Context) error {
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

	var req updateIncomeTemplateRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "name must not be empty")
	}
	if req.Lines != nil {
		if len(*req.Lines) == 0 {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "at least one line is required")
		}
		if _, msg := validateTemplateLines(*req.Lines); msg != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, *msg)
		}
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	tmpl, err := q.GetIncomeTemplate(ctx, pgtype.UUID{Bytes: templateID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "income template not found")
		}
		c.Logger().Errorf("api update template: get: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load income template")
	}

	name := tmpl.Name
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	leftoverParam := tmpl.LeftoverPosID
	if req.LeftoverPosID != nil {
		leftoverParam = pgtype.UUID{}
		if strings.TrimSpace(*req.LeftoverPosID) != "" {
			id, err := uuid.Parse(strings.TrimSpace(*req.LeftoverPosID))
			if err != nil {
				return mw.WriteAPIError(c, http.StatusBadRequest,
					mw.APIErrorCodeValidation, "leftover_pos_id must be a valid UUID")
			}
			leftoverParam = pgtype.UUID{Bytes: id, Valid: true}
		}
	}

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
	tq := dbq.New(tx)

	if err := tq.UpdateIncomeTemplate(ctx, dbq.UpdateIncomeTemplateParams{
		ID: tmpl.ID, Name: name, LeftoverPosID: leftoverParam,
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return mw.WriteAPIError(c, http.StatusConflict,
				mw.APIErrorCodeConflict,
				"an income template with that name already exists")
		}
		c.Logger().Errorf("api update template: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to update income template")
	}

	if req.Lines != nil {
		if err := tq.DeleteIncomeTemplateLinesByTemplate(ctx, tmpl.ID); err != nil {
			c.Logger().Errorf("clear lines: %v", err)
			return mw.WriteAPIError(c, http.StatusInternalServerError,
				mw.APIErrorCodeInternal, "failed to replace template lines")
		}
		for _, l := range *req.Lines {
			posID, _ := uuid.Parse(l.PosID)
			if _, err := tq.AddIncomeTemplateLine(ctx, dbq.AddIncomeTemplateLineParams{
				TemplateID: tmpl.ID,
				PosID:      pgtype.UUID{Bytes: posID, Valid: true},
				Amount:     l.Amount,
				PercentBps: l.PercentBps,
				SortOrder:  l.SortOrder,
			}); err != nil {
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
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.Logger().Errorf("commit: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to commit template")
	}
	committed = true

	updated, err := q.GetIncomeTemplate(ctx, tmpl.ID)
	if err != nil {
		c.Logger().Errorf("reload template: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to reload income template")
	}
	lineRows, err := q.ListIncomeTemplateLines(ctx, tmpl.ID)
	if err != nil {
		c.Logger().Errorf("reload lines: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to reload template lines")
	}
	return c.JSON(http.StatusOK, apiTemplateFromRows(updated, lineRows))
}

// previewIncomeTemplateRequest is the body for POST
// /api/v1/income-templates/:id/preview. Pure computation — no rows
// are written, no counterparty is resolved.
type previewIncomeTemplateRequest struct {
	Amount int64 `json:"amount"`
}

// APIIncomeTemplatePreview implements POST
// /api/v1/income-templates/:id/preview. Runs the template's Apply
// logic for the given amount and returns the suggested split so an
// API caller can show it before POSTing /apply.
func (h *Handlers) APIIncomeTemplatePreview(c echo.Context) error {
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

	var req previewIncomeTemplateRequest
	if err := decodeJSONStrict(c.Request().Body, &req); err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "invalid JSON body: "+err.Error())
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	q := dbq.New(h.DB)

	tmpl, err := q.GetIncomeTemplate(ctx, pgtype.UUID{Bytes: templateID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mw.WriteAPIError(c, http.StatusNotFound,
				mw.APIErrorCodeNotFound, "income template not found")
		}
		c.Logger().Errorf("api preview: get template: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load income template")
	}
	lineRows, err := q.ListIncomeTemplateLines(ctx, tmpl.ID)
	if err != nil {
		c.Logger().Errorf("api preview: list lines: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to load template lines")
	}

	logicTmpl := logicTemplateFromRows(tmpl, lineRows)
	allocations, err := logictpl.Apply(logicTmpl, req.Amount)
	if err != nil {
		return applyLogicError(c, err)
	}

	var total int64
	for _, a := range allocations {
		total += a.Amount
	}
	return c.JSON(http.StatusOK, map[string]any{
		"template_id": logicTmpl.ID,
		"amount":      req.Amount,
		"total":       total,
		"allocations": allocations,
	})
}

// logicTemplateFromRows folds DB rows into the pure logic shape so
// preview and apply share one mapping.
func logicTemplateFromRows(tmpl dbq.IncomeTemplate, lineRows []dbq.IncomeTemplateLine) logictpl.Template {
	out := logictpl.Template{
		ID:    uuid.UUID(tmpl.ID.Bytes).String(),
		Name:  tmpl.Name,
		Lines: make([]logictpl.Line, 0, len(lineRows)),
	}
	if tmpl.LeftoverPosID.Valid {
		out.LeftoverPosID = uuid.UUID(tmpl.LeftoverPosID.Bytes).String()
		out.HasLeftoverPos = true
	}
	for _, l := range lineRows {
		line := logictpl.Line{
			ID:     uuid.UUID(l.ID.Bytes).String(),
			PosID:  uuid.UUID(l.PosID.Bytes).String(),
			Amount: l.Amount,
		}
		if l.PercentBps != nil {
			line.PercentBps = int(*l.PercentBps)
		}
		out.Lines = append(out.Lines, line)
	}
	return out
}

// apiTemplateFromRows folds DB rows into the JSON shape so list,
// create, and update share one mapping.
func apiTemplateFromRows(tmpl dbq.IncomeTemplate, lineRows []dbq.IncomeTemplateLine) APIIncomeTemplate {
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
	for _, l := range lineRows {
		out.Lines = append(out.Lines, APIIncomeTemplateLine{
			ID:         uuid.UUID(l.ID.Bytes).String(),
			PosID:      uuid.UUID(l.PosID.Bytes).String(),
			Amount:     l.Amount,
			PercentBps: l.PercentBps,
			SortOrder:  l.SortOrder,
			CreatedAt:  l.CreatedAt.Time,
		})
	}
	return out
}

// itoa avoids strconv import for the small per-line index we render.
func applyLogicError(c echo.Context, err error) error {
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
	case errors.Is(err, logictpl.ErrInvalidPercent):
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"template has a line with percent_bps outside 1..10000")
	case errors.Is(err, logictpl.ErrPercentTotalExceeds):
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"template total percent exceeds 100%")
	case errors.Is(err, logictpl.ErrInvalidLine):
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			"template has a line with neither a positive amount nor a percent")
	default:
		c.Logger().Errorf("apply: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "apply failed")
	}
}
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
