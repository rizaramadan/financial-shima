package dbq_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rizaramadan/financial-shima/db/dbq"
)

func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// TestIntegration_LocalPostgres exercises the round-trip: connect, insert,
// read back, archive, list. Skipped when no DB URL is provided so CI
// without a Postgres can still run go test ./....
//
// To run locally:
//
//	export DATABASE_URL=postgres://postgres@localhost:5432/financial_shima?sslmode=disable
//	go test ./db/dbq/... -run TestIntegration -v
func TestIntegration_LocalPostgres(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	q := dbq.New(conn)

	t.Run("Account CRUD round-trip", func(t *testing.T) {
		// Use a unique name so reruns don't collide on the soft-archive.
		acc, err := q.CreateAccount(ctx, "Integration test "+time.Now().Format("20060102150405.000"))
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		defer func() { _ = q.ArchiveAccount(ctx, acc.ID) }()

		got, err := q.GetAccount(ctx, acc.ID)
		if err != nil {
			t.Fatalf("GetAccount: %v", err)
		}
		if got.ID != acc.ID || got.Name != acc.Name {
			t.Errorf("round-trip mismatch: created %+v got %+v", acc, got)
		}
		if got.Archived {
			t.Error("freshly created account should not be archived")
		}

		list, err := q.ListAccounts(ctx)
		if err != nil {
			t.Fatalf("ListAccounts: %v", err)
		}
		found := false
		for _, a := range list {
			if a.ID == acc.ID {
				found = true
				break
			}
		}
		if !found {
			t.Error("created account not in ListAccounts")
		}

		if err := q.ArchiveAccount(ctx, acc.ID); err != nil {
			t.Fatalf("ArchiveAccount: %v", err)
		}
		afterArchive, err := q.ListAccounts(ctx)
		if err != nil {
			t.Fatalf("ListAccounts: %v", err)
		}
		for _, a := range afterArchive {
			if a.ID == acc.ID {
				t.Error("archived account still in default ListAccounts")
			}
		}
	})

	t.Run("Pos rejects bad currency", func(t *testing.T) {
		_, err := q.CreatePos(ctx, dbq.CreatePosParams{
			Name:     "Test Pos " + time.Now().Format("20060102150405.000"),
			Currency: "INVALID UPPERCASE",
		})
		if err == nil {
			t.Error("CreatePos accepted uppercase/space currency; CHECK constraint should have rejected it")
		}
	})

	t.Run("Counterparty dedupes by name_lower", func(t *testing.T) {
		// Counterparty name regex disallows '.'; use a digits-only suffix.
		stamp := time.Now().Format("150405")
		first, err := q.GetOrCreateCounterparty(ctx, "Salary "+stamp)
		if err != nil {
			t.Fatalf("first GetOrCreateCounterparty: %v", err)
		}
		// Same name, different casing — should resolve to the same row,
		// and the original casing is preserved (spec §4.4).
		second, err := q.GetOrCreateCounterparty(ctx, "salary "+stamp)
		if err != nil {
			t.Fatalf("second GetOrCreateCounterparty: %v", err)
		}
		if first.ID != second.ID {
			t.Errorf("dedup failed: first=%v second=%v", first.ID, second.ID)
		}
		if second.Name != first.Name {
			t.Errorf("original casing not preserved: first=%q second=%q", first.Name, second.Name)
		}
	})

	t.Run("Sessions FK and expiry filter", func(t *testing.T) {
		stamp := time.Now().Format("150405.000")
		u, err := q.UpsertUser(ctx, dbq.UpsertUserParams{
			DisplayName:        "Tester " + stamp,
			TelegramIdentifier: "@tester_" + stamp,
		})
		if err != nil {
			t.Fatalf("UpsertUser: %v", err)
		}

		// Active session.
		token := "tok-" + stamp
		_, err = q.CreateSession(ctx, dbq.CreateSessionParams{
			Token:     token,
			UserID:    u.ID,
			ExpiresAt: ts(time.Now().Add(7 * 24 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		s, err := q.GetSession(ctx, token)
		if err != nil {
			t.Fatalf("GetSession active: %v", err)
		}
		if s.UID != u.ID {
			t.Errorf("session.UID = %v, want %v", s.UID, u.ID)
		}

		// Expired session — set expiry in the past, GetSession filters it.
		_, err = q.CreateSession(ctx, dbq.CreateSessionParams{
			Token:     token + "-exp",
			UserID:    u.ID,
			ExpiresAt: ts(time.Now().Add(-1 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("CreateSession expired: %v", err)
		}
		_, err = q.GetSession(ctx, token+"-exp")
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("GetSession on expired returned %v, want pgx.ErrNoRows", err)
		}
	})
}

