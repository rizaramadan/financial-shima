// Package incometemplate computes the per-Pos allocation when an
// incoming amount is applied against a saved template. Pure: no DB,
// no time. The handler does the I/O; this file holds the rule.
//
// Apply rules (fixed-only templates behave exactly as before):
//
//	amount  <  fixed Σ + percent share   → ErrAmountBelowTemplate
//	amount ==  fixed Σ + percent share   → split per lines
//	amount  >  fixed Σ + percent share
//	   AND HasLeftoverPos               → split per lines + remainder
//	   AND !HasLeftoverPos              → ErrAmountExceedsTemplate
//
// Invariants: Σ(line percents) ≤ 100%; on success Σ(allocations)
// == amount; each percent line is within 1 unit of its exact share.
package incometemplate

import "errors"

// Line is one allocation entry on a template. A line is either
// fixed (Amount > 0), percent (PercentBps 1..10000, i.e. basis
// points of the incoming amount), or both — in which case the
// allocation is the fixed amount plus the Hamilton share of the
// percent part.
type Line struct {
	ID     string // stable identifier (e.g. uuid string), passed through to the derived idempotency key
	PosID  string
	Amount int64
	// PercentBps is the percent of the incoming amount in basis
	// points (6000 = 60%). Zero means "no percent part".
	PercentBps int
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

	// ErrInvalidPercent is returned when a line's percent is outside
	// 1..10000 basis points.
	ErrInvalidPercent = errors.New("incometemplate: percent_bps must be between 1 and 10000")

	// ErrPercentTotalExceeds is returned when Σ(line percents) >
	// 100% — the template claims more than the incoming amount.
	ErrPercentTotalExceeds = errors.New("incometemplate: total percent exceeds 100%")

	// ErrInvalidLine is returned when a line carries neither a
	// positive fixed amount nor a percent part.
	ErrInvalidLine = errors.New("incometemplate: line must have a positive amount or percent")

	// ErrNonPositiveAmount is returned when the incoming amount is
	// zero or negative.
	ErrNonPositiveAmount = errors.New("incometemplate: amount must be positive")
)

// Apply computes the allocation for a given (template, amount) pair.
// Fixed lines contribute their Amount; percent lines contribute
// PercentBps/10000 of the incoming amount apportioned by Hamilton's
// largest-remainder method (same pattern as logic/obligation), so
// each percent line is within 1 unit of its exact share and the
// percent parts sum to exactly ⌊Σbps·amount/10000⌋.
//
// Success always yields Σ(allocations) == amount: the fixed parts
// plus the percent shares, plus — when those fall short — a
// leftover entry for the remainder. Without a leftover Pos the
// shortfall (or any amount below the fixed+percent demand) is
// rejected, exactly as in the fixed-only rules.
func Apply(t Template, amount int64) ([]Allocation, error) {
	if amount <= 0 {
		return nil, ErrNonPositiveAmount
	}
	if len(t.Lines) == 0 {
		return nil, ErrEmptyTemplate
	}

	var fixedSum, totalBps int64
	for _, l := range t.Lines {
		if l.Amount < 0 {
			return nil, ErrInvalidLine
		}
		if l.PercentBps < 0 || l.PercentBps > 10000 {
			return nil, ErrInvalidPercent
		}
		if l.Amount == 0 && l.PercentBps == 0 {
			return nil, ErrInvalidLine
		}
		fixedSum += l.Amount
		totalBps += int64(l.PercentBps)
	}
	if totalBps > 10000 {
		return nil, ErrPercentTotalExceeds
	}

	// Hamilton apportionment of the percent pool across percent
	// lines. Overflow-safe: amount = q·10000 + r, so
	// ⌊p·amount/10000⌋ = p·q + ⌊p·r/10000⌋ with p·q ≤ amount.
	q, r := amount/10000, amount%10000
	totalPool := totalBps*q + (totalBps*r)/10000
	floors := make([]int64, len(t.Lines))
	rems := make([]int64, len(t.Lines))
	var floorSum int64
	for i, l := range t.Lines {
		p := int64(l.PercentBps)
		if p == 0 {
			continue
		}
		floors[i] = p*q + (p*r)/10000
		rems[i] = (p * r) % 10000
		floorSum += floors[i]
	}
	residual := totalPool - floorSum
	for residual > 0 {
		best := -1
		for i, l := range t.Lines {
			if l.PercentBps == 0 {
				continue
			}
			if best == -1 || rems[i] > rems[best] {
				best = i
			}
		}
		floors[best]++
		rems[best] = -1 // each line wins at most one extra unit
		residual--
	}

	required := fixedSum + totalPool
	if required > amount {
		return nil, ErrAmountBelowTemplate
	}

	out := make([]Allocation, 0, len(t.Lines)+1)
	for i, l := range t.Lines {
		total := l.Amount + floors[i]
		if total == 0 {
			// A dust share (e.g. 1bps of a tiny amount) rounds to
			// zero — it is no allocation, same as obligation's
			// dropped zero pairs. Skipping keeps the storage
			// CHECK (amount > 0) intact without moving the sum.
			continue
		}
		out = append(out, Allocation{
			LineID: l.ID, PosID: l.PosID, Amount: total,
		})
	}

	if required == amount {
		return out, nil
	}

	// required < amount
	if !t.HasLeftoverPos {
		return nil, ErrAmountExceedsTemplate
	}
	out = append(out, Allocation{
		LineID: "leftover",
		PosID:  t.LeftoverPosID,
		Amount: amount - required,
	})
	return out, nil
}
