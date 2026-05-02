// Package incometemplate computes the per-Pos allocation when an
// incoming amount is applied against a saved template. Pure: no DB,
// no time. The handler does the I/O; this file holds the rule.
//
// Apply rules:
//
//	amount  <  Σ(lines)                 → ErrAmountBelowTemplate
//	amount ==  Σ(lines)                 → split exactly per lines
//	amount  >  Σ(lines)
//	   AND HasLeftoverPos               → split per lines + remainder
//	   AND !HasLeftoverPos              → ErrAmountExceedsTemplate
package incometemplate

import "errors"

// Line is one allocation entry on a template.
type Line struct {
	ID     string // stable identifier (e.g. uuid string), passed through to the derived idempotency key
	PosID  string
	Amount int64
}

// Template is the validation-relevant slice of an income_template
// row plus its lines.
type Template struct {
	ID             string
	Name           string
	Lines          []Line
	LeftoverPosID  string // empty when not configured
	HasLeftoverPos bool
}

// Allocation is one row in the materialized allocation produced by
// Apply. The handler converts each into a money_in transaction.
type Allocation struct {
	// LineID is the source template-line id, or "leftover" for the
	// remainder allocation. Used to derive a deterministic
	// idempotency_key on the generated money_in row.
	LineID string
	PosID  string
	Amount int64
}

var (
	// ErrAmountBelowTemplate is returned when the incoming amount is
	// strictly less than the sum of template lines, regardless of
	// whether a leftover Pos is configured.
	ErrAmountBelowTemplate = errors.New("incometemplate: amount below template total")

	// ErrAmountExceedsTemplate is returned when the incoming amount
	// is greater than Σ(lines) AND the template has no leftover Pos
	// to absorb the remainder.
	ErrAmountExceedsTemplate = errors.New("incometemplate: amount exceeds template total and no leftover Pos configured")

	// ErrEmptyTemplate is returned when the template has zero lines —
	// applying it would expand to zero transactions, which is almost
	// certainly an operator misconfiguration.
	ErrEmptyTemplate = errors.New("incometemplate: template has no lines")

	// ErrNonPositiveAmount is returned when the incoming amount is
	// zero or negative.
	ErrNonPositiveAmount = errors.New("incometemplate: amount must be positive")
)

// Apply computes the allocation for a given (template, amount) pair.
// On success returns N allocations: one per line, optionally followed
// by a leftover entry when amount > Σ(lines) and the template
// configures a leftover Pos.
//
// The function is pure — same inputs always produce the same output.
func Apply(t Template, amount int64) ([]Allocation, error) {
	if amount <= 0 {
		return nil, ErrNonPositiveAmount
	}
	if len(t.Lines) == 0 {
		return nil, ErrEmptyTemplate
	}

	var sum int64
	for _, l := range t.Lines {
		sum += l.Amount
	}

	if amount < sum {
		return nil, ErrAmountBelowTemplate
	}

	out := make([]Allocation, 0, len(t.Lines)+1)
	for _, l := range t.Lines {
		out = append(out, Allocation{
			LineID: l.ID, PosID: l.PosID, Amount: l.Amount,
		})
	}

	if amount == sum {
		return out, nil
	}

	// amount > sum
	if !t.HasLeftoverPos {
		return nil, ErrAmountExceedsTemplate
	}
	out = append(out, Allocation{
		LineID: "leftover",
		PosID:  t.LeftoverPosID,
		Amount: amount - sum,
	})
	return out, nil
}
