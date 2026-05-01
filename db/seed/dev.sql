-- db/seed/dev.sql — sample data for local development.
-- Idempotent: re-running re-seeds the canonical fixtures without duplicates.
-- Run via: psql $DATABASE_URL -f db/seed/dev.sql

BEGIN;

-- Two seeded users per spec §3.1. UPSERT by telegram_identifier.
INSERT INTO users (display_name, telegram_identifier) VALUES
    ('Riza',  '@riza_ramadan'),
    ('Shima', '@shima')
ON CONFLICT (telegram_identifier) DO UPDATE SET display_name = EXCLUDED.display_name;

-- Sample accounts (IDR-only per §4.1). UPSERT by name (no unique constraint
-- yet — this script tolerates re-runs by checking existence first).
INSERT INTO accounts (name)
SELECT 'BCA Riza'
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'BCA Riza');
INSERT INTO accounts (name)
SELECT 'Mandiri Shima'
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'Mandiri Shima');

-- Sample Pos (envelopes). UPSERT by (name, currency) — already UNIQUE.
INSERT INTO pos (name, currency, target) VALUES
    ('Belanja Bulanan', 'idr', 5000000),
    ('Tabungan Mobil',  'idr', 50000000),
    ('Tabungan Emas',   'gold-g', 100),
    ('Dapur Darurat',   'idr', NULL)
ON CONFLICT (name, currency) DO UPDATE SET target = EXCLUDED.target;

-- Sample counterparties. name_lower is generated; UPSERT by it.
INSERT INTO counterparties (name) VALUES
    ('Salary'),
    ('Pertamina'),
    ('Indomaret'),
    ('Antam')
ON CONFLICT (name_lower) DO NOTHING;

COMMIT;
