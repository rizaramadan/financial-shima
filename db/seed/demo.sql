-- db/seed/demo.sql — comprehensive sample data for screenshot/demo runs.
-- Resets the data tables (preserving migrations) and inserts a coherent
-- two-user, multi-account, multi-currency snapshot covering every page.
--
-- Per spec §4.2/§5.6, each Pos belongs to exactly one Account; account
-- balances are derived (no stored balance, and no transaction.account_id
-- — that field was removed in migration 0005). Account flows for a Pos
-- attribute to the Pos's *current* account_id, with snapshot semantics:
-- moving a Pos to a different account retroactively shifts past flows.
--
-- The demo therefore picks one account per Pos that "feels right" for
-- the operator persona; if the visual story needed cross-account
-- funding, the demo would need to split that envelope into multiple Pos.
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

-- account_id chosen per Pos. Joint-BCA holds Mortgage + Liburan + US
-- Savings (the household envelopes); Shima-BCA holds Belanja; Riza-Cash
-- holds Anak Sekolah, Petty Cash, Tabungan Mobil, Tabungan Emas.
INSERT INTO pos (name, currency, account_id, target) VALUES
    ('Belanja Bulanan', 'idr',
        (SELECT id FROM accounts WHERE name='Shima — BCA'),  5000000),
    ('Mortgage',        'idr',
        (SELECT id FROM accounts WHERE name='Joint — BCA'), 12000000),
    ('Anak Sekolah',    'idr',
        (SELECT id FROM accounts WHERE name='Riza — Cash'),     NULL),
    ('Liburan',         'idr',
        (SELECT id FROM accounts WHERE name='Joint — BCA'), 25000000),
    ('Tabungan Mobil',  'idr',
        (SELECT id FROM accounts WHERE name='Riza — Cash'), 50000000),
    ('US Savings',      'usd',
        (SELECT id FROM accounts WHERE name='Joint — BCA'),  1000000),
    ('Tabungan Emas',   'gold-g',
        (SELECT id FROM accounts WHERE name='Riza — Cash'),      100),
    -- Petty Cash deliberately overdrawn — drives the negative-cash
    -- marker per spec §6.2 / scenario S20.
    ('Petty Cash',      'idr',
        (SELECT id FROM accounts WHERE name='Riza — Cash'),     NULL);

INSERT INTO counterparties (name) VALUES
    ('PT Telkom'),
    ('Hypermart'),
    ('Toko Buku Gramedia'),
    ('Pasar Senen'),
    ('BCA Mortgage');

