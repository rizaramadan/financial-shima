// Package balance computes per-account and per-pos cash balances from a
// stream of transactions. The package is pure: no DB, no time.Now, no rand.
//
// Spec references:
//   - §4.2: Pos cash_balance is derived (no stored balance column).
//   - §5.1 IDR-Pos rule: money_in / money_out to an IDR pos requires
//     account_amount == pos_amount.
//   - §10.5: per-currency reconciliation — for IDR, Σ(Pos.cash) = Σ(Account).
//   - §10.6: inter_pos line totals per-currency reconcile.
//
// Receivables/payables (which depend on borrow obligations) are not yet
// computed here; they ship in Phase 8 alongside pos_obligation matching.
package balance

import (
	"errors"
	"fmt"
	"math"
)

// IDR is the literal currency string used for the operator's primary unit.
// Centralised so a future rename ripples cleanly.
const IDR = "idr"

// PosKey uniquely identifies a Pos balance bucket — a Pos id paired with
// the currency the running total is in. Lifting Currency into the key keeps
// per-currency aggregation natural.
type PosKey struct {
	PosID    string
	Currency string
}

// Event is the closed sum of transaction shapes. Pointer methods on each
// concrete type embed eventTag so the interface is unforgeable outside
// the package.
type Event interface {
	apply(s *State) error
}

type MoneyIn struct {
	AccountID    string
	AccountIDR   int64 // positive
	PosID        string
	PosCurrency  string
	PosAmount    int64 // positive
}

type MoneyOut struct {
	AccountID   string
	AccountIDR  int64 // positive; Apply subtracts
	PosID       string
	PosCurrency string
	PosAmount   int64 // positive; Apply subtracts
}

// InterPosLine is one line of an inter_pos event.
type InterPosLine struct {
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

type InterPos struct {
	Mode  string
	Lines []InterPosLine
}

// State accumulates running totals as events are applied.
type State struct {
	Accounts map[string]int64 // account_id → IDR cents
	Pos      map[PosKey]int64 // pos_id × currency → cents
}

// New returns a zero State.
func New() *State {
	return &State{
		Accounts: map[string]int64{},
		Pos:      map[PosKey]int64{},
	}
}

// Apply mutates s by applying the event. Returns an error if the event
// would violate spec invariants (currency mismatch, amount sign, overflow).
func (s *State) Apply(e Event) error { return e.apply(s) }

// ApplyAll applies events in order, returning at the first failure.
func (s *State) ApplyAll(events []Event) error {
	for i, e := range events {
		if err := s.Apply(e); err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}
	}
	return nil
}

// AccountTotal returns Σ(Account balance). All accounts are IDR per §4.1.
func (s *State) AccountTotal() int64 {
	var sum int64
	for _, v := range s.Accounts {
		sum += v
	}
	return sum
}

// PosCashTotal returns Σ(Pos.cash) for the given currency.
func (s *State) PosCashTotal(currency string) int64 {
	var sum int64
	for k, v := range s.Pos {
		if k.Currency == currency {
			sum += v
		}
	}
	return sum
}

// --- Event implementations ---

var (
	ErrCurrencyMismatch = errors.New("balance: pos currency mismatch with line/event")
	ErrIDRPosMismatch   = errors.New("balance: IDR pos requires account_amount == pos_amount")
	ErrNonPositive      = errors.New("balance: amount must be positive (sign comes from type)")
	ErrOverflow         = errors.New("balance: int64 arithmetic would overflow")
	ErrUnreconciledLines = errors.New("balance: inter_pos per-currency line totals do not reconcile")
)

func addSafe(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

func subSafe(a, b int64) (int64, error) {
	if (b < 0 && a > math.MaxInt64+b) || (b > 0 && a < math.MinInt64+b) {
		return 0, ErrOverflow
	}
	return a - b, nil
}

func (e MoneyIn) apply(s *State) error {
	if e.AccountIDR <= 0 || e.PosAmount <= 0 {
		return ErrNonPositive
	}
	if e.PosCurrency == IDR && e.AccountIDR != e.PosAmount {
		return ErrIDRPosMismatch
	}
	newAcc, err := addSafe(s.Accounts[e.AccountID], e.AccountIDR)
	if err != nil {
		return err
	}
	key := PosKey{PosID: e.PosID, Currency: e.PosCurrency}
	newPos, err := addSafe(s.Pos[key], e.PosAmount)
	if err != nil {
		return err
	}
	s.Accounts[e.AccountID] = newAcc
	s.Pos[key] = newPos
	return nil
}

func (e MoneyOut) apply(s *State) error {
	if e.AccountIDR <= 0 || e.PosAmount <= 0 {
		return ErrNonPositive
	}
	if e.PosCurrency == IDR && e.AccountIDR != e.PosAmount {
		return ErrIDRPosMismatch
	}
	newAcc, err := subSafe(s.Accounts[e.AccountID], e.AccountIDR)
	if err != nil {
		return err
	}
	key := PosKey{PosID: e.PosID, Currency: e.PosCurrency}
	newPos, err := subSafe(s.Pos[key], e.PosAmount)
	if err != nil {
		return err
	}
	s.Accounts[e.AccountID] = newAcc
	s.Pos[key] = newPos
	return nil
}

// apply for InterPos enforces §10.6: per-currency line totals must
// reconcile within the event itself, then mutates pos balances by adding
// `in` lines and subtracting `out` lines.
func (e InterPos) apply(s *State) error {
	outByCcy := map[string]int64{}
	inByCcy := map[string]int64{}
	for _, l := range e.Lines {
		if l.Amount <= 0 {
			return ErrNonPositive
		}
		if l.Currency == "" {
			return ErrCurrencyMismatch
		}
		switch l.Direction {
		case DirOut:
			n, err := addSafe(outByCcy[l.Currency], l.Amount)
			if err != nil {
				return err
			}
			outByCcy[l.Currency] = n
		case DirIn:
			n, err := addSafe(inByCcy[l.Currency], l.Amount)
			if err != nil {
				return err
			}
			inByCcy[l.Currency] = n
		default:
			return fmt.Errorf("balance: unknown direction %q", l.Direction)
		}
	}
	// §10.6 self-reconciliation per currency.
	for c, out := range outByCcy {
		if inByCcy[c] != out {
			return fmt.Errorf("%w: currency %q out=%d in=%d",
				ErrUnreconciledLines, c, out, inByCcy[c])
		}
	}
	for c, in := range inByCcy {
		if _, ok := outByCcy[c]; !ok {
			return fmt.Errorf("%w: currency %q in=%d with no matching out",
				ErrUnreconciledLines, c, in)
		}
	}

	// All lines reconcile; commit to pos balances.
	for _, l := range e.Lines {
		key := PosKey{PosID: l.PosID, Currency: l.Currency}
		var (
			next int64
			err  error
		)
		if l.Direction == DirOut {
			next, err = subSafe(s.Pos[key], l.Amount)
		} else {
			next, err = addSafe(s.Pos[key], l.Amount)
		}
		if err != nil {
			return err
		}
		s.Pos[key] = next
	}
	return nil
}
