DROP TABLE IF EXISTS loan_payment_submission;
DROP TYPE IF EXISTS loan_submission_status;
DROP TABLE IF EXISTS loan_session;
DROP TABLE IF EXISTS loan_access;
ALTER TABLE pos DROP COLUMN IF EXISTS is_loan;