DO $$
DECLARE
    riza_id        uuid := (SELECT id FROM users WHERE telegram_identifier='@riza_ramadan');
    p_belanja      uuid := (SELECT id FROM pos      WHERE name='Belanja Bulanan');
    p_mortgage     uuid := (SELECT id FROM pos      WHERE name='Mortgage');
    p_sekolah      uuid := (SELECT id FROM pos      WHERE name='Anak Sekolah');
    p_petty        uuid := (SELECT id FROM pos      WHERE name='Petty Cash');
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
BEGIN
    -----------------------------------------------------------------
    -- Funding events. Each money_in row credits the Pos's current
    -- account (via pos.account_id) — accounts no longer appear here.
    -----------------------------------------------------------------
    INSERT INTO transactions (type, effective_date, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by) VALUES
        ('money_in', '2026-02-01', 63000000, p_mortgage,    63000000, cp_telkom, 'Mortgage envelope funding',     'seed', 'seed-fund-mortgage', riza_id),
        ('money_in', '2026-02-01', 12500000, p_liburan,     12500000, cp_telkom, 'Liburan envelope funding',      'seed', 'seed-fund-liburan',  riza_id),
        ('money_in', '2026-02-01', 25000000, p_us_savings,    250000, cp_telkom, 'US Savings funding (USD ≈$2.5K)','seed', 'seed-fund-ussav',    riza_id),
        ('money_in', '2026-02-01',  1200000, p_sekolah,      1200000, cp_telkom, 'Anak Sekolah funding',          'seed', 'seed-fund-sekolah',  riza_id),
        ('money_in', '2026-02-01',  8800000, p_belanja,      8800000, cp_telkom, 'Belanja Bulanan funding',       'seed', 'seed-fund-belanja',  riza_id);

    -----------------------------------------------------------------
    -- Expense events — six months of mortgage + monthly groceries.
    -----------------------------------------------------------------
    INSERT INTO transactions (type, effective_date, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by) VALUES
        ('money_out', '2026-04-22', 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-apr', riza_id),
        ('money_out', '2026-03-22', 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-mar', riza_id),
        ('money_out', '2026-02-22', 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-feb', riza_id),
        ('money_out', '2026-01-22', 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-jan', riza_id),
        ('money_out', '2025-12-22', 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-dec', riza_id),
        ('money_out', '2025-11-22', 8500000, p_mortgage, 8500000, cp_bca, 'Monthly mortgage', 'seed', 'seed-mort-nov', riza_id),

        ('money_out', '2026-04-28', 1350000, p_belanja, 1350000, cp_hypermart, 'Weekly groceries', 'seed', 'seed-groc-apr', riza_id),
        ('money_out', '2026-03-15', 1290000, p_belanja, 1290000, cp_hypermart, '',                 'seed', 'seed-groc-mar', riza_id),
        ('money_out', '2026-02-18', 1180000, p_belanja, 1180000, cp_hypermart, '',                 'seed', 'seed-groc-feb', riza_id),
        ('money_out', '2026-01-18', 1410000, p_belanja, 1410000, cp_hypermart, '',                 'seed', 'seed-groc-jan', riza_id),
        ('money_out', '2025-12-18', 1320000, p_belanja, 1320000, cp_hypermart, '',                 'seed', 'seed-groc-dec', riza_id),
        ('money_out', '2025-11-18', 1240000, p_belanja, 1240000, cp_hypermart, '',                 'seed', 'seed-groc-nov', riza_id),

        ('money_out', '2026-04-26',  280000, p_sekolah,  280000, cp_gramedia, 'School books',     'seed', 'seed-books-apr',     riza_id),
        ('money_out', '2026-04-19',  340000, p_belanja,  340000, cp_pasar,    'Sayur + buah',     'seed', 'seed-groc-extra-apr',riza_id),

        -- Petty Cash: small funding, larger spend → ends negative for S20.
        ('money_in',  '2026-04-05',  100000, p_petty,    100000, cp_telkom,   'Petty Cash top-up','seed', 'seed-petty-fund',  riza_id),
        ('money_out', '2026-04-21',  450000, p_petty,    450000, cp_pasar,    'Cash advance — repair','seed', 'seed-petty-spend', riza_id);

    -----------------------------------------------------------------
    -- A wrong charge that gets reversed.
    -----------------------------------------------------------------
    INSERT INTO transactions (type, effective_date, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by) VALUES
        ('money_out', '2026-04-25', 1200000, p_belanja, 1200000, cp_hypermart, 'Refund — wrong charge', 'seed', 'seed-bad-charge', riza_id)
    RETURNING id INTO tx_groceries_bad;

    INSERT INTO transactions (type, effective_date, account_amount, pos_id, pos_amount, counterparty_id, note, source, idempotency_key, created_by, reverses_id) VALUES
        ('money_in', '2026-04-26', 1200000, p_belanja, 1200000, cp_hypermart, 'Reversal — wrong charge', 'seed', 'seed-bad-charge-rev', riza_id, tx_groceries_bad)
    RETURNING id INTO tx_groceries_rev;

    -----------------------------------------------------------------
    -- One open obligation: Belanja owes Mortgage 1.5M.
    -----------------------------------------------------------------
    SELECT id INTO tx_mortgage_apr FROM transactions WHERE idempotency_key = 'seed-mort-apr';
    INSERT INTO pos_obligation (transaction_id, creditor_pos_id, debtor_pos_id, currency, amount_owed, amount_repaid)
    VALUES (tx_mortgage_apr, p_mortgage, p_belanja, 'idr', 1500000, 0);

    -----------------------------------------------------------------
    -- Notifications for Riza.
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
         'Rp 63.000.000 from PT Telkom · Mortgage funding',
         (SELECT id FROM transactions WHERE idempotency_key='seed-fund-mortgage'),
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
