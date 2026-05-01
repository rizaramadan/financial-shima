// Package obligation models the spec §4.3 borrow-mode debt-tracking
// machinery: when an inter_pos transaction has mode = "borrow", each
// `out` line × `in` line pair produces a pos_obligation row whose
// amount is the debtor's in amount prorated by the creditor's share
// of total out. Pairs whose prorated amount rounds to zero are dropped
// from the output (they would violate the storage CHECK
// `amount_owed > 0` and represent no actual debt).
//
// This package is pure — it computes the obligation rows and the
// repayment match plan from inputs; the caller persists the result.
// No SQL, no time.Now, no rand.
//
// Spec references:
//   - §4.3 borrow-mode debt tracking ("Pattern P from interview").
//   - §5.4 negative balances are first-class.
//   - §10.7 cleared_at is set if and only if amount_repaid >= amount_owed.
package obligation

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Line is one inter_pos line.
type Line struct {
	PosID     string
	Currency  string
	Direction Direction
	Amount    int64 // strictly positive
}

type Direction string

const (
	DirOut Direction = "out"
	DirIn  Direction = "in"
)

// Obligation represents a single creditor-debtor debt row. CreatedAt is
// the source of truth for FIFO ordering — Match sorts by it on entry,
// so callers don't need to pre-sort the slice they hand in.
type Obligation struct {
	ID            string
	TransactionID string
	CreditorPosID string
	DebtorPosID   string
	Currency      string // always the debtor's
	Owed          int64  // > 0
	Repaid        int64  // >= 0
	CreatedAt     time.Time
	ClearedAt     *time.Time // non-nil iff Repaid >= Owed (§10.7)
}

// IsCleared reports the §10.7 invariant.
func (o Obligation) IsCleared() bool { return o.ClearedAt != nil }

// Validate returns nil iff the §10.7 invariant holds AND the storage
// constraints from migration 0003 are satisfied (Owed > 0, Repaid >= 0).
func (o Obligation) Validate() error {
	if o.Owed <= 0 {
		return fmt.Errorf("obligation %s: owed must be > 0, got %d", o.ID, o.Owed)
	}
	if o.Repaid < 0 {
		return fmt.Errorf("obligation %s: repaid must be >= 0, got %d", o.ID, o.Repaid)
	}
	clearedExpected := o.Repaid >= o.Owed
	clearedActual := o.IsCleared()
	if clearedExpected != clearedActual {
		return fmt.Errorf("obligation %s: §10.7 violated — repaid=%d owed=%d cleared=%t",
			o.ID, o.Repaid, o.Owed, clearedActual)
	}
	return nil
}

// Sentinel errors so callers can errors.Is. The package error grammar:
// every error is wrapped via fmt.Errorf("%w: …") with a sentinel base.
var (
	ErrCrossCurrencyBorrow = errors.New("obligation: cross-currency borrow not yet supported")
	ErrEmptyBorrow         = errors.New("obligation: borrow needs at least one out and one in line")
	ErrNonPositiveAmount   = errors.New("obligation: amount must be > 0")
	ErrUnknownDirection    = errors.New("obligation: unknown direction")
	ErrUnbalancedLines     = errors.New("obligation: out total does not equal in total")
	ErrInvalidCurrency     = errors.New("obligation: currency must match ^[a-z0-9-]+$")
)

