// Package obligation models the spec §4.3 borrow-mode debt-tracking
// machinery: when an inter_pos transaction has mode = "borrow", each
// `out` line × `in` line pair produces a pos_obligation row. The
// obligation is denominated in the debtor's currency.
//
// This package is pure — it computes the obligation rows and the
// repayment match plan from inputs, then the caller commits the result
// to the DB. No SQL, no time.Now, no rand.
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

// Line is one inter_pos line — same shape as logic/balance but kept
// here-package-local to avoid coupling.
type Line struct {
	PosID    string
	Currency string
	Direction Direction
	Amount    int64 // positive
}

type Direction string

const (
	DirOut Direction = "out"
	DirIn  Direction = "in"
)

// Obligation represents a single creditor-debtor debt row.
type Obligation struct {
	ID            string
	TransactionID string
	CreditorPosID string
	DebtorPosID   string
	Currency      string // always the debtor's
	Owed          int64
	Repaid        int64
	ClearedAt     *time.Time // non-nil ⇔ Repaid >= Owed (spec §10.7)
}

// IsCleared reports the §10.7 invariant: ClearedAt is set iff Repaid >= Owed.
func (o Obligation) IsCleared() bool { return o.ClearedAt != nil }

// Validate returns nil if the §10.7 invariant holds.
func (o Obligation) Validate() error {
	if o.Repaid < 0 {
		return fmt.Errorf("obligation %s: negative repaid %d", o.ID, o.Repaid)
	}
	if o.Owed < 0 {
		return fmt.Errorf("obligation %s: negative owed %d", o.ID, o.Owed)
	}
	clearedExpected := o.Repaid >= o.Owed
	clearedActual := o.IsCleared()
	if clearedExpected != clearedActual {
		return fmt.Errorf("obligation %s: §10.7 violated — repaid=%d owed=%d cleared=%t",
			o.ID, o.Repaid, o.Owed, clearedActual)
	}
	return nil
}

// ErrCrossCurrencyBorrow signals a borrow whose creditors and debtor don't
// share a currency — out of scope for the Phase-8 single-currency cut.
// Cross-currency borrow needs an exchange-rate input the spec doesn't yet
// define and ships in a later phase.
var ErrCrossCurrencyBorrow = errors.New("obligation: cross-currency borrow not yet supported")

// GenerateForBorrow takes the lines of an inter_pos transaction with
// mode = "borrow" and returns one Obligation per (creditor, debtor) pair.
//
// Phase-8 scope: every line shares one currency. The amount each creditor
// owes the debtor is prorated by the creditor's share of total out
// against the debtor's in amount.
//
// `now` is the issuance time, recorded in the transaction (passed by caller);
// ClearedAt is left nil because no payment has applied yet.
//
// `idGen` produces unique IDs for the obligation rows. Pure — no I/O.
func GenerateForBorrow(txnID string, lines []Line, idGen func() string) ([]Obligation, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	currency := lines[0].Currency
	for _, l := range lines {
		if l.Currency != currency {
			return nil, ErrCrossCurrencyBorrow
		}
		if l.Amount <= 0 {
			return nil, fmt.Errorf("obligation: line for pos %s has non-positive amount %d",
				l.PosID, l.Amount)
		}
	}

	var (
		outs    []Line
		ins     []Line
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
			return nil, fmt.Errorf("obligation: unknown direction %q", l.Direction)
		}
	}
	if len(outs) == 0 || len(ins) == 0 {
		return nil, errors.New("obligation: borrow needs at least one out and one in line")
	}

	var totalIn int64
	for _, l := range ins {
		totalIn += l.Amount
	}
	if totalIn != totalOut {
		// Spec §10.6: per-currency line totals must reconcile.
		return nil, fmt.Errorf("obligation: out total %d != in total %d", totalOut, totalIn)
	}

	// Generate one obligation per (out, in) pair. The amount owed by each
	// creditor to each debtor is:
	//
	//   creditor_share = creditor_out / total_out
	//   debtor_share   = debtor_in   / total_in   (== total_out)
	//   owed           = creditor_share * debtor_in   == creditor_out * debtor_in / total_out
	//
	// Integer arithmetic with banker-style rounding to keep the row sums
	// equal to the line totals exactly.
	out := make([]Obligation, 0, len(outs)*len(ins))
	for di, in := range ins {
		// Distribute `in.Amount` across creditors, last creditor gets the
		// remainder so rows sum exactly to in.Amount (no fractional cents
		// drift).
		var allocated int64
		for ci, cred := range outs {
			var amount int64
			if ci == len(outs)-1 {
				amount = in.Amount - allocated
			} else {
				amount = (cred.Amount * in.Amount) / totalOut
				allocated += amount
			}
			out = append(out, Obligation{
				ID:            idGen(),
				TransactionID: txnID,
				CreditorPosID: cred.PosID,
				DebtorPosID:   in.PosID,
				Currency:      currency,
				Owed:          amount,
				Repaid:        0,
				ClearedAt:     nil,
			})
			_ = di
		}
	}
	return out, nil
}

