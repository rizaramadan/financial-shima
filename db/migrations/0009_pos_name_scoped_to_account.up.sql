-- 0009_pos_name_scoped_to_account.sql — allow duplicate Pos names across accounts.
--
-- Before: UNIQUE (name, currency) — a name could only exist once per
-- currency, no matter which Account funded it.
-- After: UNIQUE (name, currency, account_id) — the same name+currency
-- may repeat in different Accounts, but stays unique within one Account.
--
-- Strictly weaker than the old constraint, so existing rows can never
-- conflict; no backfill or dedup needed.
ALTER TABLE pos DROP CONSTRAINT IF EXISTS pos_name_currency_key;
ALTER TABLE pos ADD CONSTRAINT pos_name_currency_account_key UNIQUE (name, currency, account_id);
