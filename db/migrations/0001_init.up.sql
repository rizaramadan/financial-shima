-- 0001_init.sql — Phase-4 baseline schema covering the five tables required
-- by the Phase-4 exit criteria (users, sessions, accounts, pos,
-- counterparties). Transaction tables (§4.3) ship in a later phase.
--
-- All `id` columns are uuid generated server-side. `created_at` columns are
-- timestamptz so timezone is explicit on the wire.

CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- for gen_random_uuid()

-- Users (§3.1) — pre-seeded; no self-service registration. Adding a user
-- requires editing the seed file and redeploying.
CREATE TABLE users (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name        text        NOT NULL,
    telegram_identifier text        NOT NULL UNIQUE,
    created_at          timestamptz NOT NULL DEFAULT now()
);

-- Sessions (§3.4) — opaque tokens with a 7-day rolling expiry.
CREATE TABLE sessions (
    token       text        PRIMARY KEY,
    user_id     uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_at   timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- Accounts (§4.1) — IDR-only bank accounts. No `currency` column (implicit).
-- No `opening_balance` column (opening balance is a transaction).
CREATE TABLE accounts (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text        NOT NULL CHECK (length(trim(name)) > 0),
    archived    boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Pos (§4.2) — envelope buckets, optionally non-IDR.
-- currency: lowercase alphanumeric+dash per spec; enforced via CHECK.
-- target: optional savings goal.
-- (name, currency) unique per spec §4.2: "unique per currency".
CREATE TABLE pos (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text        NOT NULL CHECK (length(trim(name)) > 0),
    currency    text        NOT NULL DEFAULT 'idr'
                            CHECK (currency ~ '^[a-z0-9-]+$'),
    -- target is integer cents, NOT numeric — spec §10.1 forbids float-style
    -- decimals for money values; the smallest unit (rupiah for IDR, the
    -- operator's chosen subdivision for non-IDR) is the only representation.
    target      bigint      CHECK (target IS NULL OR target >= 0),
    archived    boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name, currency)
);

-- Counterparties (§4.4) — external named parties for money_in/_out.
-- name validated by spec §4.4 regex; lowercase shadow column for
-- case-insensitive autocomplete and dedup.
CREATE TABLE counterparties (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- length(trim(name)) > 0 mirrors accounts/pos and prevents a whitespace-
    -- only name from passing the regex CHECK (which permits space alone).
    name        text        NOT NULL CHECK (
                                length(trim(name)) > 0
                                AND name ~ '^[a-zA-Z0-9_\- ]+$'
                            ),
    name_lower  text        NOT NULL UNIQUE GENERATED ALWAYS AS (lower(name)) STORED,
    created_at  timestamptz NOT NULL DEFAULT now()
);
