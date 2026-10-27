package handler

// Shared Pos create / update / archive path (same pattern as
// transaction_service.go). Both the web handlers (pos_new.go, pos.go)
// and the API handlers (api_pos_post.go) resolve → validate → write
// through here, so the entries stay behavior-identical by
// construction. Handlers keep only their transport concerns: parsing
// their own input shape (form fields vs JSON) and rendering their own
// output (redirect / re-render vs JSON + status code).
//
// HATEOAS-lite: results carry _links (self/list/account/transactions
// against existing GETs only — no new endpoints in this step) and
// _actions so HTMX pages render as thin views of the same JSON.
//
// Money stays integer cents; logic/pos stays pure (no DB, no time).

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rizaramadan/financial-shima/db/dbq"
	logicpos "github.com/rizaramadan/financial-shima/logic/pos"
)

// PosCreateInput is the transport-neutral shape both create entries
// map into. AccountID is already UUID-parsed by the caller (form vs
// JSON parse stays in the handler); Name/Currency are raw and get
// normalized (trim + lowercase currency) inside createPos via
// logic/pos.Normalize. Target nil = no budget target.
type PosCreateInput struct {
	Name      string
	Currency  string
	AccountID uuid.UUID
	Target    *int64
}

// PosUpdateInput is the transport-neutral shape both update entries
// map into. Nil Name = preserve; TargetSet false = preserve, true
// with nil Target = clear, non-nil = set; nil AccountID = preserve.
type PosUpdateInput struct {
	PosID     uuid.UUID
	Name      *string
	TargetSet bool
	Target    *int64
	AccountID *uuid.UUID
}

// posNotFoundError marks a missing pos (or missing new account on
// move) so callers map it to 404 (vs 500 for real DB failures).
type posNotFoundError struct{ msg string }

func (e *posNotFoundError) Error() string { return e.msg }

// IsPosNotFound reports whether err is a missing-resource failure.
func IsPosNotFound(err error) bool {
	var nf *posNotFoundError
	return errors.As(err, &nf)
}

// posConflictError marks a rename/create rejected by the UNIQUE
// (name, currency) constraint so callers map it to 409 (vs 500).
type posConflictError struct{ msg string }

func (e *posConflictError) Error() string { return e.msg }

// IsPosConflict reports whether err is a unique-violation failure.
func IsPosConflict(err error) bool {
	var ce *posConflictError
	return errors.As(err, &ce)
}

// posAccountNotFoundError marks a create/move referencing a
// non-existent account (FK 23503) so callers map it to 400 with a
// field-level message (vs 500 for real DB failures).
type posAccountNotFoundError struct{ msg string }

func (e *posAccountNotFoundError) Error() string { return e.msg }

// IsPosAccountNotFound reports whether err is a bad-account failure.
func IsPosAccountNotFound(err error) bool {
	var ae *posAccountNotFoundError
	return errors.As(err, &ae)
}

// posLinks builds the HATEOAS-lite _links for a Pos. Pure —
// unit-tested without a database. Hrefs target existing GETs only.
func posLinks(posID, accountID string) map[string]string {
	return map[string]string{
		"self":         "/api/v1/pos/" + posID,
		"list":         "/api/v1/pos",
		"account":      "/api/v1/accounts/" + accountID,
		"transactions": "/api/v1/transactions?pos_id=" + posID,
	}
}

// posActions builds the _actions for a Pos. Only existing GETs are
// referenced — no new endpoints in this step. Reuses MoneyAction so
// the wire shape stays identical across resources.
func posActions(posID string) []MoneyAction {
	return []MoneyAction{
		{Name: "list", Method: "GET", Href: "/api/v1/pos"},
		{Name: "self", Method: "GET", Href: "/api/v1/pos/" + posID},
	}
}

