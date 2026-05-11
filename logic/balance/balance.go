// Package balance computes per-pos and per-account cash balances from a
// stream of transactions. The package is pure: no DB, no time.Now, no rand.
//
// Spec references:
//   - §4.2: Pos cash_balance is derived (no stored balance column).
//     Each Pos has a current `account_id`; account balances are derived
//     by joining transactions through this pointer (§5.6 snapshot
//     semantics). Reassigning a Pos to a new Account retroactively
//     re-attributes that Pos's IDR contribution.
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
const IDR = "idr"

// PosKey uniquely identifies a Pos balance bucket — a Pos id paired with
// the currency the running total is in.
type PosKey struct {
	PosID    string
	Currency string
}

// Event is the closed sum of transaction shapes.
type Event interface {
	apply(s *State) error
}

// RegisterPos declares a Pos and binds it to its initial Account.
// Mirrors the schema's `pos.account_id NOT NULL` constraint: every Pos
// must know its funding account before any money flows through it.
type RegisterPos struct {
	PosID     string
	AccountID string
	Currency  string
}

// Reassign changes a Pos's funding Account (spec §5.6 snapshot
// semantics). Past flows attached to this Pos are reattributed to the
// new Account on the next AccountTotal() / Accounts() read — there is
// no per-event mutation of account totals because account totals are
// derived, not stored.
type Reassign struct {
	PosID        string
	NewAccountID string
}

// MoneyIn is money_in / money_out's positive cousin. AccountID is no
// longer a field — the funding account is read from posAccount[PosID]
// at apply time.
type MoneyIn struct {
	PosID       string
	PosCurrency string
	AccountIDR  int64 // positive
	PosAmount   int64 // positive
}

// MoneyOut subtracts; same field set as MoneyIn.
type MoneyOut struct {
	PosID       string
	PosCurrency string
	AccountIDR  int64 // positive; Apply subtracts
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
//
// Account balances are *derived* from posAccount + posIDRFlow
// (§5.6 snapshot view); they are not stored. Read them with Accounts()
// or AccountTotal().
type State struct {
	posAccount  map[string]string // pos_id → current account_id
	posCurrency map[string]string // pos_id → currency (set on RegisterPos)
	posIDRFlow  map[string]int64  // pos_id → cumulative IDR account-side delta
	Pos         map[PosKey]int64  // pos_id × currency → cents
}

// New returns a zero State.
func New() *State {
	return &State{
		posAccount:  map[string]string{},
		posCurrency: map[string]string{},
		posIDRFlow:  map[string]int64{},
		Pos:         map[PosKey]int64{},
	}
}

// Apply mutates s by applying the event. Returns an error if the event
// would violate spec invariants (currency mismatch, amount sign,
// overflow, unknown pos).
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

// Accounts returns the *derived* per-account IDR balances. A Pos's
// cumulative IDR flow is attributed to its current account_id (§5.6).
func (s *State) Accounts() map[string]int64 {
	out := map[string]int64{}
	for posID, acc := range s.posAccount {
		out[acc] += s.posIDRFlow[posID]
	}
	return out
}

// AccountTotal returns Σ(Account balance) under the snapshot view.
func (s *State) AccountTotal() int64 {
	var sum int64
	for _, posID := range posIDsSortedForDeterminism(s.posAccount) {
		sum += s.posIDRFlow[posID]
	}
	return sum
}

func posIDsSortedForDeterminism(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
	ErrCurrencyMismatch  = errors.New("balance: pos currency mismatch with line/event")
	ErrIDRPosMismatch    = errors.New("balance: IDR pos requires account_amount == pos_amount")
	ErrNonPositive       = errors.New("balance: amount must be positive (sign comes from type)")
	ErrOverflow          = errors.New("balance: int64 arithmetic would overflow")
	ErrUnreconciledLines = errors.New("balance: inter_pos per-currency line totals do not reconcile")
	ErrUnregisteredPos   = errors.New("balance: pos has no account_id (RegisterPos required first)")
	ErrPosAlreadyKnown   = errors.New("balance: pos already registered")
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

func (e RegisterPos) apply(s *State) error {
	if e.PosID == "" || e.AccountID == "" || e.Currency == "" {
		return ErrUnregisteredPos
	}
	if _, ok := s.posAccount[e.PosID]; ok {
		return ErrPosAlreadyKnown
	}
	s.posAccount[e.PosID] = e.AccountID
	s.posCurrency[e.PosID] = e.Currency
	return nil
}

func (e Reassign) apply(s *State) error {
	if _, ok := s.posAccount[e.PosID]; !ok {
		return ErrUnregisteredPos
	}
	if e.NewAccountID == "" {
		return ErrUnregisteredPos
	}
	s.posAccount[e.PosID] = e.NewAccountID
	return nil
}

func (e MoneyIn) apply(s *State) error {
	if e.AccountIDR <= 0 || e.PosAmount <= 0 {
		return ErrNonPositive
	}
	if _, ok := s.posAccount[e.PosID]; !ok {
		return ErrUnregisteredPos
	}
	if e.PosCurrency != s.posCurrency[e.PosID] {
		return ErrCurrencyMismatch
	}
	if e.PosCurrency == IDR && e.AccountIDR != e.PosAmount {
		return ErrIDRPosMismatch
	}
	newFlow, err := addSafe(s.posIDRFlow[e.PosID], e.AccountIDR)
	if err != nil {
		return err
	}
	key := PosKey{PosID: e.PosID, Currency: e.PosCurrency}
	newPos, err := addSafe(s.Pos[key], e.PosAmount)
	if err != nil {
		return err
	}
	s.posIDRFlow[e.PosID] = newFlow
	s.Pos[key] = newPos
	return nil
}

func (e MoneyOut) apply(s *State) error {
	if e.AccountIDR <= 0 || e.PosAmount <= 0 {
		return ErrNonPositive
	}
	if _, ok := s.posAccount[e.PosID]; !ok {
		return ErrUnregisteredPos
	}
	if e.PosCurrency != s.posCurrency[e.PosID] {
		return ErrCurrencyMismatch
	}
	if e.PosCurrency == IDR && e.AccountIDR != e.PosAmount {
		return ErrIDRPosMismatch
	}
	newFlow, err := subSafe(s.posIDRFlow[e.PosID], e.AccountIDR)
	if err != nil {
		return err
	}
	key := PosKey{PosID: e.PosID, Currency: e.PosCurrency}
	newPos, err := subSafe(s.Pos[key], e.PosAmount)
	if err != nil {
		return err
	}
	s.posIDRFlow[e.PosID] = newFlow
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
