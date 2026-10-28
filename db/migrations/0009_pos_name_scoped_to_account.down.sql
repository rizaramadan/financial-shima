-- 0009_pos_name_scoped_to_account.sql rollback: restore UNIQUE (name, currency).
ALTER TABLE pos DROP CONSTRAINT IF EXISTS pos_name_currency_account_key;
ALTER TABLE pos ADD CONSTRAINT pos_name_currency_key UNIQUE (name, currency);