// GenerateBorrowObligations takes the lines of an inter_pos borrow event
// and returns one Obligation per (creditor, debtor) pair whose prorated
// amount is > 0. Pairs whose share rounds to zero are dropped — they
// represent no actual debt and would violate the storage CHECK.
//
// The amount each creditor owes each debtor is:
//
//	creditor_share = creditor_out / total_out
//	owed           = floor(creditor_out * debtor_in / total_out)
//
// To preserve per-debtor sum invariance under integer arithmetic, the
// last creditor in a debtor's bucket absorbs any rounding residual.
// outs and ins are sorted by PosID before processing so the same input
// set always produces the same output rows (caller may pass map-walk
// order without leaking nondeterminism).
func GenerateBorrowObligations(txnID string, lines []Line, createdAt time.Time, idGen func() string) ([]Obligation, error) {
	if len(lines) == 0 {
		return nil, ErrEmptyBorrow
	}
	currency := lines[0].Currency
	if !validCurrency(currency) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidCurrency, currency)
	}
	for _, l := range lines {
		if l.Currency != currency {
			return nil, fmt.Errorf("%w: %q vs %q", ErrCrossCurrencyBorrow, currency, l.Currency)
		}
		if l.Amount <= 0 {
			return nil, fmt.Errorf("%w: pos %s amount %d", ErrNonPositiveAmount, l.PosID, l.Amount)
		}
	}

	var (
		outs     []Line
		ins      []Line
		totalOut int64
	)
	for _, l := range lines {
		switch l.Direction {
		case DirOut:
			outs = append(outs, l)
			totalOut += l.Amount
		case DirIn:
			ins = append(ins, l)
		default:
			return nil, fmt.Errorf("%w: %q", ErrUnknownDirection, l.Direction)
		}
	}
	if len(outs) == 0 || len(ins) == 0 {
		return nil, ErrEmptyBorrow
	}

	var totalIn int64
	for _, l := range ins {
		totalIn += l.Amount
	}
	if totalIn != totalOut {
		return nil, fmt.Errorf("%w: out=%d in=%d", ErrUnbalancedLines, totalOut, totalIn)
	}

	// Determinism: sort by PosID so the "last creditor absorbs rounding"
	// rule is a function of the input set, not the input slice order.
	sort.SliceStable(outs, func(i, j int) bool { return outs[i].PosID < outs[j].PosID })
	sort.SliceStable(ins, func(i, j int) bool { return ins[i].PosID < ins[j].PosID })

	out := make([]Obligation, 0, len(outs)*len(ins))
	for _, in := range ins {
		var allocated int64
		for ci, cred := range outs {
			var amount int64
			if ci == len(outs)-1 {
				amount = in.Amount - allocated
			} else {
				amount = (cred.Amount * in.Amount) / totalOut
				allocated += amount
			}
			if amount <= 0 {
				continue // drop zero-share rows; storage CHECK requires owed > 0
			}
			out = append(out, Obligation{
				ID:            idGen(),
				TransactionID: txnID,
				CreditorPosID: cred.PosID,
				DebtorPosID:   in.PosID,
				Currency:      currency,
				Owed:          amount,
				Repaid:        0,
				CreatedAt:     createdAt,
				ClearedAt:     nil,
			})
		}
	}
	return out, nil
}

// RepaymentLine is a payment from one Pos to another in the reverse
// direction of an existing obligation: FromPos is the original debtor
// paying down, ToPos is the original creditor receiving.
type RepaymentLine struct {
	FromPos  string
	ToPos    string
	Currency string
	Amount   int64 // > 0
}

// MatchPlan is the calculated mutation set the caller persists atomically.
type MatchPlan struct {
	// Updates: existing obligations whose Repaid (and possibly ClearedAt)
	// changed.
	Updates []Obligation
	// ReverseObligations: NEW rows in the OPPOSITE direction, spawned
	// when a repayment exceeded the open balance for its (creditor,
	// debtor) pair (the "kid's school cash short after gold drop" case
	// from spec §4.3) or when no open obligation existed for the pair
	// at all (one Pos starts owing another spontaneously).
	ReverseObligations []Obligation
}

