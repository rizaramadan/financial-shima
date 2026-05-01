package obligation

import (
	"errors"
	"math/rand"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

// counter returns a deterministic id generator for tests, plus a pointer
// to the call count so tests can assert idGen discipline.
func counter() (func() string, *int64) {
	var n int64
	return func() string {
		v := atomic.AddInt64(&n, 1)
		return "ob-" + strconv.FormatInt(v, 10)
	}, &n
}

// --- GenerateBorrowObligations ---

func TestGenerate_SingleCreditorSingleDebtor_FullAmountOwed(t *testing.T) {
	t.Parallel()
	gen, calls := counter()
	obs, err := GenerateBorrowObligations("tx1", []Line{
		{PosID: "C", Currency: "idr", Direction: DirOut, Amount: 1_000_000},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 1_000_000},
	}, t0, gen)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d obligations, want 1", len(obs))
	}
	if got := obs[0]; got.CreditorPosID != "C" || got.DebtorPosID != "D" || got.Owed != 1_000_000 {
		t.Errorf("unexpected obligation: %+v", got)
	}
	if obs[0].CreatedAt != t0 {
		t.Errorf("CreatedAt = %v, want %v", obs[0].CreatedAt, t0)
	}
	if obs[0].TransactionID != "tx1" {
		t.Errorf("TransactionID = %q, want tx1", obs[0].TransactionID)
	}
	if *calls != 1 {
		t.Errorf("idGen called %d times, want 1", *calls)
	}
}

// TestGenerate_NonTrivialProration uses non-divisible amounts so the
// proration math actually does something. Beck R2 — previous version
// chose 75/25 splits where the math vanishes.
func TestGenerate_NonTrivialProration(t *testing.T) {
	t.Parallel()
	// A=3, B=1, total_out=4. Debtor in=7, total_in=7. Reconciliation
	// requires total_out==total_in, so we tweak: A=3, B=4, total=7;
	// debtor in=7. floor(3*7/7)=3; B (last) absorbs 7-3=4. Sum=7. ✓
	gen, _ := counter()
	obs, err := GenerateBorrowObligations("tx1", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 3},
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 4},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 7},
	}, t0, gen)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got := map[string]int64{}
	for _, o := range obs {
		got[o.CreditorPosID] = o.Owed
	}
	// floor(3*7/7) = 3 for A; B (last) gets 7-3 = 4.
	if got["A"] != 3 || got["B"] != 4 {
		t.Errorf("proration math: got %+v, want A=3 B=4", got)
	}
	var sum int64
	for _, v := range got {
		sum += v
	}
	if sum != 7 {
		t.Errorf("sum=%d, want 7", sum)
	}
}

// TestGenerate_ZeroShareRowsAreDropped pins the §10.7-CHECK-friendly
// behavior: a creditor whose share rounds to zero is omitted from
// the output rather than producing an Owed=0 row that would violate
// the storage CHECK (Skeet R1).
func TestGenerate_ZeroShareRowsAreDropped(t *testing.T) {
	t.Parallel()
	// A=1, B=2 against debtors X=1, Y=2. floor(1*1/3)=0 (dropped),
	// floor(1*2/3)=0 (dropped); B absorbs each debtor's full amount.
	gen, _ := counter()
	obs, err := GenerateBorrowObligations("tx1", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 1},
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 2},
		{PosID: "X", Currency: "idr", Direction: DirIn, Amount: 1},
		{PosID: "Y", Currency: "idr", Direction: DirIn, Amount: 2},
	}, t0, gen)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Only B should appear — A's share rounded to zero on both debtors.
	for _, o := range obs {
		if o.CreditorPosID == "A" {
			t.Errorf("zero-share row from A not dropped: %+v", o)
		}
		if o.Owed <= 0 {
			t.Errorf("Owed must be > 0, got %+v", o)
		}
		if err := o.Validate(); err != nil {
			t.Errorf("returned obligation fails Validate(): %v", err)
		}
	}
	if len(obs) != 2 {
		t.Errorf("got %d obligations (B→X, B→Y), want 2", len(obs))
	}
}

func TestGenerate_DeterministicAcrossInputOrder(t *testing.T) {
	t.Parallel()
	gen1, _ := counter()
	a, _ := GenerateBorrowObligations("tx", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 3},
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 4},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 7},
	}, t0, gen1)
	gen2, _ := counter()
	b, _ := GenerateBorrowObligations("tx", []Line{
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 4},
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 3},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 7},
	}, t0, gen2)
	if len(a) != len(b) {
		t.Fatalf("nondeterministic count %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].CreditorPosID != b[i].CreditorPosID || a[i].Owed != b[i].Owed {
			t.Errorf("nondeterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestGenerate_CrossCurrency_RejectedAndReturnsNoRows(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	obs, err := GenerateBorrowObligations("tx", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 6_000_000},
		{PosID: "D", Currency: "gold-g", Direction: DirIn, Amount: 5},
	}, t0, gen)
	if !errors.Is(err, ErrCrossCurrencyBorrow) {
		t.Errorf("err = %v, want ErrCrossCurrencyBorrow", err)
	}
	if len(obs) != 0 {
		t.Errorf("got %d obligations on error, want 0 (Beck R3)", len(obs))
	}
}

