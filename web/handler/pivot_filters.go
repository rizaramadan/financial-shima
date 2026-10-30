package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// pivotFilterKeys is the whitelist of query keys the /pivot client may
// persist. It mirrors exactly what syncURL emits — the saved value is the
// same query string, so a fresh visit replays it through the same parse
// path as a shared link.
var pivotFilterKeys = map[string]bool{
	"akun": true, "kat": true, "preset": true, "from": true, "to": true,
	"type": true, "q": true, "empty": true, "akuntotal": true,
	"sort": true, "filters": true, "fgroups": true, "collapsed": true,
}

// maxPivotFilterBytes caps the stored query string. A fully-collapsed view
// over ~70 Kategori serializes to ~2.5KB; 8KB leaves headroom without
// letting a rogue client stuff the row.
const maxPivotFilterBytes = 8 * 1024

// pivotFiltersBody is the POST /pivot/filters payload: the client's
// current filter query string (without the leading '?').
type pivotFiltersBody struct {
	Filters string `json:"filters"`
}

// PivotFiltersPost persists the caller's current /pivot filter view.
// Body: {"filters": "<query string>"}. Explicit URL params still win for
// the current view — this only records the state so the next fresh visit
// (no params) restores it. No-DB boot is a 204 no-op so local dev without
// Postgres keeps working.
func (h *Handlers) PivotFiltersPost(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	r := http.MaxBytesReader(c.Response().Writer, c.Request().Body, maxPivotFilterBytes)
	var body pivotFiltersBody
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return c.String(http.StatusBadRequest, "invalid JSON")
	}
	if len(body.Filters) > maxPivotFilterBytes {
		return c.String(http.StatusBadRequest, "filters too large")
	}
	if body.Filters != "" {
		q, err := url.ParseQuery(body.Filters)
		if err != nil {
			return c.String(http.StatusBadRequest, "invalid filters")
		}
		for k := range q {
			if !pivotFilterKeys[k] {
				return c.String(http.StatusBadRequest, "unknown filter key")
			}
		}
	}
	if h.DB == nil {
		return c.NoContent(http.StatusNoContent)
	}
	uid, err := uuid.Parse(u.ID)
	if err != nil {
		return c.String(http.StatusBadRequest, "unknown user")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	_, err = h.DB.Exec(ctx, `
		INSERT INTO pivot_filter_settings (user_id, filter_query, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE SET
			filter_query = EXCLUDED.filter_query,
			updated_at = now()`,
		uid, body.Filters)
	if err != nil {
		c.Logger().Errorf("PivotFiltersPost: %v", err)
		return c.String(http.StatusInternalServerError, "save failed")
	}
	return c.NoContent(http.StatusNoContent)
}

// loadPivotFilters returns the caller's saved /pivot filter query string
// ("" when none). Any failure degrades to "" so the page renders with
// defaults — a missing settings row must never break the report.
func (h *Handlers) loadPivotFilters(ctx context.Context, c echo.Context, uidStr string) string {
	if h.DB == nil {
		return ""
	}
	uid, err := uuid.Parse(uidStr)
	if err != nil {
		return ""
	}
	var q string
	err = h.DB.QueryRow(ctx,
		`SELECT filter_query FROM pivot_filter_settings WHERE user_id = $1`, uid,
	).Scan(&q)
	if err != nil {
		return ""
	}
	return q
}
