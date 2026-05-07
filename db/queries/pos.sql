-- name: CreatePos :one
INSERT INTO pos (name, currency, account_id, target)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetPos :one
SELECT * FROM pos WHERE id = $1;

-- name: ListPos :many
SELECT * FROM pos WHERE NOT archived ORDER BY currency, name, id;

-- name: ListPosIncludingArchived :many
SELECT * FROM pos ORDER BY currency, name, id;

-- name: ArchivePos :exec
UPDATE pos SET archived = true WHERE id = $1;

-- name: UpdatePosAccount :one
-- Reassign a Pos to a different Account. Snapshot semantics per spec
-- §5.6: every historical money_in / money_out for this Pos is re-
-- attributed to the new Account on the next balance read; no ledger
-- entry is written. Returns the updated Pos so the caller can echo it.
UPDATE pos SET account_id = $2 WHERE id = $1
RETURNING *;
