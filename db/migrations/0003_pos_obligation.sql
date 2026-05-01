-- 0003_pos_obligation.sql — Phase-8 borrow-mode debt tracking.
--
-- Spec §4.3 borrow-mode generates one row per (creditor, debtor) line pair.
-- Spec §10.7: cleared_at is set if and only if amount_repaid >= amount_owed.
-- The CHECK below pins the iff at the storage layer so the application's
-- update path can't drift from the invariant.
--
-- The Phase-6 transactions CHECK still locks `type` to money_in / money_out;
-- relaxing it to admit inter_pos lands when the ledger writes inter_pos
-- transactions (not yet — Phase 9/10 territory). For now, pos_obligation
-- rows are written by Phase-8 logic-layer functions whose output the
-- caller persists; the Phase-8 schema lets that persistence happen.

CREATE TABLE pos_obligation (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id  uuid        NOT NULL REFERENCES transactions(id),
    creditor_pos_id uuid        NOT NULL REFERENCES pos(id),
    debtor_pos_id   uuid        NOT NULL REFERENCES pos(id),
    -- §4.3: obligation is in the debtor's currency.
    currency        text        NOT NULL CHECK (currency ~ '^[a-z0-9-]+$'),
    amount_owed     bigint      NOT NULL CHECK (amount_owed > 0),
    amount_repaid   bigint      NOT NULL DEFAULT 0 CHECK (amount_repaid >= 0),
    cleared_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- Spec §10.7 enforced as a CHECK so a buggy update path can't leave
    -- the row in a "limbo" state.
    CONSTRAINT pos_obligation_cleared_iff_repaid_meets_owed CHECK (
        (cleared_at IS NULL AND amount_repaid <  amount_owed) OR
        (cleared_at IS NOT NULL AND amount_repaid >= amount_owed)
    ),
    CHECK (creditor_pos_id <> debtor_pos_id)
);
CREATE INDEX pos_obligation_creditor_debtor_idx
    ON pos_obligation (creditor_pos_id, debtor_pos_id, created_at)
    WHERE cleared_at IS NULL;
CREATE INDEX pos_obligation_transaction_id_idx ON pos_obligation (transaction_id);
