// Package obligation models the spec §4.3 borrow-mode debt-tracking
// machinery: when an inter_pos transaction has mode = "borrow", each
// (creditor, debtor) line pair produces a pos_obligation row whose
// amount is the debtor's in amount prorated by the creditor's share
// of total out. Pairs whose prorated amount rounds to zero are dropped
// from the output (they would violate the storage CHECK
// `amount_owed > 0` and represent no actual debt); the residual is
// always carried by a creditor with a non-zero share so the per-debtor
// sum stays exact.
//
// This package is pure. No SQL, no time.Now, no rand.
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
// the source of truth for FIFO ordering — MatchRepayments sorts by it
// on entry, so callers don't need to pre-sort the slice they pass.
type Obligation struct {
	ID            string
	TransactionID string
	CreditorPosID string
	DebtorPosID   string
	Currency      string // always the debtor's
	Owed          int64  // > 0
	Repaid        int64  // >= 0 and <= Owed
	CreatedAt     time.Time
	ClearedAt     *time.Time // non-nil iff Repaid >= Owed (§10.7)
}

// IsCleared reports the §10.7 invariant.
func (o Obligation) IsCleared() bool { return o.ClearedAt != nil }

// Validate returns nil iff every storage-side and §10.7 invariant holds.
// This mirrors the migration CHECKs end-to-end so a hand-built Obligation
// that would be rejected by the DB is also rejected here.
func (o Obligation) Validate() error {
	if o.Owed <= 0 {
		return fmt.Errorf("obligation %s: owed must be > 0, got %d", o.ID, o.Owed)
	}
	if o.Repaid < 0 {
		return fmt.Errorf("obligation %s: repaid must be >= 0, got %d", o.ID, o.Repaid)
	}
	if o.Repaid > o.Owed {
		return fmt.Errorf("obligation %s: repaid %d exceeds owed %d (overage must spawn reverse, not overshoot)",
			o.ID, o.Repaid, o.Owed)
	}
	if o.CreditorPosID == o.DebtorPosID {
		return fmt.Errorf("obligation %s: pos cannot owe itself (creditor=debtor=%s)",
			o.ID, o.CreditorPosID)
	}
	clearedExpected := o.Repaid >= o.Owed
	clearedActual := o.IsCleared()
	if clearedExpected != clearedActual {
		return fmt.Errorf("obligation %s: §10.7 violated — repaid=%d owed=%d cleared=%t",
			o.ID, o.Repaid, o.Owed, clearedActual)
	}
	return nil
}

// Sentinel errors so callers can errors.Is. Every error returned by this
// package wraps one of these via fmt.Errorf("%w: …").
var (
	ErrCrossCurrencyBorrow = errors.New("obligation: cross-currency borrow not yet supported")
	ErrEmptyBorrow         = errors.New("obligation: borrow needs at least one out and one in line")
	ErrNonPositiveAmount   = errors.New("obligation: amount must be > 0")
	ErrUnknownDirection    = errors.New("obligation: unknown direction")
	ErrUnbalancedLines     = errors.New("obligation: out total does not equal in total")
	ErrInvalidCurrency     = errors.New("obligation: currency must match ^[a-z0-9-]+$")
	ErrSelfDebt            = errors.New("obligation: pos cannot owe itself")
	ErrDuplicateID         = errors.New("obligation: duplicate Obligation.ID in input")
)

