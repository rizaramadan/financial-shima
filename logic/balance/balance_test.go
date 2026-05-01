package balance

import (
	"errors"
	"math/rand"
	"testing"
)

// --- Unit tests ---

func TestMoneyIn_AddsToAccountAndPos(t *testing.T) {
	t.Parallel()
	s := New()
	err := s.Apply(MoneyIn{
		AccountID: "acc", AccountIDR: 100_000,
		PosID: "p", PosCurrency: IDR, PosAmount: 100_000,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := s.Accounts["acc"]; got != 100_000 {
		t.Errorf("account = %d, want 100000", got)
	}
	if got := s.Pos[PosKey{PosID: "p", Currency: IDR}]; got != 100_000 {
		t.Errorf("pos = %d, want 100000", got)
	}
}

func TestMoneyIn_NonIDRPos_AmountsMayDiffer(t *testing.T) {
	t.Parallel()
	s := New()
	err := s.Apply(MoneyIn{
		AccountID: "acc", AccountIDR: 6_000_000,
		PosID: "g", PosCurrency: "gold-g", PosAmount: 5,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if s.Accounts["acc"] != 6_000_000 || s.Pos[PosKey{PosID: "g", Currency: "gold-g"}] != 5 {
		t.Errorf("state mismatch: %+v", s)
	}
}

func TestMoneyIn_IDRPos_MismatchedAmountsRejected(t *testing.T) {
	t.Parallel()
	s := New()
	err := s.Apply(MoneyIn{
		AccountID: "acc", AccountIDR: 100,
		PosID: "p", PosCurrency: IDR, PosAmount: 50,
	})
	if !errors.Is(err, ErrIDRPosMismatch) {
		t.Errorf("err = %v, want ErrIDRPosMismatch", err)
	}
}

func TestMoneyIn_NegativeOrZero_Rejected(t *testing.T) {
	t.Parallel()
	for _, e := range []MoneyIn{
		{AccountIDR: 0, PosAmount: 1, PosCurrency: IDR},
		{AccountIDR: -1, PosAmount: 1, PosCurrency: IDR},
		{AccountIDR: 1, PosAmount: 0, PosCurrency: IDR},
	} {
		s := New()
		if err := s.Apply(e); !errors.Is(err, ErrNonPositive) {
			t.Errorf("input %+v: err = %v, want ErrNonPositive", e, err)
		}
	}
}

func TestMoneyOut_SubtractsAndAllowsNegative(t *testing.T) {
	t.Parallel()
	s := New()
	_ = s.Apply(MoneyOut{
		AccountID: "acc", AccountIDR: 50_000,
		PosID: "p", PosCurrency: IDR, PosAmount: 50_000,
	})
	if s.Accounts["acc"] != -50_000 {
		t.Errorf("account = %d, want -50000 (negative permitted)", s.Accounts["acc"])
	}
	if s.Pos[PosKey{PosID: "p", Currency: IDR}] != -50_000 {
		t.Errorf("pos = %d, want -50000", s.Pos[PosKey{PosID: "p", Currency: IDR}])
	}
}

func TestInterPos_ReallocatesWithinCurrency(t *testing.T) {
	t.Parallel()
	s := New()
	// Seed both pos to 100k IDR.
	_ = s.Apply(MoneyIn{AccountID: "a", AccountIDR: 100_000, PosID: "src", PosCurrency: IDR, PosAmount: 100_000})
	_ = s.Apply(MoneyIn{AccountID: "a", AccountIDR: 100_000, PosID: "dst", PosCurrency: IDR, PosAmount: 100_000})
	err := s.Apply(InterPos{
		Mode: "reallocation",
		Lines: []InterPosLine{
			{PosID: "src", Currency: IDR, Direction: DirOut, Amount: 30_000},
			{PosID: "dst", Currency: IDR, Direction: DirIn, Amount: 30_000},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := s.Pos[PosKey{PosID: "src", Currency: IDR}]; got != 70_000 {
		t.Errorf("src = %d, want 70000", got)
	}
	if got := s.Pos[PosKey{PosID: "dst", Currency: IDR}]; got != 130_000 {
		t.Errorf("dst = %d, want 130000", got)
	}
}

func TestInterPos_UnreconciledLines_Rejected(t *testing.T) {
	t.Parallel()
	s := New()
	err := s.Apply(InterPos{
		Lines: []InterPosLine{
			{PosID: "a", Currency: IDR, Direction: DirOut, Amount: 100},
			{PosID: "b", Currency: IDR, Direction: DirIn, Amount: 90}, // does not match
		},
	})
	if !errors.Is(err, ErrUnreconciledLines) {
		t.Errorf("err = %v, want ErrUnreconciledLines", err)
	}
}

func TestInterPos_CrossCurrency_EachReconcilesIndependently(t *testing.T) {
	t.Parallel()
	s := New()
	// Borrow: 6M IDR out from "rp_pool", 5g gold-g in to "school".
	// §10.6 says cross-currency lines do NOT sum across; each must
	// reconcile to itself. So this should FAIL because IDR has out=6M
	// with no in, and gold-g has in=5 with no out.
	err := s.Apply(InterPos{
		Mode: "borrow",
		Lines: []InterPosLine{
			{PosID: "rp_pool", Currency: IDR, Direction: DirOut, Amount: 6_000_000},
			{PosID: "school", Currency: "gold-g", Direction: DirIn, Amount: 5},
		},
	})
	if !errors.Is(err, ErrUnreconciledLines) {
		t.Errorf("err = %v, want ErrUnreconciledLines (cross-currency must reconcile per-currency)", err)
	}
}

// --- Property tests for §10.5 / §10.6 invariants ---

// TestProperty_IDRReconciliation_AfterRandomEvents drives random valid
// money_in/money_out/inter_pos events on IDR-only accounts/pos and asserts
// after EVERY event that Σ(Account) = Σ(Pos.cash where currency=IDR).
// This is the spec §10.5 invariant verbatim.
func TestProperty_IDRReconciliation_AfterRandomEvents(t *testing.T) {
	t.Parallel()
	const seeds = 50
	for s := int64(0); s < seeds; s++ {
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(s))
			state := New()
			events := genIDREventStream(rng, 200)
			for i, e := range events {
				if err := state.Apply(e); err != nil {
					t.Fatalf("seed=%d event=%d: %v", s, i, err)
				}
				accSum := state.AccountTotal()
				posSum := state.PosCashTotal(IDR)
				if accSum != posSum {
					t.Fatalf("seed=%d event=%d §10.5 broken: Σ(Account)=%d Σ(Pos.cash IDR)=%d",
						s, i, accSum, posSum)
				}
			}
		})
	}
}

// TestProperty_InterPosLines_ReconcilePerCurrency: §10.6 says for each
// inter_pos and each currency in its lines, Σ(out) = Σ(in). We test the
// negative space: feeding a generated unreconciled inter_pos always
// rejects.
func TestProperty_InterPosLines_ReconcilePerCurrency(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 50; i++ {
		ip := genUnreconciledInterPos(rng)
		err := New().Apply(ip)
		if !errors.Is(err, ErrUnreconciledLines) {
			t.Errorf("iter %d: unreconciled lines accepted; err=%v lines=%+v", i, err, ip.Lines)
		}
	}
}

// genIDREventStream produces a random sequence of valid IDR-only events
// across MULTIPLE accounts and pos. The multi-account spread is what
// distinguishes "code correctly aggregates §10.5 across accounts" from
// "code uses the only key it has." Beck Phase-6 R3.
func genIDREventStream(rng *rand.Rand, n int) []Event {
	accountIDs := []string{"acc-1", "acc-2"}
	const maxAmount = 1_000_000
	posIDs := []string{"p-A", "p-B", "p-C"}

	out := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			amount := int64(rng.Intn(maxAmount) + 1)
			out = append(out, MoneyIn{
				AccountID: accountIDs[rng.Intn(len(accountIDs))], AccountIDR: amount,
				PosID: posIDs[rng.Intn(len(posIDs))], PosCurrency: IDR, PosAmount: amount,
			})
		case 5, 6, 7:
			amount := int64(rng.Intn(maxAmount) + 1)
			out = append(out, MoneyOut{
				AccountID: accountIDs[rng.Intn(len(accountIDs))], AccountIDR: amount,
				PosID: posIDs[rng.Intn(len(posIDs))], PosCurrency: IDR, PosAmount: amount,
			})
		default:
			amount := int64(rng.Intn(maxAmount) + 1)
			src := posIDs[rng.Intn(len(posIDs))]
			dst := posIDs[rng.Intn(len(posIDs))]
			out = append(out, InterPos{
				Mode: "reallocation",
				Lines: []InterPosLine{
					{PosID: src, Currency: IDR, Direction: DirOut, Amount: amount},
					{PosID: dst, Currency: IDR, Direction: DirIn, Amount: amount},
				},
			})
		}
	}
	return out
}

// genUnreconciledInterPos produces an inter_pos whose per-currency totals
// don't match. With 50% probability returns a single-currency mismatch;
// the other 50% returns a TWO-currency event where one currency
// reconciles and the other doesn't — the bug surface is "code that
// aggregates across currencies" wouldn't catch the second form.
// Beck Phase-6 R4.
func genUnreconciledInterPos(rng *rand.Rand) InterPos {
	if rng.Intn(2) == 0 {
		out := int64(rng.Intn(10000) + 1)
		in := out + int64(rng.Intn(100)+1)
		return InterPos{
			Lines: []InterPosLine{
				{PosID: "x", Currency: IDR, Direction: DirOut, Amount: out},
				{PosID: "y", Currency: IDR, Direction: DirIn, Amount: in},
			},
		}
	}
	// Two-currency case: IDR reconciles (out=in=1000) but gold-g doesn't.
	idrAmt := int64(rng.Intn(10000) + 1)
	goldOut := int64(rng.Intn(10) + 1)
	goldIn := goldOut + int64(rng.Intn(5)+1)
	return InterPos{
		Lines: []InterPosLine{
			{PosID: "x1", Currency: IDR, Direction: DirOut, Amount: idrAmt},
			{PosID: "y1", Currency: IDR, Direction: DirIn, Amount: idrAmt}, // IDR balances
			{PosID: "x2", Currency: "gold-g", Direction: DirOut, Amount: goldOut},
			{PosID: "y2", Currency: "gold-g", Direction: DirIn, Amount: goldIn}, // gold-g doesn't
		},
	}
}
