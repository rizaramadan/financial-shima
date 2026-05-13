-- 0004_income_template.sql — Phase-11 income templates.
--
-- An "income type" the operator names once (e.g. "Riza monthly salary",
-- "Shima salary", "Year-end bonus") and the fixed-amount allocation it
-- expands into across N Pos. The template is the ONLY mechanism by
-- which a single incoming event credits multiple Pos; a regular
-- money_in still credits exactly one (per-pos surface unchanged).
--
-- Apply rules (enforced in the application, not the schema):
--   incoming.amount  <  Σ(lines.amount)                   → reject
--   incoming.amount ==  Σ(lines.amount)                   → split per lines
--   incoming.amount  >  Σ(lines.amount)
--      AND template.leftover_pos_id IS NOT NULL           → split per lines, remainder to leftover
--      AND template.leftover_pos_id IS NULL               → reject

CREATE TABLE income_template (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name             text        NOT NULL CHECK (length(trim(name)) > 0),
    -- Optional Pos that absorbs any salary > Σ(lines). NULL = strict
    -- (apply must match exactly).
    leftover_pos_id  uuid        REFERENCES pos(id),
    archived         boolean     NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name)
);

CREATE TABLE income_template_line (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id  uuid        NOT NULL REFERENCES income_template(id) ON DELETE CASCADE,
    pos_id       uuid        NOT NULL REFERENCES pos(id),
    amount       bigint      NOT NULL CHECK (amount > 0),
    sort_order   int         NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- A given Pos appears at most once per template — no double-funding
    -- the same envelope from one salary.
    UNIQUE (template_id, pos_id)
);

CREATE INDEX income_template_line_template_idx ON income_template_line (template_id);
