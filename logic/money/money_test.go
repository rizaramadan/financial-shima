package money

import (
	"math"
	"testing"
	"testing/quick"
)

// --- Construction & shape ---

func TestZero_HasZeroAmountAndEmptyCurrency(t *testing.T) {
	t.Parallel()
	var m Money
	if m.Cents != 0 {
		t.Errorf("zero Money cents = %d, want 0", m.Cents)
	}
	if m.Currency != "" {
		t.Errorf("zero Money currency = %q, want empty", m.Currency)
	}
}

func TestNew_BindsAmountAndCurrency(t *testing.T) {
	t.Parallel()
	m := New(123_456, "IDR")
	if m.Cents != 123_456 {
		t.Errorf("Cents = %d, want 123456", m.Cents)
	}
	// New lowercases per spec §4.2's `^[a-z0-9-]+$` invariant.
	if m.Currency != "idr" {
		t.Errorf("Currency = %q, want idr (lowercased)", m.Currency)
	}
}

func TestNew_LowercasesCurrency(t *testing.T) {
	t.Parallel()
	m := New(0, "Gold-G")
	if m.Currency != "gold-g" {
		t.Errorf("Currency = %q, want gold-g (spec §4.2: ^[a-z0-9-]+$)", m.Currency)
	}
}

// --- Arithmetic ---

func TestAdd_SameCurrency_Sums(t *testing.T) {
	t.Parallel()
	a, b := New(100, "IDR"), New(50, "IDR")
	got, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.Cents != 150 || got.Currency != "idr" {
		t.Errorf("got %+v, want {150 idr}", got)
	}
}

func TestAdd_DifferentCurrency_Errors(t *testing.T) {
	t.Parallel()
	a, b := New(100, "IDR"), New(1, "gold-g")
	if _, err := a.Add(b); err == nil {
		t.Error("Add with different currencies did not error")
	}
}

func TestSub_SameCurrency_Subtracts(t *testing.T) {
	t.Parallel()
	a, b := New(100, "IDR"), New(30, "IDR")
	got, err := a.Sub(b)
	if err != nil {
		t.Fatalf("Sub: %v", err)
	}
	if got.Cents != 70 {
		t.Errorf("got %d, want 70", got.Cents)
	}
}

func TestSub_AllowsNegative(t *testing.T) {
	t.Parallel()
	a, b := New(50, "IDR"), New(100, "IDR")
	got, _ := a.Sub(b)
	if got.Cents != -50 {
		t.Errorf("got %d, want -50 (spec §4.2: negative cash_balance is first-class)", got.Cents)
	}
}

func TestNeg_FlipsSign(t *testing.T) {
	t.Parallel()
	if got := New(100, "IDR").Neg().Cents; got != -100 {
		t.Errorf("got %d, want -100", got)
	}
	if got := New(-100, "IDR").Neg().Cents; got != 100 {
		t.Errorf("got %d, want 100", got)
	}
}

func TestAdd_OverflowReturnsError(t *testing.T) {
	t.Parallel()
	a := New(math.MaxInt64, "IDR")
	b := New(1, "IDR")
	if _, err := a.Add(b); err == nil {
		t.Error("Add at MaxInt64 boundary did not error")
	}
}

func TestAdd_UnderflowReturnsError(t *testing.T) {
	t.Parallel()
	a := New(math.MinInt64, "IDR")
	b := New(-1, "IDR")
	if _, err := a.Add(b); err == nil {
		t.Error("Add at MinInt64 boundary did not error")
	}
}

func TestSub_OverflowReturnsError(t *testing.T) {
	t.Parallel()
	a := New(math.MaxInt64, "IDR")
	b := New(-1, "IDR")
	// MaxInt64 - (-1) overflows.
	if _, err := a.Sub(b); err == nil {
		t.Error("Sub overflow did not error")
	}
}

// --- Properties ---

// Identity: a + 0 = a. Holds for any Cents in int64.
func TestProperty_AddZeroIsIdentity(t *testing.T) {
	t.Parallel()
	f := func(cents int64) bool {
		a := Money{Cents: cents, Currency: "idr"}
		zero := Money{Cents: 0, Currency: "idr"}
		got, err := a.Add(zero)
		return err == nil && got == a
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Error(err)
	}
}