// RepaymentLine is a line from an inter_pos borrow transaction in the
// REVERSE direction — the original debtor is now sending out, the original
// creditor is now receiving in. The matcher pairs payments to open
// obligations FIFO by (creditor, debtor) and produces the resulting
// updates and any new obligations spawned by overpayment.
type RepaymentLine struct {
	FromPos  string // the pos paying down (was the debtor)
	ToPos    string // the pos being repaid (was the creditor)
	Currency string
	Amount   int64 // positive
}

// MatchPlan is the calculated set of mutations to apply atomically.
type MatchPlan struct {
	// Updates: existing obligations whose Repaid (and possibly ClearedAt)
	// changed.
	Updates []Obligation
	// Newly created obligations in the REVERSE direction, when a payment
	// exceeded the sum of open obligations from this debtor to this
	// creditor (the "kid's school cash short after gold price drop"
	// case from spec §4.3).
	NewObligations []Obligation
}

// Match applies repayments to open obligations using FIFO order
// (ascending by an Order field exposed by callers — usually CreatedAt).
// Open obligations are those with !IsCleared().
//
// `now` is the time at which a freshly-cleared obligation's ClearedAt
// gets stamped. `idGen` is used only for new reverse-direction
// obligations spawned by overpayment.
func Match(open []Obligation, payments []RepaymentLine, now time.Time, idGen func() string) (MatchPlan, error) {
	for _, o := range open {
		if err := o.Validate(); err != nil {
			return MatchPlan{}, err
		}
	}
	plan := MatchPlan{}

	// Obligations are matched by (CreditorPosID, DebtorPosID, Currency).
	// In the repayment, FromPos = original debtor, ToPos = original creditor.
	// So the lookup key for a RepaymentLine{FromPos: D, ToPos: C} is
	// (Creditor=C, Debtor=D).
	type key struct{ Creditor, Debtor, Currency string }

	// Bucket open obligations by (creditor, debtor, currency) and sort each
	// bucket FIFO. We rely on the input order being FIFO already (caller
	// must pass `open` in oldest-first order); we don't sort by ID alone
	// because IDs may be uuids. Stable order preserves caller intent.
	buckets := map[key][]int{}
	for i, o := range open {
		k := key{Creditor: o.CreditorPosID, Debtor: o.DebtorPosID, Currency: o.Currency}
		buckets[k] = append(buckets[k], i)
	}
	// Track per-obligation pending update so a single repayment can clear
	// across multiple obligations.
	updated := map[int]Obligation{}
	getCurrent := func(i int) Obligation {
		if o, ok := updated[i]; ok {
			return o
		}
		return open[i]
	}

	for _, p := range payments {
		if p.Amount <= 0 {
			return MatchPlan{}, fmt.Errorf("repayment to %s: non-positive amount %d", p.ToPos, p.Amount)
		}
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
			// Overpayment: create a new obligation in the reverse direction
			// (the original creditor now owes the original debtor).
			plan.NewObligations = append(plan.NewObligations, Obligation{
				ID:            idGen(),
				TransactionID: "", // caller stamps this with the repayment txn id
				CreditorPosID: p.FromPos, // formerly debtor
				DebtorPosID:   p.ToPos,   // formerly creditor
				Currency:      p.Currency,
				Owed:          remaining,
				Repaid:        0,
				ClearedAt:     nil,
			})
		}
	}

	// Stable update list ordered by original index so callers see deterministic output.
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
