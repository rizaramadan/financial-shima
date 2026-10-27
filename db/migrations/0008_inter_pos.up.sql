-- 0008_inter_pos.sql — Phase-7 inter_pos transfer header + lines.
--
-- An inter_pos transfer is ONE transactions row (type='inter_pos',
-- mode='reallocation'|'borrow') plus N inter_pos_lines rows. The header
-- carries no money fields of its own (those live on the lines), so the
-- money-only columns stay NULL and the mode column stays NULL on money
-- rows. Spec references: §4.3 (reallocation vs borrow), §10.6
-- (per-currency Σ(out)==Σ(in), enforced in logic/transaction, not here).
--
-- Borrow-mode debt rows (pos_obligation) are written by the same
-- application transaction that inserts the header + lines; the
-- pos_obligation table itself ships in 0003 and is untouched here.

ALTER TABLE transactions ADD COLUMN mode text;

-- Replace the money-only shape CHECK with one that admits both shapes.
ALTER TABLE transactions DROP CONSTRAINT transactions_money_shape_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_shape_check CHECK (
    (
        type IN ('money_in', 'money_out')
        AND mode IS NULL
        AND account_amount IS NOT NULL
        AND account_amount > 0
        AND pos_id IS NOT NULL
        AND pos_amount IS NOT NULL
        AND pos_amount > 0
        AND counterparty_id IS NOT NULL
    ) OR (
        type = 'inter_pos'
        AND mode IN ('reallocation', 'borrow')
        AND account_amount IS NULL
        AND pos_id IS NULL
        AND pos_amount IS NULL
        AND counterparty_id IS NULL
    )
);

CREATE TABLE inter_pos_lines (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id uuid        NOT NULL REFERENCES transactions(id),
    pos_id         uuid        NOT NULL REFERENCES pos(id),
    direction      text        NOT NULL CHECK (direction IN ('out', 'in')),
    -- Cents per CLAUDE.md (money is int64, never float64). Positive —
    -- the sign comes from the direction, mirroring money_in/_out.
    amount         bigint      NOT NULL CHECK (amount > 0),
    -- Lowercase per logic/money convention; must match the pos's own
    -- currency (enforced in logic/transaction.ValidateInterPos).
    currency       text        NOT NULL CHECK (currency ~ '^[a-z0-9-]+$'),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inter_pos_lines_transaction_id_idx ON inter_pos_lines (transaction_id);
CREATE INDEX inter_pos_lines_pos_id_idx ON inter_pos_lines (pos_id);