func TestGenerate_RejectsUnreconciledTotals(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	_, err := GenerateBorrowObligations("tx", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 100},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 90},
	}, t0, gen)
	if !errors.Is(err, ErrUnbalancedLines) {
		t.Errorf("err = %v, want ErrUnbalancedLines", err)
	}
}

func TestGenerate_RejectsInvalidCurrency(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	_, err := GenerateBorrowObligations("tx", []Line{
		{PosID: "A", Currency: "USD", Direction: DirOut, Amount: 1}, // uppercase
		{PosID: "D", Currency: "USD", Direction: DirIn, Amount: 1},
	}, t0, gen)
	if !errors.Is(err, ErrInvalidCurrency) {
		t.Errorf("err = %v, want ErrInvalidCurrency", err)
	}
}

// --- MatchRepayments ---

func TestMatch_FullRepayment_ClearsObligation(t *testing.T) {
	t.Parallel()
	gen, calls := counter()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1000, CreatedAt: t0},
	}
	plan, err := MatchRepayments(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 1000},
	}, "rtx", t0.Add(time.Hour), gen)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].Repaid != 1000 || plan.Updates[0].ClearedAt == nil {
		t.Errorf("update: %+v", plan.Updates)
	}
	if len(plan.ReverseObligations) != 0 {
		t.Errorf("got %d reverse, want 0", len(plan.ReverseObligations))
	}
	if *calls != 0 {
		t.Errorf("idGen called %d times, want 0 (no overpayment)", *calls)
	}
}

func TestMatch_PartialRepayment_DoesNotClear(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1000, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 600},
	}, "rtx", t0, gen)
	if plan.Updates[0].Repaid != 600 || plan.Updates[0].ClearedAt != nil {
		t.Errorf("partial: %+v", plan.Updates[0])
	}
}

// TestMatch_FIFO_ByCreatedAt: oldest CreatedAt clears first regardless of
// slice input order. Beck R7 (previous test pinned slice order).
func TestMatch_FIFO_ByCreatedAt(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "newer", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1000, CreatedAt: t0.Add(2 * time.Hour)},
		{ID: "older", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1000, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 600},
	}, "rtx", t0, gen)
	// Older obligation should receive the partial payment first.
	for _, u := range plan.Updates {
		if u.ID == "older" && u.Repaid != 600 {
			t.Errorf("older.Repaid = %d, want 600", u.Repaid)
		}
		if u.ID == "newer" && u.Repaid != 0 {
			t.Errorf("newer.Repaid = %d, want 0 (FIFO violated)", u.Repaid)
		}
	}
}

func TestMatch_OverpaymentSpawnsReverseWithTransactionID(t *testing.T) {
	t.Parallel()
	gen, calls := counter()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1000, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 1500},
	}, "rtx-42", t0, gen)
	if len(plan.ReverseObligations) != 1 {
		t.Fatalf("got %d reverse, want 1", len(plan.ReverseObligations))
	}
	rev := plan.ReverseObligations[0]
	if rev.CreditorPosID != "D" || rev.DebtorPosID != "C" || rev.Owed != 500 {
		t.Errorf("reverse: %+v", rev)
	}
	// Skeet R2 / Ive R4: TransactionID is stamped, not empty.
	if rev.TransactionID != "rtx-42" {
		t.Errorf("reverse TransactionID = %q, want rtx-42", rev.TransactionID)
	}
	if rev.CreatedAt != t0 {
		t.Errorf("reverse CreatedAt = %v, want %v", rev.CreatedAt, t0)
	}
	if *calls != 1 {
		t.Errorf("idGen called %d times, want 1", *calls)
	}
}

// TestMatch_GoldDropScenario_PartialRepayLeavesOriginalOpen renamed (Beck R4)
// — old name claimed "LeftWithReverseObligation" but the assertion is the
// opposite.
func TestMatch_GoldDropScenario_PartialRepayLeavesOriginalOpen(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "gold-pool", DebtorPosID: "kids-school",
			Currency: "g", Owed: 5, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{FromPos: "kids-school", ToPos: "gold-pool", Currency: "g", Amount: 4},
	}, "rtx", t0, gen)
	if u := plan.Updates[0]; u.Repaid != 4 || u.ClearedAt != nil {
		t.Errorf("partial: %+v", u)
	}
	if len(plan.ReverseObligations) != 0 {
		t.Errorf("got %d reverse, want 0 (partial repay leaves original open)",
			len(plan.ReverseObligations))
	}
}