// GenerateBorrowObligations takes the lines of an inter_pos borrow event
// and returns one Obligation per (creditor, debtor) pair whose prorated
// amount is > 0. Pairs whose share rounds to zero are dropped — they
// represent no actual debt and would violate the storage CHECK.
//
// Per debtor, amounts are computed as floor(creditor_out * debtor_in /
// total_out). The remainder is carried by the creditor with the largest
// non-zero share, ensuring the per-debtor sum equals debtor_in exactly
// regardless of input ordering or which creditor's share happens to round.
//
// outs and ins are sorted by PosID before processing so the same input
// set always produces the same output rows.
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

	sort.SliceStable(outs, func(i, j int) bool { return outs[i].PosID < outs[j].PosID })
	sort.SliceStable(ins, func(i, j int) bool { return ins[i].PosID < ins[j].PosID })

	out := make([]Obligation, 0, len(outs)*len(ins))
	for _, in := range ins {
		// Hamilton's largest-remainder apportionment per debtor.
		//
		// Self-debt is silently skipped (a Pos cannot owe itself; the
		// storage CHECK agrees). Critically, when the self-creditor is
		// excluded we must also remove its share from the denominator —
		// otherwise the per-debtor sum would short by the self-creditor's
		// implied portion, and the residual loop would silently drop
		// units rather than carry the full debtor amount across the
		// remaining creditors.
		// Sum ALL self-creditor amounts (a PosID can appear multiple
		// times in outs); each contributes to the share that must be
		// excluded from the residual denominator for this debtor.
		var selfOut int64
		for _, cred := range outs {
			if cred.PosID == in.PosID {
				selfOut += cred.Amount
			}
		}
		effTotalOut := totalOut - selfOut
		if effTotalOut == 0 {
			// Only the debtor itself is on the out side; nothing is owed.
			continue
		}

		shares := make([]int64, len(outs))
		remainders := make([]int64, len(outs))
		eligible := make([]int, 0, len(outs))
		var allocated int64
		for ci, cred := range outs {
			if cred.PosID == in.PosID {
				continue
			}
			shares[ci] = (cred.Amount * in.Amount) / effTotalOut
			remainders[ci] = (cred.Amount * in.Amount) % effTotalOut
			allocated += shares[ci]
			eligible = append(eligible, ci)
		}
		residual := in.Amount - allocated
		sort.SliceStable(eligible, func(a, b int) bool {
			return remainders[eligible[a]] > remainders[eligible[b]]
		})
		// residual is bounded by len(eligible) since each remainder is
		// strictly less than effTotalOut and Σ remainders < len * eff;
		// after eligible-many +1 bumps, residual is fully absorbed.
		for r := int64(0); r < residual; r++ {
			shares[eligible[int(r)%len(eligible)]]++
		}

		for ci, cred := range outs {
			if shares[ci] <= 0 {
				continue
			}
			out = append(out, Obligation{
				ID:            idGen(),
				TransactionID: txnID,
				CreditorPosID: cred.PosID,
				DebtorPosID:   in.PosID,
				Currency:      currency,
				Owed:          shares[ci],
				Repaid:        0,
				CreatedAt:     createdAt,
				ClearedAt:     nil,
			})
		}
	}
	return out, nil
}

// RepaymentLine is a payment from one Pos to another. The names mirror
// the obligation it would satisfy: a payment with DebtorPosID=D and
// CreditorPosID=C pays down an open Obligation{Creditor=C, Debtor=D, …}.
type RepaymentLine struct {
	DebtorPosID   string // pos paying down (was the debtor on the original obligation)
	CreditorPosID string // pos being repaid (was the creditor)
	Currency      string
	Amount        int64 // > 0
}

// MatchPlan is the calculated mutation set the caller persists atomically.
type MatchPlan struct {
	// Progressed: existing obligations whose Repaid (and possibly
	// ClearedAt) changed. May contain still-open rows with Repaid bumped,
	// or freshly-cleared rows where ClearedAt was just stamped.
	Progressed []Obligation
	// ReverseObligations: NEW rows in the OPPOSITE direction. A payment
	// that exceeds the open balance for its (creditor, debtor) pair, or
	// finds no open balance at all, becomes a fresh obligation in the
	// reverse direction. See spec §4.3.
	ReverseObligations []Obligation
}

