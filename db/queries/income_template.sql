-- name: CreateIncomeTemplate :one
INSERT INTO income_template (name, leftover_pos_id)
VALUES ($1, $2)
RETURNING *;

-- name: GetIncomeTemplate :one
SELECT * FROM income_template WHERE id = $1;

-- name: ListIncomeTemplates :many
SELECT * FROM income_template
WHERE NOT archived
ORDER BY name;

-- name: ArchiveIncomeTemplate :exec
UPDATE income_template SET archived = true WHERE id = $1;

-- name: AddIncomeTemplateLine :one
INSERT INTO income_template_line (template_id, pos_id, amount, sort_order)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListIncomeTemplateLines :many
-- Returns lines in display order (sort_order, then created_at as
-- tiebreaker). Caller folds these into the apply allocation.
SELECT * FROM income_template_line
WHERE template_id = $1
ORDER BY sort_order, created_at;

-- name: SumIncomeTemplateLines :one
-- Total of all lines on the template — used to validate that the
-- incoming amount meets or exceeds the template's required allocation.
SELECT COALESCE(SUM(amount), 0)::bigint AS total
FROM income_template_line
WHERE template_id = $1;

-- name: DeleteIncomeTemplateLine :exec
DELETE FROM income_template_line WHERE id = $1;