// createPos normalizes via logic/pos, validates via logic/pos, then
// inserts via dbq.CreatePos.
//
// Returns:
//   - (result, nil, nil) on success.
//   - (zero, violations, nil) when input fails validation — caller maps
//     to 400 (API, violations[0]) or re-renders the form (web, full list).
//   - (zero, nil, *posConflictError) on UNIQUE (name, currency).
//   - (zero, nil, *posAccountNotFoundError) on FK account_id.
//   - (zero, nil, err) on DB failure — caller maps to 500 / retry message.
func createPos(
	ctx context.Context,
	pool *pgxpool.Pool,
	in PosCreateInput,
) (APIPos, []string, error) {
	var zero APIPos

	normalized := logicpos.Normalize(logicpos.CreateInput{
		Name:      in.Name,
		Currency:  in.Currency,
		AccountID: in.AccountID.String(),
		Target:    derefTarget(in.Target),
		HasTarget: in.Target != nil,
	})
	if errs := logicpos.Validate(normalized); len(errs) > 0 {
		return zero, errs, nil
	}

	params := dbq.CreatePosParams{
		Name:      normalized.Name,
		Currency:  normalized.Currency,
		AccountID: pgtype.UUID{Bytes: in.AccountID, Valid: true},
	}
	if in.Target != nil {
		t := *in.Target
		params.Target = &t
	}
	row, err := dbq.New(pool).CreatePos(ctx, params)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return zero, nil, &posConflictError{msg: "a Pos with that name and currency already exists"}
		}
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return zero, nil, &posAccountNotFoundError{msg: "account_id does not refer to an existing account"}
		}
		return zero, nil, err
	}
	return posRowToAPI(row), nil, nil
}

// updatePos applies a rename/target change and/or an account move in
// one shared path. Name/target go through UpdatePosNameAndTarget,
// the move through UpdatePosAccount (snapshot semantics per spec
// §5.6 — balances re-attribute on next read, no ledger entry).
//
// Validation messages use the API's lowercase wording; the web rename
// handler pre-checks with its own capitalized wording before calling
// in, so its messages win on that path and behavior stays identical.
//
// Returns:
//   - (result, nil, nil) on success (fully-populated row for partial PATCH).
//   - (zero, violations, nil) on bad name/target — caller maps to 400.
//   - (zero, nil, *posNotFoundError) when the pos is missing.
//   - (zero, nil, *posConflictError) on UNIQUE (name, currency).
//   - (zero, nil, *posAccountNotFoundError) on FK account_id.
//   - (zero, nil, err) on DB failure.
func updatePos(
	ctx context.Context,
	pool *pgxpool.Pool,
	in PosUpdateInput,
) (APIPos, []string, error) {
	var zero APIPos

	q := dbq.New(pool)
	current, err := q.GetPos(ctx, pgtype.UUID{Bytes: in.PosID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil, &posNotFoundError{msg: "pos not found"}
		}
		return zero, nil, err
	}

	if in.Name != nil || in.TargetSet {
		newName := current.Name
		if in.Name != nil {
			newName = strings.TrimSpace(*in.Name)
			if newName == "" {
				return zero, []string{"name must not be empty"}, nil
			}
		}
		var newTarget *int64
		if in.TargetSet {
			if in.Target != nil {
				t := *in.Target
				if t < 0 {
					return zero, []string{"target must be zero or positive"}, nil
				}
				newTarget = &t
			} else {
				newTarget = nil
			}
		} else {
			newTarget = current.Target
		}
		row, err := q.UpdatePosNameAndTarget(ctx, dbq.UpdatePosNameAndTargetParams{
			ID:     pgtype.UUID{Bytes: in.PosID, Valid: true},
			Name:   newName,
			Target: newTarget,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, nil, &posNotFoundError{msg: "pos not found"}
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return zero, nil, &posConflictError{msg: "a Pos with that name and currency already exists"}
			}
			return zero, nil, err
		}
		current = row
	}

	if in.AccountID != nil {
		row, err := q.UpdatePosAccount(ctx, dbq.UpdatePosAccountParams{
			ID:        pgtype.UUID{Bytes: in.PosID, Valid: true},
			AccountID: pgtype.UUID{Bytes: *in.AccountID, Valid: true},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, nil, &posNotFoundError{msg: "pos not found"}
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return zero, nil, &posAccountNotFoundError{msg: "account_id does not refer to an existing account"}
			}
			return zero, nil, err
		}
		current = row
	}

	return posRowToAPI(current), nil, nil
}

// archivePos soft-deletes a Pos (archived rows are preserved and
// excluded from default listings per spec §10.3). Looks up first so
// API callers can distinguish 404 from success; web callers map
// not-found to their existing redirect target.
func archivePos(
	ctx context.Context,
	pool *pgxpool.Pool,
	id uuid.UUID,
) error {
	q := dbq.New(pool)
	if _, err := q.GetPos(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &posNotFoundError{msg: "pos not found"}
		}
		return err
	}
	return q.ArchivePos(ctx, pgtype.UUID{Bytes: id, Valid: true})
}

func derefTarget(t *int64) int64 {
	if t == nil {
		return 0
	}
	return *t
}
