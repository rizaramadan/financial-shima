-- down migrations are intentionally no-ops. Schema changes here are forward-only;
-- recovery is via Postgres backup/PITR, not migrate down. golang-migrate still
-- requires a .down.sql file alongside each .up.sql to discover the version.
SELECT 1;
