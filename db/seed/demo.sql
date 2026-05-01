-- db/seed/demo.sql — comprehensive sample data for screenshot/demo runs.
-- Resets the data tables (preserving migrations) and inserts a coherent
-- two-user, multi-account, multi-currency snapshot covering every page.
--
-- Balances are derived (no stored balance column — spec §4.2). Funding
-- events ("salary" money_in to a pos via the receiving account) seed
-- positive balances; expense events drain them. The shape is tuned so
-- every account and Pos lands at a sensible positive balance after the
-- event stream is folded.
--
-- Run via: psql $DATABASE_URL -f db/seed/demo.sql

BEGIN;

TRUNCATE notifications, pos_obligation, transactions,
         sessions, accounts, pos, counterparties RESTART IDENTITY CASCADE;
DELETE FROM users WHERE telegram_identifier NOT IN ('@riza_ramadan', '@shima');

INSERT INTO users (display_name, telegram_identifier) VALUES
    ('Riza',  '@riza_ramadan'),
    ('Shima', '@shima')
ON CONFLICT (telegram_identifier) DO UPDATE SET display_name = EXCLUDED.display_name;

INSERT INTO accounts (name) VALUES
    ('Riza — Cash'),
    ('Shima — BCA'),
    ('Joint — BCA');

INSERT INTO pos (name, currency, target) VALUES
    ('Belanja Bulanan', 'idr',     5000000),
    ('Mortgage',        'idr',    12000000),
    ('Anak Sekolah',    'idr',         NULL),
    ('Liburan',         'idr',    25000000),
    ('Tabungan Mobil',  'idr',    50000000),
    ('US Savings',      'usd',     1000000),
    ('Tabungan Emas',   'gold-g',      100);

INSERT INTO counterparties (name) VALUES
    ('PT Telkom'),
    ('Hypermart'),
    ('Toko Buku Gramedia'),
    ('Pasar Senen'),
    ('BCA Mortgage');

DO $$
DECLARE
    riza_id        uuid := (SELECT id FROM users WHERE telegram_identifier='@riza_ramadan');
    a_riza_cash    uuid := (SELECT id FROM accounts WHERE name='Riza — Cash');
    a_shima_bca    uuid := (SELECT id FROM accounts WHERE name='Shima — BCA');
    a_joint_bca    uuid := (SELECT id FROM accounts WHERE name='Joint — BCA');
    p_belanja      uuid := (SELECT id FROM pos      WHERE name='Belanja Bulanan');
    p_mortgage     uuid := (SELECT id FROM pos      WHERE name='Mortgage');
    p_sekolah      uuid := (SELECT id FROM pos      WHERE name='Anak Sekolah');
    p_liburan      uuid := (SELECT id FROM pos      WHERE name='Liburan');
    p_us_savings   uuid := (SELECT id FROM pos      WHERE name='US Savings');
    cp_telkom      uuid := (SELECT id FROM counterparties WHERE name='PT Telkom');
    cp_hypermart   uuid := (SELECT id FROM counterparties WHERE name='Hypermart');
    cp_gramedia    uuid := (SELECT id FROM counterparties WHERE name='Toko Buku Gramedia');
    cp_pasar       uuid := (SELECT id FROM counterparties WHERE name='Pasar Senen');
    cp_bca         uuid := (SELECT id FROM counterparties WHERE name='BCA Mortgage');

    tx_groceries_bad uuid;
    tx_groceries_rev uuid;
    tx_mortgage_apr  uuid;

    -- Helper macro implemented as nested INSERT — keep below.
