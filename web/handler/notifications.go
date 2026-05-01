package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/db/dbq"
	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// NotificationsGet renders the per-user feed (spec §6.5).
//
// The list shows newest-first, unread rows visually distinguished from read.
// Each row links to its related transaction (when set). When DB is unwired,
// the page renders an empty state — same convention as HomeGet.
func (h *Handlers) NotificationsGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}

	data := template.NotificationsData{
		Title:       "Notifications",
		DisplayName: u.DisplayName,
	}
	if h.DB == nil {
		return c.Render(http.StatusOK, "notifications", data)
	}

	uid, err := uuid.Parse(u.ID)
	if err != nil {
		// Phase-2 in-memory users carry string IDs ("riza"/"shima"); the
		// DB-resolved user IDs are uuids. Until the auth path is wired
		// through DB, this can't render real notifications. Render the
		// empty state honestly.
		return c.Render(http.StatusOK, "notifications", data)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	rows, err := q.ListNotificationsForUser(ctx, pgtype.UUID{Bytes: uid, Valid: true})
	if err != nil {
		c.Logger().Errorf("ListNotificationsForUser: %v", err)
		data.LoadError = true
		return c.Render(http.StatusOK, "notifications", data)
	}

	for _, n := range rows {
		var (
			body          string
			relatedTxnID  string
			hasRelated    bool
			isRead        bool
		)
		if n.Body != nil {
			body = *n.Body
		}
		if n.RelatedTransactionID.Valid {
			relatedTxnID = uuid.UUID(n.RelatedTransactionID.Bytes).String()
			hasRelated = true
		}
		if n.ReadAt.Valid {
			isRead = true
		}
		data.Items = append(data.Items, template.NotificationRow{
			ID:           uuid.UUID(n.ID.Bytes).String(),
			Title:        n.Title,
			Body:         body,
			HasRelated:   hasRelated,
			RelatedTxnID: relatedTxnID,
			IsRead:       isRead,
			CreatedAt:    n.CreatedAt.Time,
		})
		if !isRead {
			data.UnreadCount++
		}
	}
	return c.Render(http.StatusOK, "notifications", data)
}

// NotificationMarkRead marks a single notification as read for the
// current user, then redirects back to the feed (or the related
// transaction if set in the form).
func (h *Handlers) NotificationMarkRead(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	if h.DB == nil {
		return c.Redirect(http.StatusSeeOther, "/notifications")
	}
	uid, err := uuid.Parse(u.ID)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/notifications")
	}
	nid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/notifications")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	if err := q.MarkNotificationRead(ctx, dbq.MarkNotificationReadParams{
		ID:     pgtype.UUID{Bytes: nid, Valid: true},
		UserID: pgtype.UUID{Bytes: uid, Valid: true},
	}); err != nil {
		c.Logger().Errorf("MarkNotificationRead: %v", err)
	}
	return c.Redirect(http.StatusSeeOther, "/notifications")
}

// loadBellCount fetches the unread count for the current user. Used by
// authenticated handlers that need to populate the layout's bell badge
// (spec §6.5: "Unread count surfaces in the global header"). Returns 0
// silently on any failure path so the bell never breaks the page render.
func (h *Handlers) loadBellCount(ctx context.Context, c echo.Context, uidStr string) int {
	if h.DB == nil {
		return 0
	}
	uid, err := uuid.Parse(uidStr)
	if err != nil {
		return 0
	}
	count, err := dbq.New(h.DB).UnreadCount(ctx, pgtype.UUID{Bytes: uid, Valid: true})
	if err != nil {
		c.Logger().Errorf("UnreadCount: %v", err)
		return 0
	}
	return int(count)
}

// NotificationsMarkAllRead bulk-marks unread notifications. Used by the
// "Mark all read" button at the top of the feed.
func (h *Handlers) NotificationsMarkAllRead(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	if h.DB == nil {
		return c.Redirect(http.StatusSeeOther, "/notifications")
	}
	uid, err := uuid.Parse(u.ID)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/notifications")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	q := dbq.New(h.DB)
	if _, err := q.MarkAllNotificationsRead(ctx, pgtype.UUID{Bytes: uid, Valid: true}); err != nil {
		c.Logger().Errorf("MarkAllNotificationsRead: %v", err)
	}
	return c.Redirect(http.StatusSeeOther, "/notifications")
}
