-- Loan-to-other feature.
--
-- A "loan Pos" tracks money lent to an outside borrower. Lifecycle (§model
-- confirmed with operator): the Pos is funded (money_in) then disbursed
-- (money_out) so its balance settles at 0, then climbs back toward
-- pos.target as approved repayments land as money_in. The balance therefore
-- represents "repaid so far" (0 → target); outstanding = target − balance.
-- is_loan flags these so they can be excluded from spending/budget rollups.
ALTER TABLE pos ADD COLUMN is_loan boolean NOT NULL DEFAULT false;

-- Per-loan borrower credentials — one shared login per loan Pos. Password is
-- bcrypt-hashed. The row's existence is what grants a Pos borrower access.
CREATE TABLE loan_access (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    pos_id        uuid        NOT NULL UNIQUE REFERENCES pos(id) ON DELETE CASCADE,
    username      text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Borrower web sessions — DB-backed (independent of the in-memory family
-- auth store) so they survive restarts. Each session is bound to exactly one
-- pos_id; middleware re-checks it against the :id in every /loan/:id route.
CREATE TABLE loan_session (
    token      text        PRIMARY KEY,
    pos_id     uuid        NOT NULL REFERENCES pos(id) ON DELETE CASCADE,
    issued_at  timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX loan_session_expires_at_idx ON loan_session (expires_at);

-- Borrower-submitted repayments awaiting family approval. Approval appends a
-- money_in transaction (transaction_id) and flips status; the submission row
-- itself never moves the balance — the ledger entry does.
CREATE TYPE loan_submission_status AS ENUM ('pending', 'approved', 'rejected', 'cancelled');

CREATE TABLE loan_payment_submission (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    pos_id         uuid        NOT NULL REFERENCES pos(id) ON DELETE CASCADE,
    payer_name     text        NOT NULL,
    amount         bigint      NOT NULL CHECK (amount > 0),
    effective_date date        NOT NULL,
    note           text,
    status         loan_submission_status NOT NULL DEFAULT 'pending',
    submitted_at   timestamptz NOT NULL DEFAULT now(),
    decided_at     timestamptz,
    decided_by     uuid        REFERENCES users(id),
    reject_reason  text,
    transaction_id uuid        REFERENCES transactions(id)
);
CREATE INDEX loan_payment_submission_pos_status_idx
    ON loan_payment_submission (pos_id, status);
