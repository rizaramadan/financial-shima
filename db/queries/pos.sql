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

-- name: UpdatePosNameAndTarget :one
-- Rename a Pos and/or change its budget target. Currency is
-- intentionally NOT mutable here — changing it would re-bucket every
-- past transaction's pos_amount semantics, which is the kind of
-- balance-mutating UPDATE spec §10.3 forbids. Callers wanting a
-- different currency archive this Pos and create a new one.
UPDATE pos SET name = $2, target = $3 WHERE id = $1
RETURNING *;

-- name: SearchPos :many
-- Case-insensitive substring search by name for the global search box.
-- Active Pos only; capped to keep the results panel balanced.
SELECT * FROM pos
WHERE NOT archived
  AND lower(name) LIKE '%' || lower($1) || '%'
ORDER BY currency, name
LIMIT 10;
