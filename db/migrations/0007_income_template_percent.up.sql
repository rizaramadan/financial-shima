-- 0007_income_template_percent.sql — percent lines on income templates.
--
-- A line is fixed (amount > 0), percent (percent_bps 1..10000, i.e.
-- basis points of the incoming amount), or both. percent_bps NULL
-- means "no percent part" (all pre-0007 rows). amount may now be 0,
-- but only when a percent part carries the line.
ALTER TABLE income_template_line
    ADD COLUMN percent_bps integer NULL
    CHECK (percent_bps IS NULL OR (percent_bps > 0 AND percent_bps <= 10000));

ALTER TABLE income_template_line
    DROP CONSTRAINT income_template_line_amount_check;

ALTER TABLE income_template_line
    ADD CONSTRAINT income_template_line_amount_check CHECK (amount >= 0);

ALTER TABLE income_template_line
    ADD CONSTRAINT income_template_line_percent_or_amount_check
    CHECK (amount > 0 OR percent_bps IS NOT NULL);
