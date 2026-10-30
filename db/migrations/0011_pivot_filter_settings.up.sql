-- 0011_pivot_filter_settings — per-user persistence of the /pivot filter
-- view (spec: filters survive across sessions). The client keeps the live
-- state in the URL; on every change it POSTs the same query string here,
-- and PivotGet embeds it back so a fresh visit (no URL params) restores
-- the user's last view. One row per user; login writes never conflict
-- because each user only ever touches their own row.
CREATE TABLE pivot_filter_settings (
    user_id     uuid        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    filter_query text       NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now()
);