BEGIN
    -----------------------------------------------------------------
    -- Funding events (one big chunk per Pos in Feb, deposited into the
    -- account that "covers" that Pos for the household). After the six
    -- months of expenses below, every Pos and account ends positive.
    -----------------------------------------------------------------

    -- Joint — BCA funds Mortgage (50M), Liburan (12.5M), and US Savings
    -- (25M IDR account-side ↔ 250k cents pos-side, ≈ $2,500 at funding).
    INSERT INTO transactions (type, effective_date, account_id, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by) VALUES
        ('money_in', '2026-02-01', a_joint_bca, 50000000, p_mortgage,    50000000, cp_telkom, 'Mortgage envelope funding',     'seed', 'seed-fund-jbca-mortgage', riza_id),
        ('money_in', '2026-02-01', a_joint_bca, 12500000, p_liburan,     12500000, cp_telkom, 'Liburan envelope funding',      'seed', 'seed-fund-jbca-liburan',  riza_id),
        ('money_in', '2026-02-01', a_joint_bca, 25000000, p_us_savings,    250000, cp_telkom, 'US Savings funding (USD ≈$2.5K)','seed', 'seed-fund-jbca-ussav',    riza_id);

    -- Riza — Cash funds Mortgage and Anak Sekolah.
    INSERT INTO transactions (type, effective_date, account_id, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by) VALUES
        ('money_in', '2026-02-01', a_riza_cash,  8000000, p_mortgage,     8000000, cp_telkom, 'Mortgage envelope funding (Riza)', 'seed', 'seed-fund-rcash-mortgage', riza_id),
        ('money_in', '2026-02-01', a_riza_cash,  1200000, p_sekolah,      1200000, cp_telkom, 'Anak Sekolah funding',             'seed', 'seed-fund-rcash-sekolah',  riza_id);

    -- Shima — BCA funds Mortgage and Belanja Bulanan.
    INSERT INTO transactions (type, effective_date, account_id, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by) VALUES
        ('money_in', '2026-02-01', a_shima_bca,  5000000, p_mortgage,     5000000, cp_telkom, 'Mortgage envelope funding (Shima)', 'seed', 'seed-fund-sbca-mortgage', riza_id),
        ('money_in', '2026-02-01', a_shima_bca,  8800000, p_belanja,      8800000, cp_telkom, 'Belanja Bulanan funding',           'seed', 'seed-fund-sbca-belanja',  riza_id);

    -----------------------------------------------------------------
    -- Expense events — six months of mortgage + monthly groceries.
    -- Drains the pos cash balance toward the displayed target.
    -----------------------------------------------------------------
    INSERT INTO transactions (type, effective_date, account_id, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by)
    VALUES
        -- Mortgage payments (Joint — BCA → Mortgage pos), monthly.
        ('money_out', '2026-04-22', a_joint_bca, 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-apr', riza_id),
        ('money_out', '2026-03-22', a_joint_bca, 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-mar', riza_id),
        ('money_out', '2026-02-22', a_joint_bca, 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-feb', riza_id),
        ('money_out', '2026-01-22', a_joint_bca, 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-jan', riza_id),
        ('money_out', '2025-12-22', a_joint_bca, 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-dec', riza_id),
        ('money_out', '2025-11-22', a_joint_bca, 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-nov', riza_id),

        -- Groceries via Shima — BCA → Belanja Bulanan, monthly.
        ('money_out', '2026-04-28', a_shima_bca, 1350000, p_belanja, 1350000, cp_hypermart, 'Weekly groceries', 'seed', 'seed-groc-apr', riza_id),
        ('money_out', '2026-03-15', a_shima_bca, 1290000, p_belanja, 1290000, cp_hypermart, '',                 'seed', 'seed-groc-mar', riza_id),
        ('money_out', '2026-02-18', a_shima_bca, 1180000, p_belanja, 1180000, cp_hypermart, '',                 'seed', 'seed-groc-feb', riza_id),
        ('money_out', '2026-01-18', a_shima_bca, 1410000, p_belanja, 1410000, cp_hypermart, '',                 'seed', 'seed-groc-jan', riza_id),
        ('money_out', '2025-12-18', a_shima_bca, 1320000, p_belanja, 1320000, cp_hypermart, '',                 'seed', 'seed-groc-dec', riza_id),
        ('money_out', '2025-11-18', a_shima_bca, 1240000, p_belanja, 1240000, cp_hypermart, '',                 'seed', 'seed-groc-nov', riza_id),

        -- Apr: school books (Riza Cash → Anak Sekolah).
        ('money_out', '2026-04-26', a_riza_cash,  280000, p_sekolah,  280000, cp_gramedia, 'School books',     'seed', 'seed-books-apr', riza_id),

        -- Apr 19: extra groceries trip via Riza Cash (Pasar Senen).
        ('money_out', '2026-04-19', a_riza_cash,  340000, p_belanja,  340000, cp_pasar,    'Sayur + buah',     'seed', 'seed-groc-extra-apr', riza_id);

    -----------------------------------------------------------------
    -- A wrong charge that gets reversed — drives the line-through
    -- styling and the "reverses →" badge on /transactions.
    -----------------------------------------------------------------
    INSERT INTO transactions (type, effective_date, account_id, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by) VALUES
        ('money_out', '2026-04-25', a_shima_bca, 1200000, p_belanja, 1200000, cp_hypermart, 'Refund — wrong charge', 'seed', 'seed-bad-charge', riza_id)
    RETURNING id INTO tx_groceries_bad;

    INSERT INTO transactions (type, effective_date, account_id, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by, reverses_id) VALUES
        ('money_in', '2026-04-26', a_shima_bca, 1200000, p_belanja, 1200000, cp_hypermart, 'Reversal — wrong charge', 'seed', 'seed-bad-charge-rev', riza_id, tx_groceries_bad)
    RETURNING id INTO tx_groceries_rev;

    -----------------------------------------------------------------
    -- One open obligation: Belanja owes Mortgage 1.5M (a manual
    -- inter-pos borrow scenario, anchored on the Apr mortgage txn).
    -----------------------------------------------------------------
    SELECT id INTO tx_mortgage_apr FROM transactions WHERE idempotency_key = 'seed-mort-apr';
    INSERT INTO pos_obligation (transaction_id, creditor_pos_id, debtor_pos_id, currency, amount_owed, amount_repaid)
    VALUES (tx_mortgage_apr, p_mortgage, p_belanja, 'idr', 1500000, 0);

    -----------------------------------------------------------------
    -- Notifications for Riza, referencing recent txns. Mix of unread
    -- (3) and read (2) so the bell shows "3" and the feed shows both
    -- weights.
    -----------------------------------------------------------------
    INSERT INTO notifications (user_id, type, title, body, related_transaction_id, read_at, created_at) VALUES
        (riza_id, 'transaction_created', 'New expense recorded',
         'Hypermart — Rp 1.350.000 from Belanja Bulanan',
         (SELECT id FROM transactions WHERE idempotency_key='seed-groc-apr'),
         NULL, now() - interval '12 minutes'),
        (riza_id, 'transaction_created', 'Mortgage payment',
         'Rp 8.500.000 from Joint — BCA · Mortgage envelope',
         tx_mortgage_apr,
         NULL, now() - interval '3 hours'),
        (riza_id, 'transaction_created', 'Salary received',
         'Rp 50.000.000 from PT Telkom · Mortgage funding',
         (SELECT id FROM transactions WHERE idempotency_key='seed-fund-jbca-mortgage'),
         NULL, now() - interval '1 day 2 hours'),
        (riza_id, 'transaction_created', 'Refund processed',
         'Hypermart reversed Rp 1.200.000 (wrong charge)',
         tx_groceries_rev,
         now() - interval '2 days', now() - interval '3 days'),
        (riza_id, 'transaction_created', 'School books',
         'Toko Buku Gramedia — Rp 280.000 from Anak Sekolah',
         (SELECT id FROM transactions WHERE idempotency_key='seed-books-apr'),
         now() - interval '4 days', now() - interval '5 days');
END $$;

COMMIT;
