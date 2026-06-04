-- name: CreateAccount :one
INSERT INTO accounts (name) VALUES ($1) RETURNING *;

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = $1;

-- name: ListAccounts :many
SELECT * FROM accounts WHERE NOT archived ORDER BY name;

-- name: ListAccountsIncludingArchived :many
SELECT * FROM accounts ORDER BY archived, name;

-- name: ArchiveAccount :exec
UPDATE accounts SET archived = true WHERE id = $1;

-- name: DeleteAccountIfUnused :execrows
-- Hard-delete an account if no Pos references it. Safe because
-- transactions.account_id was dropped in 0005 (Pos now owns the
-- account FK), so pos is the only inbound reference. Returns rows
-- affected: 0 means the account is either missing or still has Pos —
-- the caller disambiguates with a follow-up GetAccount.
DELETE FROM accounts
WHERE accounts.id = $1
  AND NOT EXISTS (SELECT 1 FROM pos WHERE pos.account_id = accounts.id);

-- name: UpdateAccountName :one
-- Rename an account. Account.name is free text per spec §4.1; no
-- uniqueness constraint, so a rename to a clashing name is allowed.
UPDATE accounts SET name = $2 WHERE id = $1
RETURNING *;

-- name: SearchAccounts :many
-- Case-insensitive substring search by name for the global search box.
-- Active accounts only; capped so one entity can't flood the results.
SELECT * FROM accounts
WHERE NOT archived
  AND lower(name) LIKE '%' || lower($1) || '%'
ORDER BY name
LIMIT 10;
