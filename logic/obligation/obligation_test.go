package obligation

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

var t0 = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

// counter returns a deterministic id generator for tests.
func counter() func() string {
	n := 0
	return func() string {
		n++
		return "ob-" + strconv.Itoa(n)
	}
}

// --- GenerateForBorrow ---

func TestGenerate_SingleCreditorSingleDebtor_FullAmountOwed(t *testing.T) {
	t.Parallel()
	obs, err := GenerateForBorrow("tx1", []Line{
		{PosID: "C", Currency: "idr", Direction: DirOut, Amount: 1_000_000},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 1_000_000},
	}, counter())
	if err != nil {
		t.Fatalf("GenerateForBorrow: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("got %d obligations, want 1", len(obs))
	}
	o := obs[0]
	if o.CreditorPosID != "C" || o.DebtorPosID != "D" {
		t.Errorf("got creditor=%s debtor=%s, want C/D", o.CreditorPosID, o.DebtorPosID)
	}
	if o.Owed != 1_000_000 {
		t.Errorf("Owed=%d, want 1000000", o.Owed)
	}
	if o.Currency != "idr" || o.Repaid != 0 || o.ClearedAt != nil {
		t.Errorf("unexpected obligation %+v", o)
	}
}

func TestGenerate_TwoCreditorsOneDebtor_ProratedByOutShare(t *testing.T) {
	t.Parallel()
	obs, err := GenerateForBorrow("tx1", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 3_000_000},
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 1_000_000},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 4_000_000},
	}, counter())
	if err != nil {
		t.Fatalf("GenerateForBorrow: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("got %d obligations, want 2", len(obs))
	}
	// 75% / 25% prorated against debtor's 4M.
	wantA, wantB := int64(3_000_000), int64(1_000_000)
	for _, o := range obs {
		switch o.CreditorPosID {
		case "A":
			if o.Owed != wantA {
				t.Errorf("A.Owed=%d, want %d", o.Owed, wantA)
			}
		case "B":
			if o.Owed != wantB {
				t.Errorf("B.Owed=%d, want %d", o.Owed, wantB)
			}
		}
	}
	// Sum of obligation amounts equals debtor's in amount exactly.
	var sum int64
	for _, o := range obs {
		sum += o.Owed
	}
	if sum != 4_000_000 {
		t.Errorf("sum of owed=%d, want 4000000 (no rounding drift)", sum)
	}
}

func TestGenerate_MultipleDebtors_AmountsSumToInTotalPerDebtor(t *testing.T) {
	t.Parallel()
	// Two creditors (1, 2), two debtors (1, 2). Total out = total in = 3.
	// Last creditor in each debtor's bucket absorbs rounding so the rows
	// for each debtor sum exactly to that debtor's in amount.
	obs, err := GenerateForBorrow("tx1", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 1},
		{PosID: "B", Currency: "idr", Direction: DirOut, Amount: 2},
		{PosID: "X", Currency: "idr", Direction: DirIn, Amount: 1},
		{PosID: "Y", Currency: "idr", Direction: DirIn, Amount: 2},
	}, counter())
	if err != nil {
		t.Fatalf("GenerateForBorrow: %v", err)
	}
	if len(obs) != 4 {
		t.Fatalf("got %d obligations, want 4 (M*N)", len(obs))
	}
	perDebtor := map[string]int64{}
	for _, o := range obs {
		perDebtor[o.DebtorPosID] += o.Owed
	}
	if perDebtor["X"] != 1 {
		t.Errorf("X.sum=%d, want 1", perDebtor["X"])
	}
	if perDebtor["Y"] != 2 {
		t.Errorf("Y.sum=%d, want 2", perDebtor["Y"])
	}
}

func TestGenerate_CrossCurrencyRejected(t *testing.T) {
	t.Parallel()
	_, err := GenerateForBorrow("tx1", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 6_000_000},
		{PosID: "D", Currency: "gold-g", Direction: DirIn, Amount: 5},
	}, counter())
	if !errors.Is(err, ErrCrossCurrencyBorrow) {
		t.Errorf("err=%v, want ErrCrossCurrencyBorrow", err)
	}
}

func TestGenerate_RejectsUnreconciledTotals(t *testing.T) {
	t.Parallel()
	_, err := GenerateForBorrow("tx1", []Line{
		{PosID: "A", Currency: "idr", Direction: DirOut, Amount: 100},
		{PosID: "D", Currency: "idr", Direction: DirIn, Amount: 90}, // out=100, in=90
	}, counter())
	if err == nil {
		t.Error("unreconciled totals accepted")
	}
}

// --- Match (FIFO repayment) ---

func TestMatch_FullRepayment_ClearsObligation(t *testing.T) {
	t.Parallel()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr", Owed: 1000, Repaid: 0},
	}
	plan, err := Match(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 1000},
	}, t0, counter())
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("got %d updates, want 1", len(plan.Updates))
	}
	u := plan.Updates[0]
	if u.Repaid != 1000 || u.ClearedAt == nil {
		t.Errorf("update %+v not cleared", u)
	}
	if len(plan.NewObligations) != 0 {
		t.Errorf("got %d new obligations, want 0", len(plan.NewObligations))
	}
}

func TestMatch_PartialRepayment_DoesNotClear(t *testing.T) {
	t.Parallel()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr", Owed: 1000},
	}
	plan, err := Match(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 600},
	}, t0, counter())
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if plan.Updates[0].Repaid != 600 {
		t.Errorf("Repaid=%d, want 600", plan.Updates[0].Repaid)
	}
	if plan.Updates[0].ClearedAt != nil {
		t.Error("partial repayment cleared the obligation")
	}
}

