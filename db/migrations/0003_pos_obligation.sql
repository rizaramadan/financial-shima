-- 0003_pos_obligation.sql — Phase-8 borrow-mode debt tracking (§4.3).
--
-- Spec §10.7: cleared_at is set if and only if amount_repaid >= amount_owed.
-- The CHECK below pins the iff at the storage layer.

CREATE TABLE pos_obligation (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id  uuid        NOT NULL REFERENCES transactions(id),
    creditor_pos_id uuid        NOT NULL REFERENCES pos(id),
    debtor_pos_id   uuid        NOT NULL REFERENCES pos(id),
    -- §4.3: obligation is in the debtor's currency.
    currency        text        NOT NULL CHECK (currency ~ '^[a-z0-9-]+$'),
    amount_owed     bigint      NOT NULL CHECK (amount_owed > 0),
    -- amount_repaid <= amount_owed: overpayment must spawn a reverse
    -- obligation (logic/obligation.MatchRepayments), never overshoot
    -- the original. Skeet R5 — without this, a buggy update path could
    -- set repaid past owed and still pass the iff below.
    amount_repaid   bigint      NOT NULL DEFAULT 0
                                CHECK (amount_repaid >= 0 AND amount_repaid <= amount_owed),
    cleared_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- Spec §10.7 in the equality-of-iff form (Ive R8):
    -- "cleared_at is set" iff "repaid >= owed". The boolean equality is
    -- well-defined here because the right side is non-null and the
    -- amount_repaid <= amount_owed CHECK above means repaid >= owed
    -- collapses to repaid = owed in this row's lifetime.
    CONSTRAINT pos_obligation_cleared_iff_repaid_meets_owed CHECK (
        (cleared_at IS NOT NULL) = (amount_repaid >= amount_owed)
    ),
    CHECK (creditor_pos_id <> debtor_pos_id)
);
CREATE INDEX pos_obligation_creditor_debtor_idx
    ON pos_obligation (creditor_pos_id, debtor_pos_id, created_at)
    WHERE cleared_at IS NULL;
CREATE INDEX pos_obligation_transaction_id_idx ON pos_obligation (transaction_id);
