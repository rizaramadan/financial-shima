-- 0010_income_template_line_note.sql — free-text note on template lines.
--
-- income_template_line is UNIQUE (template_id, pos_id), so a template can
-- fund a given Pos only once. When a budget covers one Pos for two payees
-- (e.g. "Zakat jt" -> Renty 250,000 + Bi mimin 250,000) the two rows are
-- collapsed into a single line. The split is recorded here so the payee
-- detail is not lost.
--
-- Nullable and purely additive. Queries list their columns explicitly, so
-- nothing reads it yet; it exists for humans and for reports that want the
-- breakdown.
--
-- IF NOT EXISTS because this column was added by hand on the live database
-- before the migration existed (schema drift from the 2026-10-27 data load).
-- Running this against that database must be a no-op, not an error.
ALTER TABLE income_template_line
    ADD COLUMN IF NOT EXISTS note text;