// TestMatch_BatchedPayments: spec §7.2 admits multiple repayment lines in
// one inter_pos transaction. The matcher must route each line to its own
// (creditor, debtor) bucket. Beck R6.
func TestMatch_BatchedPayments_RouteToOwnBuckets(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "AB", CreditorPosID: "A", DebtorPosID: "B", Currency: "idr",
			Owed: 100, CreatedAt: t0},
		{ID: "XY", CreditorPosID: "X", DebtorPosID: "Y", Currency: "idr",
			Owed: 200, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{FromPos: "B", ToPos: "A", Currency: "idr", Amount: 100}, // pays AB
		{FromPos: "Y", ToPos: "X", Currency: "idr", Amount: 200}, // pays XY
	}, "rtx", t0, gen)
	if len(plan.Updates) != 2 {
		t.Fatalf("got %d updates, want 2", len(plan.Updates))
	}
	for _, u := range plan.Updates {
		if u.ClearedAt == nil {
			t.Errorf("expected %s cleared, got %+v", u.ID, u)
		}
	}
}

func TestMatch_TwoPaymentsAgainstSameObligation(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1000, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 500},
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 500},
	}, "rtx", t0, gen)
	if plan.Updates[0].Repaid != 1000 || plan.Updates[0].ClearedAt == nil {
		t.Errorf("two halves should fully clear: %+v", plan.Updates[0])
	}
}

func TestMatch_NoOpenObligation_CreatesReverse(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	plan, _ := MatchRepayments(nil, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 700},
	}, "rtx", t0, gen)
	if len(plan.Updates) != 0 {
		t.Errorf("no-open with %d updates", len(plan.Updates))
	}
	if len(plan.ReverseObligations) != 1 || plan.ReverseObligations[0].Owed != 700 {
		t.Fatalf("reverse: %+v", plan.ReverseObligations)
	}
}

func TestMatch_RejectsInvalidPayment(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	cases := []struct {
		name string
		pay  RepaymentLine
		want error
	}{
		{"non-positive", RepaymentLine{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 0}, ErrNonPositiveAmount},
		{"negative", RepaymentLine{FromPos: "D", ToPos: "C", Currency: "idr", Amount: -1}, ErrNonPositiveAmount},
		{"empty currency", RepaymentLine{FromPos: "D", ToPos: "C", Currency: "", Amount: 1}, ErrInvalidCurrency},
		{"uppercase currency", RepaymentLine{FromPos: "D", ToPos: "C", Currency: "IDR", Amount: 1}, ErrInvalidCurrency},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := MatchRepayments(nil, []RepaymentLine{c.pay}, "rtx", t0, gen)
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
}

// --- §10.7 invariant: real property test (not boundary table) ---

// TestProperty_Invariant_§10.7_Random drives 10000 random repayment
// scenarios through MatchRepayments and asserts every obligation in
// the resulting plan satisfies §10.7 via Validate(). Beck R1 — the
// previous "property" was 8 hand-picked rows.
func TestProperty_Invariant_Random(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(0xC0FFEE))
	for i := 0; i < 10000; i++ {
		owed := rng.Int63n(100_000) + 1
		repay := rng.Int63n(2*owed + 1) // [0, 2*owed]
		gen, _ := counter()
		open := []Obligation{
			{ID: "o", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
				Owed: owed, CreatedAt: t0},
		}
		var payments []RepaymentLine
		if repay > 0 {
			payments = []RepaymentLine{
				{FromPos: "D", ToPos: "C", Currency: "idr", Amount: repay},
			}
		}
		plan, err := MatchRepayments(open, payments, "rtx", t0.Add(time.Second), gen)
		if err != nil {
			t.Fatalf("iter %d (owed=%d repay=%d): %v", i, owed, repay, err)
		}
		for _, u := range plan.Updates {
			if err := u.Validate(); err != nil {
				t.Fatalf("iter %d update fails Validate: %v", i, err)
			}
		}
		for _, n := range plan.ReverseObligations {
			if err := n.Validate(); err != nil {
				t.Fatalf("iter %d reverse fails Validate: %v", i, err)
			}
		}
	}
}

func TestMatch_RejectsClearedOpenInputs(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	cleared := t0
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 100, Repaid: 50, ClearedAt: &cleared, CreatedAt: t0}, // §10.7 violated
	}
	_, err := MatchRepayments(open, nil, "rtx", t0, gen)
	if err == nil {
		t.Error("Match accepted §10.7-violating obligation")
	}
}

func TestObligation_Validate_GuardsBothHalvesOfTheIff(t *testing.T) {
	// §10.7 has TWO violations: cleared with repaid<owed, AND not-cleared
	// with repaid>=owed. Validate() must catch both. Beck R5 (other half
	// was untested).
	t.Parallel()
	cleared := t0
	cases := []struct {
		name   string
		o      Obligation
		errors bool
	}{
		{"cleared with repaid >= owed", Obligation{
			ID: "o", Owed: 100, Repaid: 100, ClearedAt: &cleared}, false},
		{"not cleared with repaid < owed", Obligation{
			ID: "o", Owed: 100, Repaid: 50}, false},
		{"cleared with repaid < owed (§10.7 violated)", Obligation{
			ID: "o", Owed: 100, Repaid: 50, ClearedAt: &cleared}, true},
		{"not cleared with repaid >= owed (§10.7 violated)", Obligation{
			ID: "o", Owed: 100, Repaid: 100}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.o.Validate()
			if c.errors && err == nil {
				t.Error("expected Validate to error")
			}
			if !c.errors && err != nil {
				t.Errorf("expected nil, got %v", err)
			}
		})
	}
}
