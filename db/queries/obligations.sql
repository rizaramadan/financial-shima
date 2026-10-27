-- name: InsertObligation :one
-- One borrow-mode debt leg. Caller (ledger.Service.InsertInterPos)
-- derives the full leg set from logic/obligation.GenerateBorrowObligations
-- so the per-debtor sums stay exact; this query only persists rows.
INSERT INTO pos_obligation (transaction_id, creditor_pos_id, debtor_pos_id, currency, amount_owed)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListOpenObligations :many
-- Every open borrow-mode debt for GET /api/v1/debts: all
-- pos_obligation rows where cleared_at IS NULL, with the creditor and
-- debtor Pos names joined so the API caller never round-trips per row.
-- outstanding = amount_owed - amount_repaid is computed in
-- logic/obligation (pure), not here.
SELECT
    o.id, o.transaction_id,
    o.creditor_pos_id, o.debtor_pos_id,
    o.currency, o.amount_owed, o.amount_repaid,
    o.cleared_at, o.created_at,
    c.name AS creditor_pos_name,
    d.name AS debtor_pos_name
FROM pos_obligation o
JOIN pos c ON c.id = o.creditor_pos_id
JOIN pos d ON d.id = o.debtor_pos_id
WHERE o.cleared_at IS NULL
ORDER BY o.created_at DESC;
