-- 0005_pos_account.sql — move account ownership off transactions, onto Pos.
--
-- Per spec §4.2 and §5.6, each Pos lives in exactly one IDR Account, and
-- `pos.account_id` is mutable with snapshot semantics: changing it
-- retroactively re-attributes the Pos's historical money_in / money_out
-- account flows to the new Account. The audited entity is the Pos; the
-- Account is a derived current-state view (§10.5).
--
-- Schema impact:
--   * pos gets a NOT NULL account_id FK.
--   * transactions loses account_id (and the index, and the CHECK clause
--     that pinned it). account_amount stays — it's still the IDR
--     contribution, especially material for non-IDR Pos.
--
-- Backfill strategy: every existing money_in / money_out has an
-- account_id. For each Pos:
--   1. If it has any transactions, pick the account_id from the
--      earliest one (deterministic + matches the operator's intent for
--      the opening balance).
--   2. Otherwise fall back to the *most-used* non-archived account —
--      the one with the most existing transactions, ties broken by
--      created_at. This avoids landing zero-txn Pos on a stray empty
--      account that happened to be created first.
--
-- The operator can move any Pos later via spec §5.6.

BEGIN;

-- 1. Add column nullable so we can backfill in one statement.
ALTER TABLE pos ADD COLUMN account_id uuid REFERENCES accounts(id);

-- 2. Backfill. Must succeed for every Pos before the NOT NULL flips on,
--    so we error loudly later if no account exists at all.
UPDATE pos
SET account_id = COALESCE(
    (
        SELECT t.account_id
        FROM transactions t
        WHERE t.pos_id = pos.id
          AND t.account_id IS NOT NULL
        ORDER BY t.effective_date ASC, t.created_at ASC
        LIMIT 1
    ),
    (
        SELECT a.id
        FROM accounts a
        LEFT JOIN transactions t ON t.account_id = a.id
        WHERE NOT a.archived
        GROUP BY a.id, a.created_at
        ORDER BY COUNT(t.id) DESC, a.created_at ASC
        LIMIT 1
    )
);

-- Fail fast on any Pos still missing an account: the migration cannot
-- recover from "no accounts in the system" automatically.
DO $$
DECLARE
    missing int;
BEGIN
    SELECT count(*) INTO missing FROM pos WHERE account_id IS NULL;
    IF missing > 0 THEN
        RAISE EXCEPTION 'pos.account_id backfill incomplete: % rows still NULL (no accounts available?)', missing;
    END IF;
END $$;

ALTER TABLE pos ALTER COLUMN account_id SET NOT NULL;
CREATE INDEX pos_account_id_idx ON pos (account_id);

-- 3. Drop transactions.account_id and rebuild the CHECK constraint
--    without the account_id clause. The original CHECK was anonymous;
--    we drop it via the table-level constraint catalog.
DROP INDEX IF EXISTS transactions_account_id_idx;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_check;
ALTER TABLE transactions DROP COLUMN account_id;
ALTER TABLE transactions ADD CONSTRAINT transactions_money_shape_check CHECK (
    type IN ('money_in', 'money_out')
    AND account_amount IS NOT NULL
    AND account_amount > 0
    AND pos_id IS NOT NULL
    AND pos_amount IS NOT NULL
    AND pos_amount > 0
    AND counterparty_id IS NOT NULL
);

COMMIT;
