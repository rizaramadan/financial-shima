-- 0002_transactions.sql — Phase-6 ledger primitives.
--
-- Per spec §10.3 the application never issues DELETE or balance-mutating
-- UPDATE on `transactions`; corrections are reversal rows pointing back
-- via `reverses_id`. The schema admits append-only writes naturally; a
-- future migration can revoke UPDATE/DELETE from the runtime role to
-- enforce this at the database layer.
--
-- inter_pos lines and obligations are intentionally omitted — they ship
-- in Phase 7-8 alongside their balance-computation tests. This migration
-- covers just the surface needed for money_in / money_out + the
-- notification atomicity rule (§5.4, §10.8).

CREATE TYPE transaction_type AS ENUM ('money_in', 'money_out', 'inter_pos');
CREATE TYPE transaction_source AS ENUM ('web', 'api', 'seed');
CREATE TYPE notification_type AS ENUM ('transaction_created');

CREATE TABLE transactions (
    id              uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
    type            transaction_type NOT NULL,
    -- Phase 7 will add `mode` for inter_pos.
    effective_date  date             NOT NULL,
    -- money_in / money_out fields (NULL for inter_pos in later phases).
    account_id      uuid             REFERENCES accounts(id),
    account_amount  bigint,
    pos_id          uuid             REFERENCES pos(id),
    pos_amount      bigint,
    counterparty_id uuid             REFERENCES counterparties(id),
    note            text,
    source          transaction_source NOT NULL,
    -- created_by is null for source='seed' or 'api' per spec §4.3.
    created_by      uuid             REFERENCES users(id),
    -- Spec §10.4: idempotency on every state-changing op.
    idempotency_key text             NOT NULL UNIQUE,
    created_at      timestamptz      NOT NULL DEFAULT now(),
    -- §5.2/5.3: corrections insert a reversal pointing here.
    reverses_id     uuid             REFERENCES transactions(id),

    -- Phase-6 shape constraint: money_in / money_out only.
    -- Phase 7 will relax this to cover inter_pos.
    CHECK (
        type IN ('money_in', 'money_out')
        AND account_id IS NOT NULL
        AND account_amount IS NOT NULL
        AND account_amount > 0
        AND pos_id IS NOT NULL
        AND pos_amount IS NOT NULL
        AND pos_amount > 0
        AND counterparty_id IS NOT NULL
    )
);
CREATE INDEX transactions_effective_date_idx ON transactions (effective_date DESC);
CREATE INDEX transactions_account_id_idx ON transactions (account_id);
CREATE INDEX transactions_pos_id_idx ON transactions (pos_id);
CREATE INDEX transactions_counterparty_id_idx ON transactions (counterparty_id);
CREATE INDEX transactions_reverses_id_idx ON transactions (reverses_id);

-- Spec §4.5 / §10.8: notifications and transactions are atomic. A
-- transaction row exists if and only if all of its notification rows
-- exist. Enforced by inserting both in the same DB transaction; the FK
-- below provides the half of the bicycle the DB can guarantee
-- (notification cannot reference a missing transaction).
CREATE TABLE notifications (
    id                     uuid              PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                uuid              NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type                   notification_type NOT NULL,
    title                  text              NOT NULL,
    body                   text,
    -- NOT NULL enforces half of §10.8 at the schema layer: a notification
    -- cannot exist without its transaction. The other half (transaction
    -- exists -> notifications exist) lives in the application's atomic
    -- insert path. Phase 9+ may add notification types that don't reference
    -- a transaction; that warrants a separate migration relaxing this.
    related_transaction_id uuid              NOT NULL REFERENCES transactions(id),
    read_at                timestamptz,
    created_at             timestamptz       NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_id_idx ON notifications (user_id);
CREATE INDEX notifications_user_unread_idx ON notifications (user_id) WHERE read_at IS NULL;
