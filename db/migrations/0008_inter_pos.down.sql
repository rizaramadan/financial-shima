-- 0008_inter_pos.sql rollback: drop the lines table, the mode
-- column, and restore the money-only shape CHECK from 0005.
DROP TABLE inter_pos_lines;
ALTER TABLE transactions DROP CONSTRAINT transactions_shape_check;
ALTER TABLE transactions DROP COLUMN mode;
ALTER TABLE transactions ADD CONSTRAINT transactions_money_shape_check CHECK (
    type IN ('money_in', 'money_out')
    AND account_amount IS NOT NULL
    AND account_amount > 0
    AND pos_id IS NOT NULL
    AND pos_amount IS NOT NULL
    AND pos_amount > 0
    AND counterparty_id IS NOT NULL
);