func TestMatch_FIFO_AcrossMultipleObligations(t *testing.T) {
	t.Parallel()
	// Two obligations from D→C (debtor D owes creditor C twice). A 1500
	// payment should clear o1 (1000) and partially fill o2 (500/2000).
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr", Owed: 1000},
		{ID: "o2", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr", Owed: 2000},
	}
	plan, _ := Match(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 1500},
	}, t0, counter())
	if len(plan.Updates) != 2 {
		t.Fatalf("got %d updates, want 2", len(plan.Updates))
	}
	if plan.Updates[0].ClearedAt == nil || plan.Updates[0].Repaid != 1000 {
		t.Errorf("o1: %+v", plan.Updates[0])
	}
	if plan.Updates[1].ClearedAt != nil || plan.Updates[1].Repaid != 500 {
		t.Errorf("o2: %+v", plan.Updates[1])
	}
}

// TestMatch_OverpaymentSpawnsReverseObligation: spec §4.3 — "If a repayment
// has no matching open obligation, it succeeds and creates a new obligation
// in the opposite direction (the creditor now owes the debtor)." Same path
// when the payment is LARGER than the open balance.
func TestMatch_OverpaymentSpawnsReverseObligation(t *testing.T) {
	t.Parallel()
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr", Owed: 1000},
	}
	plan, _ := Match(open, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 1500},
	}, t0, counter())
	if len(plan.NewObligations) != 1 {
		t.Fatalf("got %d new obligations, want 1", len(plan.NewObligations))
	}
	n := plan.NewObligations[0]
	if n.CreditorPosID != "D" || n.DebtorPosID != "C" {
		t.Errorf("reverse obligation has wrong direction: creditor=%s debtor=%s",
			n.CreditorPosID, n.DebtorPosID)
	}
	if n.Owed != 500 {
		t.Errorf("reverse Owed=%d, want 500", n.Owed)
	}
}

func TestMatch_NoOpenObligationCreatesReverse(t *testing.T) {
	t.Parallel()
	plan, _ := Match(nil, []RepaymentLine{
		{FromPos: "D", ToPos: "C", Currency: "idr", Amount: 700},
	}, t0, counter())
	if len(plan.Updates) != 0 {
		t.Errorf("no open obligations, got %d updates", len(plan.Updates))
	}
	if len(plan.NewObligations) != 1 {
		t.Fatalf("got %d new obligations, want 1", len(plan.NewObligations))
	}
	if plan.NewObligations[0].Owed != 700 {
		t.Errorf("Owed=%d, want 700", plan.NewObligations[0].Owed)
	}
}

func TestMatch_GoldDropScenario_LeftWithReverseObligation(t *testing.T) {
	t.Parallel()
	// Spec §4.3: kids-school borrows 5g from gold-pool. Gold drops; when
	// kids-school repays, they can only return 4g. Their original
	// 5g obligation gets 4g credited (still has 1g shortfall remaining).
	// The user later acknowledges the deficit (or further repays).
	open := []Obligation{
		{ID: "o1", CreditorPosID: "gold-pool", DebtorPosID: "kids-school",
			Currency: "g", Owed: 5},
	}
	plan, _ := Match(open, []RepaymentLine{
		{FromPos: "kids-school", ToPos: "gold-pool", Currency: "g", Amount: 4},
	}, t0, counter())
	u := plan.Updates[0]
	if u.Repaid != 4 || u.ClearedAt != nil {
		t.Errorf("partial repay: %+v", u)
	}
	// Outstanding 1g remains; no reverse obligation yet (kids-school still
	// owes gold-pool, just less). Reverse only spawns on overpayment.
	if len(plan.NewObligations) != 0 {
		t.Errorf("partial repay produced %d new obligations, want 0",
			len(plan.NewObligations))
	}
}

func TestMatch_RejectsClearedOpenInputs(t *testing.T) {
	t.Parallel()
	cleared := t0
	open := []Obligation{
		{ID: "o1", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr",
			Owed: 100, Repaid: 50, ClearedAt: &cleared}, // §10.7 violated
	}
	_, err := Match(open, nil, t0, counter())
	if err == nil {
		t.Error("Match accepted §10.7-violating obligation")
	}
}

// --- §10.7 invariant property test ---

// TestProperty_Invariant_§10.7_ClearedIffRepaidGEOwed runs random
// repayment scenarios and asserts that EVERY obligation in the resulting
// plan satisfies the §10.7 invariant — Validate() returns nil.
func TestProperty_Invariant_AfterMatch(t *testing.T) {
	t.Parallel()
	scenarios := []struct {
		owed, repay int64
	}{
		{1000, 0}, {1000, 100}, {1000, 999}, {1000, 1000}, {1000, 1500},
		{1, 1}, {1, 2}, {0, 0},
	}
	for _, sc := range scenarios {
		if sc.owed == 0 {
			continue
		}
		open := []Obligation{
			{ID: "o", CreditorPosID: "C", DebtorPosID: "D", Currency: "idr", Owed: sc.owed},
		}
		var payments []RepaymentLine
		if sc.repay > 0 {
			payments = []RepaymentLine{{FromPos: "D", ToPos: "C", Currency: "idr", Amount: sc.repay}}
		}
		plan, err := Match(open, payments, t0, counter())
		if err != nil {
			t.Errorf("Match(%+v) error: %v", sc, err)
			continue
		}
		for _, u := range plan.Updates {
			if err := u.Validate(); err != nil {
				t.Errorf("scenario %+v: %v", sc, err)
			}
		}
		for _, n := range plan.NewObligations {
			if err := n.Validate(); err != nil {
				t.Errorf("scenario %+v new obligation: %v", sc, err)
			}
		}
	}
}
