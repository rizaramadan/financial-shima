-- name: GetUserByTelegramIdentifier :one
SELECT * FROM users WHERE telegram_identifier = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY display_name;

-- name: UpsertUser :one
INSERT INTO users (display_name, telegram_identifier)
VALUES ($1, $2)
ON CONFLICT (telegram_identifier) DO UPDATE
  SET display_name = EXCLUDED.display_name
RETURNING *;
