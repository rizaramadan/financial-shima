-- name: InsertMoneyTransaction :one
-- Insert a money_in / money_out row. Idempotency: ON CONFLICT on
-- idempotency_key returns the existing row; the (xmax = 0) projection
-- distinguishes a fresh insert (was_inserted=true) from a conflict
-- (was_inserted=false), so the application can skip the notification
-- loop on idempotent re-submission per spec §10.8. Duplicate POSTs
-- silently return the original record per spec §7.2.
INSERT INTO transactions (
    type, effective_date,
    account_id, account_amount,
    pos_id, pos_amount,
    counterparty_id, note,
    source, created_by, idempotency_key,
    reverses_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (idempotency_key)
DO UPDATE SET idempotency_key = transactions.idempotency_key  -- no-op so RETURNING fires
RETURNING transactions.*, (xmax = 0) AS was_inserted;

-- name: GetTransaction :one
SELECT * FROM transactions WHERE id = $1;

-- name: ListTransactionsByAccount :many
SELECT * FROM transactions
WHERE account_id = $1
ORDER BY effective_date DESC, created_at DESC;

-- name: InsertNotification :one
INSERT INTO notifications (user_id, type, title, body, related_transaction_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListNotificationsForUser :many
SELECT * FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT 100;

-- name: MarkNotificationRead :exec
UPDATE notifications SET read_at = now()
WHERE id = $1 AND user_id = $2 AND read_at IS NULL;

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications SET read_at = now()
WHERE user_id = $1 AND read_at IS NULL;

-- name: UnreadCount :one
SELECT count(*) FROM notifications
WHERE user_id = $1 AND read_at IS NULL;
