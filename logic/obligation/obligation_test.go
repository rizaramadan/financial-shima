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
	if len(obs) != 1 || obs[0].Owed != 1_000_000 || obs[0].TransactionID != "tx1" {
		t.Errorf("unexpected: %+v", obs)
	}
	if *calls != 1 {
		t.Errorf("idGen called %d times, want 1", *calls)
	}
}

// TestGenerate_RoundingExercisedByMultipleDebtors uses creditors A=3, B=4
// against debtors X=3, Y=4 (Hamilton's largest-remainder method).
//
// For X (in=3): floor(3*3/7)=1 rem 2; floor(4*3/7)=1 rem 5. Residual 1
//   goes to B (largest remainder). A→X=1, B→X=2. Sum=3. ✓
// For Y (in=4): floor(3*4/7)=1 rem 5; floor(4*4/7)=2 rem 2. Residual 1
//   goes to A (largest remainder). A→Y=2, B→Y=2. Sum=4. ✓
//
// Beck R1 — math actually does something; expectations follow from
// Hamilton's apportionment, not the floor-only "trivial copy" path.
func TestGenerate_RoundingExercisedByMultipleDebtors(t *testing.T) {
	t.Parallel()
	gen, calls := counter()
	obs, err := GenerateBorrowObligations("tx", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 3},
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 4},
		{PosID: "X", Currency: "idr", Direction: DirIn, Amount: 3},
		{PosID: "Y", Currency: "idr", Direction: DirIn, Amount: 4},
	}, t0, gen)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got := map[string]int64{}
	for _, o := range obs {
		got[o.CreditorPosID+"→"+o.DebtorPosID] = o.Owed
	}
	want := map[string]int64{"A→X": 1, "B→X": 2, "A→Y": 2, "B→Y": 2}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	if got["A→X"]+got["B→X"] != 3 {
		t.Errorf("X sum=%d, want 3", got["A→X"]+got["B→X"])
	}
	if got["A→Y"]+got["B→Y"] != 4 {
		t.Errorf("Y sum=%d, want 4", got["A→Y"]+got["B→Y"])
	}
	if int64(len(obs)) != *calls {
		t.Errorf("idGen calls=%d, len(obs)=%d (no wasted IDs)", *calls, len(obs))
	}
}

// TestGenerate_ZeroShareRowsAreDropped: a creditor whose floor share is 0
// AND who doesn't win the residual is dropped from output. With Hamilton,
// this happens when their fractional remainder is smaller than another
// creditor's.
//
// A=1, B=2, X=1: floor(1*1/3)=0 rem 1; floor(2*1/3)=0 rem 2. Residual 1
//   goes to B (larger rem). A→X dropped; B→X = 1.
// A=1, B=2, Y=2: floor(1*2/3)=0 rem 2; floor(2*2/3)=1 rem 1. Residual 1
//   goes to A (larger rem). A→Y = 1; B→Y = 1.
//
// So A is dropped on X but not Y; the suite verifies (CreditorPosID="A",
// DebtorPosID="X") row is absent and every emitted row passes Validate().
func TestGenerate_ZeroShareRowsAreDropped(t *testing.T) {
	t.Parallel()
	gen, calls := counter()
	obs, err := GenerateBorrowObligations("tx", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 1},
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 2},
		{PosID: "X", Currency: "idr", Direction: DirIn, Amount: 1},
		{PosID: "Y", Currency: "idr", Direction: DirIn, Amount: 2},
	}, t0, gen)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, o := range obs {
		if o.CreditorPosID == "A" && o.DebtorPosID == "X" {
			t.Errorf("A→X (zero share, no residual) not dropped: %+v", o)
		}
		if err := o.Validate(); err != nil {
			t.Errorf("returned obligation fails Validate(): %v", err)
		}
	}
	// 3 expected: B→X, A→Y, B→Y. (A→X dropped.)
	if len(obs) != 3 {
		t.Errorf("got %d obligations, want 3 (A→X dropped)", len(obs))
	}
	if int64(len(obs)) != *calls {
		t.Errorf("idGen calls=%d, len(obs)=%d (zero-share rows must not consume IDs)",
			*calls, len(obs))
	}
}

