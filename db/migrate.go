// Package db owns DB-startup concerns: applying pending migrations
// before the server begins serving traffic. Connection pooling lives
// in [cmd/server], and the per-query code lives in [db/dbq] (sqlc-
// generated). This file is the only place the binary touches DDL.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	// Side-effect import: registers the `pgx5://` URL scheme with the
	// migrate driver registry. Without this, NewWithSourceInstance
	// errors with "unknown driver pgx5".
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsFS embeds every `NNNN_*.up.sql` / `NNNN_*.down.sql` pair
// under db/migrations. `//go:embed` is rooted at this file's directory.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// baselineVersion infers which migration set has already been applied
// to a pre-existing database that was bootstrapped before this code
// path existed (i.e. before `schema_migrations` was tracked).
//
// The detection is column-based rather than table-based because the
// MVP added migrations 0002–0004 without changing the set of tables;
// only 0001 (initial DDL) and 0005 (pos.account_id move) shifted the
// column shape in a way we can fingerprint at runtime.
//
// Returns:
//
//	(version, true)  — a baseline we can `Force` to.
//	(0,       false) — schema is empty / unrecognised; caller should
//	                   run all migrations from zero.
//
// Reads are done via pgxpool to share the binary's existing pool;
// the migrate library opens its own *sql.DB but we don't need to
// re-pool just to ask information_schema two questions.
func baselineVersion(ctx context.Context, pool *pgxpool.Pool) (uint, bool, error) {
	// Empty / fresh DB ⇒ no `accounts` table yet ⇒ start from zero
	// and let migrate apply everything.
	var hasAccounts bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM information_schema.tables
		    WHERE table_schema = 'public' AND table_name = 'accounts'
		)`).Scan(&hasAccounts); err != nil {
		return 0, false, fmt.Errorf("baseline: check accounts table: %w", err)
	}
	if !hasAccounts {
		return 0, false, nil
	}

	// `pos.account_id` exists ⇒ 0005 has been applied manually.
	// Otherwise we're at 0004 (0001–0004 share a table-set fingerprint
	// that's stable through their column additions; 0005 is the first
	// of those that's missing on the existing production DB and the
	// only one we need to discriminate on).
	var hasPosAccountID bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM information_schema.columns
		    WHERE table_schema = 'public'
		      AND table_name   = 'pos'
		      AND column_name  = 'account_id'
		)`).Scan(&hasPosAccountID); err != nil {
		return 0, false, fmt.Errorf("baseline: check pos.account_id column: %w", err)
	}
	if hasPosAccountID {
		return 5, true, nil
	}
	return 4, true, nil
}

// hasSchemaMigrationsTable reports whether golang-migrate has already
// claimed this database. If so, we leave the bookkeeping alone and let
// migrate's normal flow take over (no baseline forcing).
func hasSchemaMigrationsTable(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var present bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM information_schema.tables
		    WHERE table_schema = 'public' AND table_name = 'schema_migrations'
		)`).Scan(&present); err != nil {
		return false, fmt.Errorf("check schema_migrations table: %w", err)
	}
	return present, nil
}

// Migrate brings the database up to the latest migration. Called once
// from cmd/server before the HTTP server starts.
//
// Bootstrap path: if `schema_migrations` is missing, infer the
// already-applied version from the live schema (see [baselineVersion])
// and Force migrate's bookkeeping to that version before calling Up.
// This is needed because the project's first ~4 migrations were
// applied via raw `psql` in earlier phases — there's no row history
// for them. Without the force, migrate would try to re-apply 0001
// and fail on `CREATE TABLE accounts` (already exists).
//
// After bootstrap, every subsequent boot takes the normal path:
// migrate reads `schema_migrations` and applies whatever's new.
//
// Errors:
//   - any failure here returns; cmd/server treats it as fatal so the
//     binary refuses to serve traffic against an inconsistent schema.
func Migrate(ctx context.Context, pool *pgxpool.Pool, dbURL string) error {
	// Probe the schema BEFORE constructing migrate. The postgres
	// driver auto-creates schema_migrations on its first connect,
	// which would mask the "no tracking yet" signal we need to decide
	// whether to force a baseline.
	tracked, err := hasSchemaMigrationsTable(ctx, pool)
	if err != nil {
		return err
	}
	var baseline uint
	hasBaseline := false
	if !tracked {
		base, ok, err := baselineVersion(ctx, pool)
		if err != nil {
			return err
		}
		baseline, hasBaseline = base, ok
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate: open embedded source: %w", err)
	}

	// pgx/v5 driver lets us reuse the same connection-string semantics
	// the rest of the binary uses (pgxpool.New). Avoids a second DSN
	// flavor that could drift.
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5://"+stripScheme(dbURL))
	if err != nil {
		return fmt.Errorf("migrate: new instance: %w", err)
	}
	defer func() {
		// migrate.Close returns (sourceErr, dbErr); we don't have a
		// useful place to surface those independently. Best-effort.
		_, _ = m.Close()
	}()

	switch {
	case tracked:
		log.Print("migrate: schema_migrations present; running normal Up")
	case hasBaseline:
		log.Printf("migrate: schema_migrations absent; forcing baseline to %d", baseline)
		if err := m.Force(int(baseline)); err != nil {
			return fmt.Errorf("migrate: force baseline %d: %w", baseline, err)
		}
	default:
		log.Print("migrate: fresh database, running all migrations from zero")
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	log.Print("migrate: complete")
	return nil
}

// stripScheme replaces a leading `postgres://` or `postgresql://`
// with nothing, so callers can prepend the `pgx5://` scheme that the
// migrate pgx/v5 driver registers. Anything else is returned as-is
// so e.g. unix-socket strings still work.
func stripScheme(u string) string {
	for _, p := range []string{"postgres://", "postgresql://"} {
		if len(u) >= len(p) && u[:len(p)] == p {
			return u[len(p):]
		}
	}
	return u
}
