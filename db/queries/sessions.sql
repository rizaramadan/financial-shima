-- name: CreateSession :one
INSERT INTO sessions (token, user_id, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetSession :one
SELECT s.token, s.user_id, s.issued_at, s.expires_at,
       u.id AS u_id, u.display_name, u.telegram_identifier
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token = $1 AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = $1;

-- name: PurgeExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();
