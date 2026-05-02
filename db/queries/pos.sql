-- name: CreatePos :one
INSERT INTO pos (name, currency, target)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetPos :one
SELECT * FROM pos WHERE id = $1;

-- name: ListPos :many
SELECT * FROM pos WHERE NOT archived ORDER BY currency, name, id;

-- name: ListPosIncludingArchived :many
SELECT * FROM pos ORDER BY currency, name, id;

-- name: ArchivePos :exec
UPDATE pos SET archived = true WHERE id = $1;