// MatchRepayments applies repayments to open obligations using FIFO
// ordering by CreatedAt (oldest first). Match itself does the sort —
// callers may pass open obligations in any order.
//
// repaymentTxnID is stamped onto every newly-spawned reverse obligation
// so the caller never receives a row with empty TransactionID.
//
// `now` is the time stamped onto a freshly-cleared obligation's
// ClearedAt and onto reverse obligations' CreatedAt.
//
// Returns ErrInvalidCurrency / ErrNonPositiveAmount on bad payment
// shapes; otherwise the input is treated as authoritative.
func MatchRepayments(open []Obligation, payments []RepaymentLine, repaymentTxnID string, now time.Time, idGen func() string) (MatchPlan, error) {
	for _, o := range open {
		if err := o.Validate(); err != nil {
			return MatchPlan{}, err
		}
	}
	for _, p := range payments {
		if p.Amount <= 0 {
			return MatchPlan{}, fmt.Errorf("%w: payment %s→%s amount %d",
				ErrNonPositiveAmount, p.FromPos, p.ToPos, p.Amount)
		}
		if !validCurrency(p.Currency) {
			return MatchPlan{}, fmt.Errorf("%w: payment %s→%s currency %q",
				ErrInvalidCurrency, p.FromPos, p.ToPos, p.Currency)
		}
	}
	plan := MatchPlan{}

	// Bucket open obligations by (creditor, debtor, currency) and sort
	// each bucket FIFO by CreatedAt. The Match function owns the sort —
	// see issue #3 in the Phase-8 review (caller couldn't honor a
	// FIFO contract because the struct didn't expose CreatedAt).
	type key struct{ Creditor, Debtor, Currency string }
	buckets := map[key][]int{}
	for i, o := range open {
		k := key{Creditor: o.CreditorPosID, Debtor: o.DebtorPosID, Currency: o.Currency}
		buckets[k] = append(buckets[k], i)
	}
	for k := range buckets {
		idxs := buckets[k]
		sort.SliceStable(idxs, func(a, b int) bool {
			return open[idxs[a]].CreatedAt.Before(open[idxs[b]].CreatedAt)
		})
		buckets[k] = idxs
	}

	updated := map[int]Obligation{}
	getCurrent := func(i int) Obligation {
		if o, ok := updated[i]; ok {
			return o
		}
		return open[i]
	}

	for _, p := range payments {
		k := key{Creditor: p.ToPos, Debtor: p.FromPos, Currency: p.Currency}
		remaining := p.Amount
		for _, idx := range buckets[k] {
			if remaining == 0 {
				break
			}
			cur := getCurrent(idx)
			if cur.IsCleared() {
				continue
			}
			gap := cur.Owed - cur.Repaid
			pay := gap
			if remaining < gap {
				pay = remaining
			}
			cur.Repaid += pay
			remaining -= pay
			if cur.Repaid >= cur.Owed {
				clearedAt := now
				cur.ClearedAt = &clearedAt
			}
			updated[idx] = cur
		}
		if remaining > 0 {
			plan.ReverseObligations = append(plan.ReverseObligations, spawnReverse(p, remaining, repaymentTxnID, now, idGen))
		}
	}

	// Stable update list ordered by original index.
	idxs := make([]int, 0, len(updated))
	for i := range updated {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		plan.Updates = append(plan.Updates, updated[i])
	}
	return plan, nil
}

// spawnReverse names the package's most distinctive behavior: an
// overpayment (or a payment with no matching open obligation) creates a
// new debt in the OPPOSITE direction. Surfaced as a helper so the
// behavior has a name in the call graph, not just in a comment.
func spawnReverse(p RepaymentLine, amount int64, repaymentTxnID string, now time.Time, idGen func() string) Obligation {
	return Obligation{
		ID:            idGen(),
		TransactionID: repaymentTxnID,
		CreditorPosID: p.FromPos, // formerly debtor
		DebtorPosID:   p.ToPos,   // formerly creditor
		Currency:      p.Currency,
		Owed:          amount,
		Repaid:        0,
		CreatedAt:     now,
		ClearedAt:     nil,
	}
}

// validCurrency mirrors the storage CHECK `^[a-z0-9-]+$`. We don't compile
// a regexp here — the predicate is small and runs in the hot path of every
// borrow / repayment.
func validCurrency(c string) bool {
	if c == "" {
		return false
	}
	for _, r := range c {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}
