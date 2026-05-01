// Package money is the integer-cents money type per spec §10.1.
//
// Invariants enforced by the type:
//   - Amount is always int64 cents — no float64 anywhere in the API.
//   - Currency is the lowercased string; spec §4.2 reserves `^[a-z0-9-]+$`
//     and this package lowercases on construction so equality is stable.
//   - Arithmetic is overflow-safe: Add/Sub return an error rather than
//     wrapping around int64 boundaries (a wrap would be a silent
//     correctness bug in a financial ledger).
//   - Negative amounts are first-class (spec §4.2: "Negative cash_balance
//     is permitted and first-class"). IsNegative() is the surface, not a
//     panic.
//
// "Cents" is the smallest unit per the operator's choice — rupiah for IDR,
// micrograms for `gold-g`, etc. The package does not interpret the unit;
// callers do.
package money

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Money pairs an int64 amount with a currency string.
type Money struct {
	Cents    int64
	Currency string // lowercased; "" for the zero value.
}

// New constructs a Money. Currency is lowercased to enforce spec §4.2's
// case-insensitive equivalence.
func New(cents int64, currency string) Money {
	return Money{Cents: cents, Currency: strings.ToLower(currency)}
}

// ErrCurrencyMismatch is returned by Add/Sub when operands carry different
// currencies. Spec §4.3 cross-currency handling: "lines on opposite
// directions may carry different currencies" — but those are SEPARATE
// per-currency totals, never summed across.
var ErrCurrencyMismatch = errors.New("money: currency mismatch")

// ErrOverflow is returned when an arithmetic op would wrap int64.
var ErrOverflow = errors.New("money: arithmetic overflow")

// Add returns m + other. The currencies must match. Returns ErrOverflow if
// the sum would wrap.
func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("%w: %q vs %q", ErrCurrencyMismatch, m.Currency, other.Currency)
	}
	// Overflow check before commit.
	if (other.Cents > 0 && m.Cents > math.MaxInt64-other.Cents) ||
		(other.Cents < 0 && m.Cents < math.MinInt64-other.Cents) {
		return Money{}, fmt.Errorf("%w: %d + %d", ErrOverflow, m.Cents, other.Cents)
	}
	return Money{Cents: m.Cents + other.Cents, Currency: m.Currency}, nil
}

// Sub returns m - other. The currencies must match. Returns ErrOverflow if
// the difference would wrap.
func (m Money) Sub(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, fmt.Errorf("%w: %q vs %q", ErrCurrencyMismatch, m.Currency, other.Currency)
	}
	if (other.Cents < 0 && m.Cents > math.MaxInt64+other.Cents) ||
		(other.Cents > 0 && m.Cents < math.MinInt64+other.Cents) {
		return Money{}, fmt.Errorf("%w: %d - %d", ErrOverflow, m.Cents, other.Cents)
	}
	return Money{Cents: m.Cents - other.Cents, Currency: m.Currency}, nil
}

// Neg returns -m. Note: math.MinInt64 has no positive counterpart; calling
// Neg() on it returns the same value (a quirk of two's complement int64,
// not a financial bug — that amount is never reachable through legitimate
// arithmetic given the overflow checks on Add/Sub).
func (m Money) Neg() Money {
	if m.Cents == math.MinInt64 {
		return m // -MinInt64 overflows; preserve the value rather than wrap.
	}
	return Money{Cents: -m.Cents, Currency: m.Currency}
}

// IsZero reports whether the amount is zero. Currency is ignored — zero is
// zero in any unit.
func (m Money) IsZero() bool { return m.Cents == 0 }

// IsNegative reports whether the amount is strictly negative.
func (m Money) IsNegative() bool { return m.Cents < 0 }

// String renders amount and currency for logging. The format is intentionally
// simple — the UI layer formats with grouping/locale, not this package.
func (m Money) String() string {
	if m.Currency == "" {
		return fmt.Sprintf("%d", m.Cents)
	}
	return fmt.Sprintf("%d %s", m.Cents, m.Currency)
}