// Commutativity: a + b = b + a. Asserted CONDITIONALLY across the full
// int64 range — if one side overflows, the other must too. This exercises
// the overflow branch (Beck R6 review) which int32 inputs could not reach.
func TestProperty_AddIsCommutative(t *testing.T) {
	t.Parallel()
	f := func(x, y int64) bool {
		a := Money{Cents: x, Currency: "idr"}
		b := Money{Cents: y, Currency: "idr"}
		ab, err1 := a.Add(b)
		ba, err2 := b.Add(a)
		// Symmetry of overflow detection itself.
		if (err1 == nil) != (err2 == nil) {
			return false
		}
		if err1 != nil {
			return true // both overflowed; commutativity vacuously holds
		}
		return ab == ba
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Error(err)
	}
}

// Round-trip: (a + b) - b = a. Conditional on the addition NOT overflowing.
// When it does, the property is vacuous; when it doesn't, the round-trip
// must be exact.
func TestProperty_AddSubRoundTrip(t *testing.T) {
	t.Parallel()
	f := func(x, y int64) bool {
		a := Money{Cents: x, Currency: "idr"}
		b := Money{Cents: y, Currency: "idr"}
		sum, err1 := a.Add(b)
		if err1 != nil {
			return true // overflow path — property doesn't apply
		}
		got, err2 := sum.Sub(b)
		if err2 != nil {
			return false // (a+b) succeeded but (a+b)-b overflowed: bug
		}
		return got == a
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Error(err)
	}
}

// TestProperty_OverflowBoundary_Add probes the exact int64 boundaries the
// generators are unlikely to hit by chance. Manual point cases compensate
// for the random sampler's blind spot.
func TestProperty_OverflowBoundary_Add(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b      int64
		wantError bool
	}{
		{math.MaxInt64, 0, false},
		{math.MaxInt64, 1, true},
		{math.MaxInt64 - 1, 1, false},
		{math.MaxInt64, math.MaxInt64, true},
		{math.MinInt64, 0, false},
		{math.MinInt64, -1, true},
		{math.MinInt64 + 1, -1, false},
		{math.MinInt64, math.MinInt64, true},
		{0, 0, false},
		{1, -1, false},
	}
	for _, c := range cases {
		_, err := Money{Cents: c.a, Currency: "x"}.Add(Money{Cents: c.b, Currency: "x"})
		if (err != nil) != c.wantError {
			t.Errorf("Add(%d,%d) err=%v wantErr=%v", c.a, c.b, err, c.wantError)
		}
	}
}

func TestProperty_OverflowBoundary_Sub(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b      int64
		wantError bool
	}{
		{math.MaxInt64, 0, false},
		{math.MaxInt64, -1, true},
		{math.MaxInt64, 1, false},
		{math.MinInt64, 0, false},
		{math.MinInt64, 1, true},
		{math.MinInt64, -1, false},
		{math.MinInt64 + 1, 1, false},
		// Two's-complement asymmetric boundary: 0 - MinInt64 cannot fit
		// in int64 because |MinInt64| > MaxInt64 (Beck review issue 5).
		{0, math.MinInt64, true},
		{1, math.MinInt64, true},
	}
	for _, c := range cases {
		_, err := Money{Cents: c.a, Currency: "x"}.Sub(Money{Cents: c.b, Currency: "x"})
		if (err != nil) != c.wantError {
			t.Errorf("Sub(%d,%d) err=%v wantErr=%v", c.a, c.b, err, c.wantError)
		}
	}
}

// Negation involution: -(-a) == a, except at math.MinInt64 where the
// documented quirk is Neg(MinInt64) == MinInt64 (no positive counterpart).
func TestProperty_NegInvolution(t *testing.T) {
	t.Parallel()
	f := func(cents int64) bool {
		a := Money{Cents: cents, Currency: "idr"}
		if cents == math.MinInt64 {
			// Documented quirk: Neg is a no-op at MinInt64. Assert it
			// rather than skipping (Beck R6 review).
			return a.Neg() == a
		}
		return a.Neg().Neg() == a
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Error(err)
	}
}

// --- Comparison ---

func TestIsZero(t *testing.T) {
	t.Parallel()
	if !New(0, "IDR").IsZero() {
		t.Error("New(0,_).IsZero() = false")
	}
	if New(1, "IDR").IsZero() {
		t.Error("New(1,_).IsZero() = true")
	}
}

func TestIsNegative(t *testing.T) {
	t.Parallel()
	if !New(-1, "IDR").IsNegative() {
		t.Error("-1 not negative")
	}
	if New(0, "IDR").IsNegative() {
		t.Error("0 reported negative")
	}
}

// --- Float64 hygiene ---

// (TestNoFloat64InAPI removed per Beck R6 review — it was a runtime no-op.
// The build itself is the assertion: Money.Cents is int64; any int-incompatible
// assignment elsewhere fails compilation, which the suite already runs.)
