package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// APITransactionListItem mirrors APITransaction (POST response shape)
// but populated for the joined-list view: name fields beside ids so
// the caller doesn't have to round-trip on every row to resolve them.
type APITransactionListItem struct {
	ID               string    `json:"id"`
	Type             string    `json:"type"`
	EffectiveDate    string    `json:"effective_date"`
	AccountID        string    `json:"account_id,omitempty"`
	AccountName      string    `json:"account_name,omitempty"`
	AccountAmount    int64     `json:"account_amount"`
	PosID            string    `json:"pos_id,omitempty"`
	PosName          string    `json:"pos_name,omitempty"`
	PosCurrency      string    `json:"pos_currency,omitempty"`
	PosAmount        int64     `json:"pos_amount"`
	CounterpartyID   string    `json:"counterparty_id,omitempty"`
	CounterpartyName string    `json:"counterparty_name,omitempty"`
	Note             string    `json:"note,omitempty"`
	IsReversal       bool      `json:"is_reversal"`
	ReversesID       string    `json:"reverses_id,omitempty"`
	IdempotencyKey   string    `json:"idempotency_key"`
	CreatedAt        time.Time `json:"created_at"`
}

// APITransactionsList implements GET /api/v1/transactions per spec §7.2.
//
// Query parameters (all optional; AND-combined):
//
//	from=YYYY-MM-DD     defaults to 30 days ago
//	to=YYYY-MM-DD       defaults to today
//	type=money_in|money_out|inter_pos
//	account_id=<uuid>
//	pos_id=<uuid>
//	counterparty_id=<uuid>
//
// Server-side filters that are NOT yet pushed to a sqlc query (type +
// id filters) are applied in Go after the date-range fetch. Acceptable
// while the bound on the date-range is small (the underlying query
// caps at 200 rows). Pagination cursor follows in a later round.
//
// Errors:
//   - 503 service_unavailable: DB unwired.
//   - 400 validation_failed: malformed date / type / uuid.
//   - 500 internal_error: DB query failure.
func (h *Handlers) APITransactionsList(c echo.Context) error {
	if h.DB == nil {
		// Validation errors take precedence over 503 so a malformed
		// request lands on a 400 the caller can fix without a working
		// DB. (See the 400-vs-503 ordering tests.)
		if v := strings.TrimSpace(c.QueryParam("from")); v != "" {
			if _, err := time.Parse(dateLayout, v); err != nil {
				return mw.WriteAPIError(c, http.StatusBadRequest,
					mw.APIErrorCodeValidation, "from must be YYYY-MM-DD")
			}
		}
		if v := strings.TrimSpace(c.QueryParam("to")); v != "" {
			if _, err := time.Parse(dateLayout, v); err != nil {
				return mw.WriteAPIError(c, http.StatusBadRequest,
					mw.APIErrorCodeValidation, "to must be YYYY-MM-DD")
			}
		}
		if v := strings.TrimSpace(c.QueryParam("type")); v != "" {
			if v != "money_in" && v != "money_out" && v != "inter_pos" {
				return mw.WriteAPIError(c, http.StatusBadRequest,
					mw.APIErrorCodeValidation,
					`type must be "money_in", "money_out", or "inter_pos"`)
			}
		}
		return mw.WriteAPIError(c, http.StatusServiceUnavailable,
			mw.APIErrorCodeServiceUnavailable,
			"data layer not configured (DATABASE_URL unset)")
	}

	now := time.Now()
	from := now.AddDate(0, 0, -defaultTxnRangeDays)
	to := now
	if v := strings.TrimSpace(c.QueryParam("from")); v != "" {
		t, err := time.Parse(dateLayout, v)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "from must be YYYY-MM-DD")
		}
		from = t
	}
	if v := strings.TrimSpace(c.QueryParam("to")); v != "" {
		t, err := time.Parse(dateLayout, v)
		if err != nil {
			return mw.WriteAPIError(c, http.StatusBadRequest,
				mw.APIErrorCodeValidation, "to must be YYYY-MM-DD")
		}
		to = t
	}
	typeFilter := strings.TrimSpace(c.QueryParam("type"))
	if typeFilter != "" && typeFilter != "money_in" && typeFilter != "money_out" && typeFilter != "inter_pos" {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation,
			`type must be "money_in", "money_out", or "inter_pos"`)
	}

	parseUUIDQuery := func(name string) (uuid.UUID, bool, error) {
		raw := strings.TrimSpace(c.QueryParam(name))
		if raw == "" {
			return uuid.Nil, false, nil
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return uuid.Nil, true, err
		}
		return id, true, nil
	}
	accountID, hasAccount, err := parseUUIDQuery("account_id")
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "account_id must be a valid UUID")
	}
	posID, hasPos, err := parseUUIDQuery("pos_id")
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "pos_id must be a valid UUID")
	}
	cpID, hasCP, err := parseUUIDQuery("counterparty_id")
	if err != nil {
		return mw.WriteAPIError(c, http.StatusBadRequest,
			mw.APIErrorCodeValidation, "counterparty_id must be a valid UUID")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), listTimeout)
	defer cancel()
	q := dbq.New(h.DB)
	rows, err := q.ListTransactionsByDateRange(ctx, dbq.ListTransactionsByDateRangeParams{
		EffectiveDate:   pgtype.Date{Time: from, Valid: true},
		EffectiveDate_2: pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		c.Logger().Errorf("api list transactions: %v", err)
		return mw.WriteAPIError(c, http.StatusInternalServerError,
			mw.APIErrorCodeInternal, "failed to list transactions")
	}

	out := make([]APITransactionListItem, 0, len(rows))
	for _, r := range rows {
		// Apply post-fetch filters. Each is a fast equality check;
		// total work is O(rows) bounded by the SQL LIMIT (200).
		if typeFilter != "" && string(r.Type) != typeFilter {
			continue
		}
		if hasAccount && (!r.AccountID.Valid || r.AccountID.Bytes != accountID) {
			continue
		}
		if hasPos && (!r.PosID.Valid || r.PosID.Bytes != posID) {
			continue
		}
		if hasCP && (!r.CounterpartyID.Valid || r.CounterpartyID.Bytes != cpID) {
			continue
		}

		item := APITransactionListItem{
			ID:             uuid.UUID(r.ID.Bytes).String(),
			Type:           string(r.Type),
			EffectiveDate:  r.EffectiveDate.Time.Format(dateLayout),
			IdempotencyKey: r.IdempotencyKey,
			CreatedAt:      r.CreatedAt.Time,
		}
		if r.AccountID.Valid {
			item.AccountID = uuid.UUID(r.AccountID.Bytes).String()
		}
		if r.AccountAmount != nil {
			item.AccountAmount = *r.AccountAmount
		}
		if r.AccountName != nil {
			item.AccountName = *r.AccountName
		}
		if r.PosID.Valid {
			item.PosID = uuid.UUID(r.PosID.Bytes).String()
		}
		if r.PosAmount != nil {
			item.PosAmount = *r.PosAmount
		}
		if r.PosName != nil {
			item.PosName = *r.PosName
		}
		if r.PosCurrency != nil {
			item.PosCurrency = *r.PosCurrency
		}
		if r.CounterpartyID.Valid {
			item.CounterpartyID = uuid.UUID(r.CounterpartyID.Bytes).String()
		}
		if r.CounterpartyName != nil {
			item.CounterpartyName = *r.CounterpartyName
		}
		if r.Note != nil {
			item.Note = *r.Note
		}
		if r.ReversesID.Valid {
			item.IsReversal = true
			item.ReversesID = uuid.UUID(r.ReversesID.Bytes).String()
		}
		out = append(out, item)
	}
	return c.JSON(http.StatusOK, out)
}