// TestGenerate_SelfCreditorIsSkipped_SumStillEqualsDebtor: a Pos that
// appears in BOTH `outs` and `ins` is silently skipped on the self-debt
// row (a pos cannot owe itself), and the remaining creditors must
// absorb the FULL debtor amount — totalOut excludes the self-creditor's
// Amount for that debtor's row. Skeet R3 caught a residual-drop bug
// where without the effTotalOut adjustment, sum < debtor.Amount.
func TestGenerate_SelfCreditorIsSkipped_SumStillEqualsDebtor(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	// outs: Z=50, A=50 (totalOut=100). ins: Z=100. Z is self-debtor on
	// the only debtor row. effTotalOut = 50; A's share = 50*100/50 = 100.
	// The full 100 must land on A — not 50 with 50 silently dropped.
	obs, err := GenerateBorrowObligations("tx", []Line{
		{PosID: "Z", Currency: "idr", Direction: DirOut, Amount: 50},
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 50},
		{PosID: "Z", Currency: "idr", Direction: DirIn, Amount: 100},
	}, t0, gen)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d obligations, want 1 (Z→Z dropped, A→Z kept)", len(obs))
	}
	if obs[0].CreditorPosID != "A" || obs[0].DebtorPosID != "Z" || obs[0].Owed != 100 {
		t.Errorf("got %+v, want A→Z=100", obs[0])
	}
}

// TestProperty_GenerateBorrow_PerDebtorSumEqualsIn property-tests the
// invariant Σ(shares for debtor d) == d.Amount across random inputs
// including self-overlaps. Skeet R3 paired with the test that catches
// the bug: without the effTotalOut adjustment, this fails.
func TestProperty_GenerateBorrow_PerDebtorSumEqualsIn(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(0xBEEF))
	for i := 0; i < 1000; i++ {
		// 2-4 creditors and 1-3 debtors with random amounts. Some inputs
		// share PosIDs to exercise the self-debt path.
		nOuts := rng.Intn(3) + 2
		nIns := rng.Intn(3) + 1
		idsPool := []string{"P0", "P1", "P2", "P3", "P4"}

		outs := make([]Line, 0, nOuts)
		ins := make([]Line, 0, nIns)
		var totalOut int64
		for j := 0; j < nOuts; j++ {
			amt := int64(rng.Intn(100) + 1)
			outs = append(outs, Line{
				PosID: idsPool[rng.Intn(len(idsPool))],
				Currency: "idr", Direction: DirOut, Amount: amt,
			})
			totalOut += amt
		}
		// distribute totalOut across nIns debtors with rounding to last
		var allocated int64
		for j := 0; j < nIns; j++ {
			var amt int64
			if j == nIns-1 {
				amt = totalOut - allocated
			} else {
				amt = totalOut / int64(nIns)
				allocated += amt
			}
			if amt <= 0 {
				amt = 1
				totalOut += 1 // tiny adjustment to keep balanced
			}
			ins = append(ins, Line{
				PosID: idsPool[rng.Intn(len(idsPool))],
				Currency: "idr", Direction: DirIn, Amount: amt,
			})
		}
		// Re-balance totalOut against ins
		var totalIn int64
		for _, in := range ins {
			totalIn += in.Amount
		}
		if totalIn != totalOut {
			outs = append(outs, Line{
				PosID: idsPool[rng.Intn(len(idsPool))],
				Currency: "idr", Direction: DirOut, Amount: totalIn - totalOut,
			})
		}

		gen, _ := counter()
		obs, err := GenerateBorrowObligations("tx", append(outs, ins...), t0, gen)
		if err != nil {
			continue // skip degenerate cases; not the property under test
		}

		// Per-debtor-PosID: sum of obligations equals sum of in.Amount for
		// that PosID, EXCEPT when only the self-creditor has out-amount
		// for that debtor (then no obligations are emitted, by design).
		debtorTotalIn := map[string]int64{}
		for _, in := range ins {
			debtorTotalIn[in.PosID] += in.Amount
		}
		obsByDebtor := map[string]int64{}
		for _, o := range obs {
			obsByDebtor[o.DebtorPosID] += o.Owed
		}
		// Build effTotalOut per debtor (sum of out.Amount where PosID != debtor).
		for dp, want := range debtorTotalIn {
			eff := int64(0)
			for _, c := range outs {
				if c.PosID != dp {
					eff += c.Amount
				}
			}
			if eff == 0 {
				// Self-only out — no obligations expected for this debtor.
				if obsByDebtor[dp] != 0 {
					t.Fatalf("iter %d debtor %s: got %d obligation total, expected 0",
						i, dp, obsByDebtor[dp])
				}
				continue
			}
			if obsByDebtor[dp] != want {
				t.Fatalf("iter %d debtor %s: sum=%d, want %d",
					i, dp, obsByDebtor[dp], want)
			}
		}
	}
}