// MatchRepayments applies repayments to open obligations using FIFO
// ordering by CreatedAt (oldest first). MatchRepayments owns the sort
// so the FIFO contract lives in one place.
//
// Payments are processed in input order. Each payment's overflow becomes
// one ReverseObligation in that same order, with deterministic CreatedAt
// (now + index nanoseconds) so a downstream FIFO match across these new
// rows is stable across runs even when multiple are spawned in one call.
//
// Returns ErrInvalidCurrency / ErrNonPositiveAmount / ErrSelfDebt /
// ErrDuplicateID on bad input shapes.
func MatchRepayments(open []Obligation, payments []RepaymentLine, repaymentTxnID string, now time.Time, idGen func() string) (MatchPlan, error) {
	seenID := map[string]struct{}{}
	for _, o := range open {
		if err := o.Validate(); err != nil {
			return MatchPlan{}, err
		}
		if _, dup := seenID[o.ID]; dup {
			return MatchPlan{}, fmt.Errorf("%w: %s", ErrDuplicateID, o.ID)
		}
		seenID[o.ID] = struct{}{}
	}
	for _, p := range payments {
		if p.Amount <= 0 {
			return MatchPlan{}, fmt.Errorf("%w: payment %s→%s amount %d",
				ErrNonPositiveAmount, p.DebtorPosID, p.CreditorPosID, p.Amount)
		}
		if !validCurrency(p.Currency) {
			return MatchPlan{}, fmt.Errorf("%w: payment %s→%s currency %q",
				ErrInvalidCurrency, p.DebtorPosID, p.CreditorPosID, p.Currency)
		}
		if p.DebtorPosID == p.CreditorPosID {
			return MatchPlan{}, fmt.Errorf("%w: payment %s→%s",
				ErrSelfDebt, p.DebtorPosID, p.CreditorPosID)
		}
	}
	plan := MatchPlan{}

	type key struct{ Creditor, Debtor, Currency string }
	buckets := map[key][]int{}
	for i, o := range open {
		k := key{Creditor: o.CreditorPosID, Debtor: o.DebtorPosID, Currency: o.Currency}
		buckets[k] = append(buckets[k], i)
	}
	for k, idxs := range buckets {
		// FIFO by CreatedAt; ID as a deterministic tiebreaker.
		sort.SliceStable(idxs, func(a, b int) bool {
			A, B := open[idxs[a]], open[idxs[b]]
			if !A.CreatedAt.Equal(B.CreatedAt) {
				return A.CreatedAt.Before(B.CreatedAt)
			}
			return A.ID < B.ID
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

	for pi, p := range payments {
		k := key{Creditor: p.CreditorPosID, Debtor: p.DebtorPosID, Currency: p.Currency}
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
			// Distinct CreatedAt per spawned reverse so FIFO across them
			// is deterministic within this call. (Across calls with the
			// same `now`, FIFO ties break on ID — see the bucket sort
			// above.)
			ts := now.Add(time.Duration(pi) * time.Nanosecond)
			rev := spawnReverse(p, remaining, repaymentTxnID, ts, idGen)
			if _, dup := seenID[rev.ID]; dup {
				return MatchPlan{}, fmt.Errorf("%w: spawned reverse collides with %s", ErrDuplicateID, rev.ID)
			}
			seenID[rev.ID] = struct{}{}
			plan.ReverseObligations = append(plan.ReverseObligations, rev)
		}
	}

	idxs := make([]int, 0, len(updated))
	for i := range updated {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		plan.Progressed = append(plan.Progressed, updated[i])
	}
	return plan, nil
}

// spawnReverse: a payment that overshoots the open balance (or finds
// none) becomes a fresh obligation in the OPPOSITE direction. The
// creditor (formerly recipient) now owes the debtor (formerly payer).
func spawnReverse(p RepaymentLine, amount int64, repaymentTxnID string, createdAt time.Time, idGen func() string) Obligation {
	return Obligation{
		ID:            idGen(),
		TransactionID: repaymentTxnID,
		CreditorPosID: p.DebtorPosID,   // formerly debtor (now owed by C)
		DebtorPosID:   p.CreditorPosID, // formerly creditor (now owes D)
		Currency:      p.Currency,
		Owed:          amount,
		Repaid:        0,
		CreatedAt:     createdAt,
		ClearedAt:     nil,
	}
}

// validCurrency mirrors the storage CHECK `^[a-z0-9-]+$`.
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
