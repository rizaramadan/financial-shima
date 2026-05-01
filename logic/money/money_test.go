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

// Commutativity: a + b = b + a (whenever neither overflows).
func TestProperty_AddIsCommutative(t *testing.T) {
	t.Parallel()
	f := func(x, y int32) bool { // int32 inputs avoid overflow at the int64 boundary
		a := Money{Cents: int64(x), Currency: "idr"}
		b := Money{Cents: int64(y), Currency: "idr"}
		ab, err1 := a.Add(b)
		ba, err2 := b.Add(a)
		if err1 != nil || err2 != nil {
			return false
		}
		return ab == ba
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Error(err)
	}
}

// Round-trip: (a + b) - b = a (modulo overflow).
func TestProperty_AddSubRoundTrip(t *testing.T) {
	t.Parallel()
	f := func(x, y int32) bool {
		a := Money{Cents: int64(x), Currency: "idr"}
		b := Money{Cents: int64(y), Currency: "idr"}
		sum, err1 := a.Add(b)
		if err1 != nil {
			return false
		}
		got, err2 := sum.Sub(b)
		if err2 != nil {
			return false
		}
		return got == a
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Error(err)
	}
}

// Negation involution: -(-a) == a.
func TestProperty_NegInvolution(t *testing.T) {
	t.Parallel()
	f := func(cents int64) bool {
		// MinInt64 cannot be negated without overflow; skip that one input.
		if cents == math.MinInt64 {
			return true
		}
		a := Money{Cents: cents, Currency: "idr"}
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

// TestNoFloat64InAPI: spec §10.1 says "Money is integer cents. Never
// float64 in Go." The Money struct uses int64; this test pins that
// surface so a future refactor to float64 fails the build.
func TestNoFloat64InAPI(_ *testing.T) {
	var m Money
	// Compile-time assertion: assigning float64 to Cents must fail.
	// (We simply use int64 ops here; the assertion is the type itself.)
	m.Cents = int64(0)
	_ = m
}
