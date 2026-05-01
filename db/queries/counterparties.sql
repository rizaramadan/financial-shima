-- name: GetOrCreateCounterparty :one
-- Insert or return existing by case-insensitive name match.
-- name_lower is generated; the unique constraint on it dedupes.
INSERT INTO counterparties (name)
VALUES ($1)
ON CONFLICT (name_lower) DO UPDATE
  SET name = counterparties.name  -- preserve original casing per spec §4.4
RETURNING *;

-- name: ListCounterparties :many
SELECT * FROM counterparties ORDER BY name_lower;

-- name: SearchCounterparties :many
SELECT * FROM counterparties
WHERE name_lower LIKE lower($1) || '%'
ORDER BY name_lower
LIMIT 20;
