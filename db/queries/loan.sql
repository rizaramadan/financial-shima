-- name: SetPosIsLoan :exec
UPDATE pos SET is_loan = $2 WHERE id = $1;

-- name: ListLoanPos :many
SELECT * FROM pos WHERE is_loan AND NOT archived ORDER BY name;

-- name: CreateLoanAccess :one
INSERT INTO loan_access (pos_id, username, password_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetLoanAccessByPos :one
SELECT * FROM loan_access WHERE pos_id = $1;

-- name: GetLoanAccessByUsername :one
SELECT * FROM loan_access WHERE username = $1;

-- name: UpdateLoanAccessPassword :exec
UPDATE loan_access SET password_hash = $2 WHERE pos_id = $1;

-- name: CreateLoanSession :one
INSERT INTO loan_session (token, pos_id, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetLoanSession :one
-- Live (unexpired) borrower session. Caller checks pos_id == route :id.
SELECT token, pos_id, issued_at, expires_at
FROM loan_session
WHERE token = $1 AND expires_at > now();

-- name: DeleteLoanSession :exec
DELETE FROM loan_session WHERE token = $1;

-- name: PurgeExpiredLoanSessions :execrows
DELETE FROM loan_session WHERE expires_at <= now();

-- name: CreateLoanSubmission :one
INSERT INTO loan_payment_submission (pos_id, payer_name, amount, effective_date, note)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetLoanSubmission :one
SELECT * FROM loan_payment_submission WHERE id = $1;

-- name: ListLoanSubmissionsByPos :many
SELECT * FROM loan_payment_submission
WHERE pos_id = $1
ORDER BY submitted_at DESC
LIMIT 100;

-- name: ListPendingLoanSubmissionsByPos :many
SELECT * FROM loan_payment_submission
WHERE pos_id = $1 AND status = 'pending'
ORDER BY submitted_at DESC;

-- name: CountPendingLoanSubmissionsByPos :one
SELECT count(*) FROM loan_payment_submission
WHERE pos_id = $1 AND status = 'pending';

-- name: CancelLoanSubmission :execrows
-- Borrower-initiated cancel: only their own Pos's pending rows. pos_id is
-- passed (not just id) so a borrower can't cancel another loan's submission.
UPDATE loan_payment_submission
SET status = 'cancelled', decided_at = now()
WHERE id = $1 AND pos_id = $2 AND status = 'pending';

-- name: RejectLoanSubmission :execrows
UPDATE loan_payment_submission
SET status = 'rejected', decided_at = now(), decided_by = $2, reject_reason = $3
WHERE id = $1 AND status = 'pending';

-- name: ApproveLoanSubmission :execrows
-- Flip pending → approved and link the appended money_in. Guarded on
-- status = 'pending' so a double-approve is a no-op (0 rows affected). The
-- caller wraps this with the transaction insert in a single DB tx.
UPDATE loan_payment_submission
SET status = 'approved', decided_at = now(), decided_by = $2, transaction_id = $3
WHERE id = $1 AND status = 'pending';
