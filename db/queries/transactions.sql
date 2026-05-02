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

-- name: ListTransactionsByPos :many
-- Chronological transactions touching this pos. Phase-7+ inter_pos lines
-- live in inter_pos_lines and are not yet wired; this query covers the
-- money_in / money_out path that Phase 6 ships.
SELECT
    t.id, t.type, t.effective_date,
    t.account_amount, t.pos_amount, t.note,
    t.created_at, t.reverses_id,
    a.name  AS account_name,
    cp.name AS counterparty_name
FROM transactions t
LEFT JOIN accounts       a  ON a.id  = t.account_id
LEFT JOIN counterparties cp ON cp.id = t.counterparty_id
WHERE t.pos_id = $1
ORDER BY t.effective_date DESC, t.created_at DESC, t.id DESC
LIMIT 200;

-- name: SumMoneyOutByPosMonth :many
-- Spending heatmap (§6.4): one row per (pos, month) summing pos_amount
-- for money_out transactions in the date range. Caller pivots to a
-- months × top-N-pos table and computes the top-N ranking from totals.
SELECT
    p.id        AS pos_id,
    p.name      AS pos_name,
    p.currency  AS pos_currency,
    date_trunc('month', t.effective_date)::date AS month,
    SUM(t.pos_amount)::bigint AS spent
FROM transactions t
JOIN pos p ON p.id = t.pos_id
WHERE t.type = 'money_out'
  AND t.effective_date >= $1
  AND t.effective_date <= $2
GROUP BY p.id, p.name, p.currency, month
ORDER BY month DESC, spent DESC;

-- name: SumAccountBalances :many
-- Per-account balance: signed sum of money_in / money_out account_amount
-- contributions per account_id, restricted to non-archived accounts. The
-- LEFT JOIN keeps zero-balance accounts in the result so the home view's
-- list of accounts matches ListAccounts row-for-row.
SELECT
    a.id,
    COALESCE(SUM(CASE
        WHEN t.type = 'money_in'  THEN t.account_amount
        WHEN t.type = 'money_out' THEN -t.account_amount
        ELSE 0
    END), 0)::bigint AS balance
FROM accounts a
LEFT JOIN transactions t ON t.account_id = a.id
WHERE NOT a.archived
GROUP BY a.id;

-- name: SumPosCashBalances :many
-- Per-pos cash balance: signed sum of money_in / money_out pos_amount per
-- (pos_id, currency). inter_pos lines are not yet wired (Phase 7+) so they
-- contribute zero. Currency comes from pos.currency since money_in/_out
-- always denominate pos_amount in the pos's currency (§4.3 / §5.1).
SELECT
    p.id,
    p.currency,
    COALESCE(SUM(CASE
        WHEN t.type = 'money_in'  THEN t.pos_amount
        WHEN t.type = 'money_out' THEN -t.pos_amount
        ELSE 0
    END), 0)::bigint AS balance
FROM pos p
LEFT JOIN transactions t ON t.pos_id = p.id
WHERE NOT p.archived
GROUP BY p.id, p.currency;

-- name: GetPosCashBalance :one
-- Single-pos variant of SumPosCashBalances for the §6.3 detail view.
SELECT COALESCE(SUM(CASE
    WHEN type = 'money_in'  THEN pos_amount
    WHEN type = 'money_out' THEN -pos_amount
    ELSE 0
END), 0)::bigint AS balance
FROM transactions
WHERE pos_id = $1;

-- name: ListObligationsForPos :many
-- Open obligations where this pos is creditor (money it's owed) or
-- debtor (money it owes). Counts toward Pos.receivables and
-- Pos.payables on the detail view per spec §4.2.
SELECT
    id, transaction_id,
    creditor_pos_id, debtor_pos_id,
    currency, amount_owed, amount_repaid,
    cleared_at, created_at
FROM pos_obligation
WHERE (creditor_pos_id = $1 OR debtor_pos_id = $1)
  AND cleared_at IS NULL
ORDER BY created_at DESC;

-- name: ListTransactionsByDateRange :many
-- Joined view for the §6.1 list: account.name, pos.name + currency,
-- counterparty.name. LEFT JOINs because Phase 7+ inter_pos rows have
-- NULL account_id / pos_id / counterparty_id (line items live in
-- inter_pos_lines — to be rendered when that phase ships).
SELECT
    t.id, t.type, t.effective_date,
    t.account_id, t.account_amount,
    t.pos_id, t.pos_amount,
    t.counterparty_id, t.note,
    t.source, t.created_by, t.idempotency_key,
    t.created_at, t.reverses_id,
    a.name  AS account_name,
    p.name  AS pos_name,
    p.currency AS pos_currency,
    cp.name AS counterparty_name
FROM transactions t
LEFT JOIN accounts       a  ON a.id  = t.account_id
LEFT JOIN pos            p  ON p.id  = t.pos_id
LEFT JOIN counterparties cp ON cp.id = t.counterparty_id
WHERE t.effective_date >= $1 AND t.effective_date <= $2
ORDER BY t.effective_date DESC, t.created_at DESC, t.id DESC
LIMIT 200;

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