func TestGenerate_DeterministicAcrossInputOrder(t *testing.T) {
	t.Parallel()
	rotate := func(out, in []Line) ([]Line, []Line) {
		// rotate slices to verify sort independence
		return append(out[1:], out[0]), append(in[1:], in[0])
	}
	outs := []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 3},
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 4},
	}
	ins := []Line{
		{PosID: "X", Currency: "idr", Direction: DirIn, Amount: 3},
		{PosID: "Y", Currency: "idr", Direction: DirIn, Amount: 4},
	}
	gen1, _ := counter()
	a, _ := GenerateBorrowObligations("tx", append(outs, ins...), t0, gen1)
	rotOuts, rotIns := rotate(outs, ins)
	gen2, _ := counter()
	b, _ := GenerateBorrowObligations("tx", append(rotOuts, rotIns...), t0, gen2)

	if len(a) != len(b) {
		t.Fatalf("nondeterministic count %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].CreditorPosID != b[i].CreditorPosID || a[i].DebtorPosID != b[i].DebtorPosID || a[i].Owed != b[i].Owed {
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
		t.Errorf("got %d obligations on error, want 0", len(obs))
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
		{PosID: "A", Currency: "USD", Direction: DirOut, Amount: 1},
		{PosID: "D", Currency: "USD", Direction: DirIn, Amount: 1},
	}, t0, gen)
	if !errors.Is(err, ErrInvalidCurrency) {
		t.Errorf("err = %v, want ErrInvalidCurrency", err)
	}
}

func TestGenerate_EmptyOrSingleSidedRejected(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	cases := [][]Line{
		nil,
		{{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 1}},
		{{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 1}},
	}
	for _, lines := range cases {
		_, err := GenerateBorrowObligations("tx", lines, t0, gen)
		if !errors.Is(err, ErrEmptyBorrow) {
			t.Errorf("lines=%v err=%v, want ErrEmptyBorrow", lines, err)
		}
	}
}

func TestGenerate_RejectsUnknownDirection(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	_, err := GenerateBorrowObligations("tx", []Line{
		{PosID: "A", Currency: "idr", Direction: "sideways", Amount: 1},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 1},
	}, t0, gen)
	if !errors.Is(err, ErrUnknownDirection) {
		t.Errorf("err = %v, want ErrUnknownDirection", err)
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
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 1000},
	}, "rtx", t0.Add(time.Hour), gen)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(plan.Progressed) != 1 || plan.Progressed[0].Repaid != 1000 || plan.Progressed[0].ClearedAt == nil {
		t.Errorf("Progressed: %+v", plan.Progressed)
	}
	if *calls != 0 {
		t.Errorf("idGen called %d times, want 0 (no overpayment)", *calls)
	}
}

// TestMatch_FIFO_ByCreatedAt_AssertsOlderTouched: explicitly assert
// the older obligation appears in Progressed and the newer doesn't.
// Beck R3.
func TestMatch_FIFO_ByCreatedAt_AssertsOlderTouched(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "newer", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1000, CreatedAt: t0.Add(2 * time.Hour)},
		{ID: "older", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1000, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 600},
	}, "rtx", t0, gen)
	if len(plan.Progressed) != 1 {
		t.Fatalf("len(Progressed) = %d, want 1 (only older touched)", len(plan.Progressed))
	}
	if plan.Progressed[0].ID != "older" {
		t.Errorf("Progressed[0].ID = %q, want older (FIFO violated)", plan.Progressed[0].ID)
	}
	if plan.Progressed[0].Repaid != 600 {
		t.Errorf("older.Repaid = %d, want 600", plan.Progressed[0].Repaid)
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
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 1500},
	}, "rtx-42", t0, gen)
	if len(plan.ReverseObligations) != 1 {
		t.Fatalf("got %d reverse, want 1", len(plan.ReverseObligations))
	}
	rev := plan.ReverseObligations[0]
	if rev.CreditorPosID != "D" || rev.DebtorPosID != "C" || rev.Owed != 500 || rev.TransactionID != "rtx-42" {
		t.Errorf("reverse: %+v", rev)
	}
	if *calls != 1 {
		t.Errorf("idGen calls=%d, want 1", *calls)
	}
}

