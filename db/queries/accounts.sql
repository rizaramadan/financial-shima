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