// TestMatch_ManyOverpayments_SpawnDistinctCreatedAt: spec invariant —
// FIFO ordering of spawned reverses is deterministic across runs.
// Skeet R6.
func TestMatch_ManyOverpayments_SpawnDistinctCreatedAt(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	plan, _ := MatchRepayments(nil, []RepaymentLine{
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 100},
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 200},
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 300},
	}, "rtx", t0, gen)
	if len(plan.ReverseObligations) != 3 {
		t.Fatalf("got %d reverse, want 3", len(plan.ReverseObligations))
	}
	// Each spawn should have strictly increasing CreatedAt.
	for i := 1; i < len(plan.ReverseObligations); i++ {
		if !plan.ReverseObligations[i].CreatedAt.After(plan.ReverseObligations[i-1].CreatedAt) {
			t.Errorf("ReverseObligations[%d].CreatedAt %v not after [%d].CreatedAt %v",
				i, plan.ReverseObligations[i].CreatedAt,
				i-1, plan.ReverseObligations[i-1].CreatedAt)
		}
	}
}

func TestMatch_GoldDropScenario_PartialRepayLeavesOriginalOpen(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "gold-pool", DebtorPosID: "kids-school",
			Currency: "g", Owed: 5, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{DebtorPosID: "kids-school", CreditorPosID: "gold-pool", Currency: "g", Amount: 4},
	}, "rtx", t0, gen)
	if u := plan.Progressed[0]; u.Repaid != 4 || u.ClearedAt != nil {
		t.Errorf("partial: %+v", u)
	}
	if len(plan.ReverseObligations) != 0 {
		t.Errorf("got %d reverse, want 0", len(plan.ReverseObligations))
	}
}

// TestMatch_BatchedPayments_RouteByID asserts each payment lands on its
// intended obligation by ID, not just that all clear. Beck R4.
func TestMatch_BatchedPayments_RouteByID(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "AB", CreditorPosID: "A", DebtorPosID: "B", Currency: "idr",
			Owed: 100, CreatedAt: t0},
		{ID: "XY", CreditorPosID: "X", DebtorPosID: "Y", Currency: "idr",
			Owed: 200, CreatedAt: t0},
	}
	plan, _ := MatchRepayments(open, []RepaymentLine{
		{DebtorPosID: "B", CreditorPosID: "A", Currency: "idr", Amount: 100},
		{DebtorPosID: "Y", CreditorPosID: "X", Currency: "idr", Amount: 200},
	}, "rtx", t0, gen)
	byID := map[string]Obligation{}
	for _, u := range plan.Progressed {
		byID[u.ID] = u
	}
	if got := byID["AB"]; got.Repaid != 100 || got.ClearedAt == nil {
		t.Errorf("AB: %+v", got)
	}
	if got := byID["XY"]; got.Repaid != 200 || got.ClearedAt == nil {
		t.Errorf("XY: %+v", got)
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
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 500},
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 500},
	}, "rtx", t0, gen)
	if plan.Progressed[0].Repaid != 1000 || plan.Progressed[0].ClearedAt == nil {
		t.Errorf("two halves should fully clear: %+v", plan.Progressed[0])
	}
}

func TestMatch_NoOpenObligation_CreatesReverse(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	plan, _ := MatchRepayments(nil, []RepaymentLine{
		{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 700},
	}, "rtx", t0, gen)
	if len(plan.Progressed) != 0 {
		t.Errorf("no-open with %d progressed", len(plan.Progressed))
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
		{"non-positive", RepaymentLine{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: 0}, ErrNonPositiveAmount},
		{"negative", RepaymentLine{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: -1}, ErrNonPositiveAmount},
		{"empty currency", RepaymentLine{DebtorPosID: "D", CreditorPosID: "C", Currency: "", Amount: 1}, ErrInvalidCurrency},
		{"uppercase currency", RepaymentLine{DebtorPosID: "D", CreditorPosID: "C", Currency: "IDR", Amount: 1}, ErrInvalidCurrency},
		{"self-payment", RepaymentLine{DebtorPosID: "X", CreditorPosID: "X", Currency: "idr", Amount: 1}, ErrSelfDebt},
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

func TestMatch_RejectsDuplicateObligationID(t *testing.T) {
	t.Parallel()
	gen, _ := counter()
	open := []Obligation{
		{ID: "dup", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1, CreatedAt: t0},
		{ID: "dup", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 1, CreatedAt: t0},
	}
	_, err := MatchRepayments(open, nil, "rtx", t0, gen)
	if !errors.Is(err, ErrDuplicateID) {
		t.Errorf("err = %v, want ErrDuplicateID", err)
	}
}

// --- §10.7 invariant: sum-conservation property test ---

// TestProperty_SumConservation: for any (owed, repay) input, the total
// payment amount must equal the total Repaid increase plus the spawned
// reverse-obligation amount. Beck R2 — previous property only asserted
// Validate(), which a buggy matcher could pass while shorting users on
// either Repaid bumps or reverse-obligation amounts.
func TestProperty_SumConservation(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(0xC0FFEE))
	for i := 0; i < 10000; i++ {
		owed := rng.Int63n(100_000) + 1
		repay := rng.Int63n(2*owed + 1)
		gen, _ := counter()
		open := []Obligation{
			{ID: "o", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
				Owed: owed, CreatedAt: t0},
		}
		var payments []RepaymentLine
		if repay > 0 {
			payments = []RepaymentLine{
				{DebtorPosID: "D", CreditorPosID: "C", Currency: "idr", Amount: repay},
			}
		}
		plan, err := MatchRepayments(open, payments, "rtx", t0.Add(time.Second), gen)
		if err != nil {
			t.Fatalf("iter %d (owed=%d repay=%d): %v", i, owed, repay, err)
		}
		var repaidDelta, reverseTotal int64
		for _, u := range plan.Progressed {
			repaidDelta += u.Repaid // original Repaid was 0
			if err := u.Validate(); err != nil {
				t.Fatalf("iter %d update fails Validate: %v", i, err)
			}
		}
		for _, n := range plan.ReverseObligations {
			reverseTotal += n.Owed
			if err := n.Validate(); err != nil {
				t.Fatalf("iter %d reverse fails Validate: %v", i, err)
			}
		}
		if repaidDelta+reverseTotal != repay {
			t.Fatalf("iter %d: repaidDelta=%d + reverseTotal=%d != payment=%d",
				i, repaidDelta, reverseTotal, repay)
		}
	}
}

func TestObligation_Validate_GuardsBothHalvesOfTheIff(t *testing.T) {
	t.Parallel()
	cleared := t0
	cases := []struct {
		name   string
		o      Obligation
		errors bool
	}{
		{"cleared with repaid == owed", Obligation{
			ID: "o", CreditorPosID: "C", DebtorPosID: "D", Owed: 100, Repaid: 100, ClearedAt: &cleared}, false},
		{"not cleared with repaid < owed", Obligation{
			ID: "o", CreditorPosID: "C", DebtorPosID: "D", Owed: 100, Repaid: 50}, false},
		{"cleared with repaid < owed (§10.7)", Obligation{
			ID: "o", CreditorPosID: "C", DebtorPosID: "D", Owed: 100, Repaid: 50, ClearedAt: &cleared}, true},
		{"not cleared with repaid >= owed (§10.7)", Obligation{
			ID: "o", CreditorPosID: "C", DebtorPosID: "D", Owed: 100, Repaid: 100}, true},
		{"repaid > owed (storage CHECK)", Obligation{
			ID: "o", CreditorPosID: "C", DebtorPosID: "D", Owed: 100, Repaid: 150, ClearedAt: &cleared}, true},
		{"self-debt", Obligation{
			ID: "o", CreditorPosID: "X", DebtorPosID: "X", Owed: 100, Repaid: 50}, true},
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
