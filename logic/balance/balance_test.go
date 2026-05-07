package balance

import (
	"errors"
	"math/rand"
	"testing"
)

// --- Unit tests ---

func TestMoneyIn_AddsToPosAndDerivedAccount(t *testing.T) {
	t.Parallel()
	s := New()
	if err := s.Apply(RegisterPos{PosID: "p", AccountID: "acc", Currency: IDR}); err != nil {
		t.Fatalf("RegisterPos: %v", err)
	}
	if err := s.Apply(MoneyIn{
		PosID: "p", PosCurrency: IDR, AccountIDR: 100_000, PosAmount: 100_000,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := s.Accounts()["acc"]; got != 100_000 {
		t.Errorf("derived account = %d, want 100000", got)
	}
	if got := s.Pos[PosKey{PosID: "p", Currency: IDR}]; got != 100_000 {
		t.Errorf("pos = %d, want 100000", got)
	}
}

func TestMoneyIn_NonIDRPos_AmountsMayDiffer(t *testing.T) {
	t.Parallel()
	s := New()
	_ = s.Apply(RegisterPos{PosID: "g", AccountID: "acc", Currency: "gold-g"})
	if err := s.Apply(MoneyIn{
		PosID: "g", PosCurrency: "gold-g", AccountIDR: 6_000_000, PosAmount: 5,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if s.Accounts()["acc"] != 6_000_000 {
		t.Errorf("derived account = %d, want 6_000_000", s.Accounts()["acc"])
	}
	if s.Pos[PosKey{PosID: "g", Currency: "gold-g"}] != 5 {
		t.Errorf("pos = %d, want 5", s.Pos[PosKey{PosID: "g", Currency: "gold-g"}])
	}
}

func TestMoneyIn_IDRPos_MismatchedAmountsRejected(t *testing.T) {
	t.Parallel()
	s := New()
	_ = s.Apply(RegisterPos{PosID: "p", AccountID: "acc", Currency: IDR})
	err := s.Apply(MoneyIn{
		PosID: "p", PosCurrency: IDR, AccountIDR: 100, PosAmount: 50,
	})
	if !errors.Is(err, ErrIDRPosMismatch) {
		t.Errorf("err = %v, want ErrIDRPosMismatch", err)
	}
}

func TestMoneyIn_NegativeOrZero_Rejected(t *testing.T) {
	t.Parallel()
	for _, e := range []MoneyIn{
		{PosID: "p", PosCurrency: IDR, AccountIDR: 0, PosAmount: 1},
		{PosID: "p", PosCurrency: IDR, AccountIDR: -1, PosAmount: 1},
		{PosID: "p", PosCurrency: IDR, AccountIDR: 1, PosAmount: 0},
	} {
		s := New()
		_ = s.Apply(RegisterPos{PosID: "p", AccountID: "acc", Currency: IDR})
		if err := s.Apply(e); !errors.Is(err, ErrNonPositive) {
			t.Errorf("input %+v: err = %v, want ErrNonPositive", e, err)
		}
	}
}

func TestMoneyIn_UnregisteredPos_Rejected(t *testing.T) {
	t.Parallel()
	s := New()
	err := s.Apply(MoneyIn{PosID: "ghost", PosCurrency: IDR, AccountIDR: 100, PosAmount: 100})
	if !errors.Is(err, ErrUnregisteredPos) {
		t.Errorf("err = %v, want ErrUnregisteredPos", err)
	}
}

func TestMoneyOut_SubtractsAndAllowsNegative(t *testing.T) {
	t.Parallel()
	s := New()
	_ = s.Apply(RegisterPos{PosID: "p", AccountID: "acc", Currency: IDR})
	_ = s.Apply(MoneyOut{PosID: "p", PosCurrency: IDR, AccountIDR: 50_000, PosAmount: 50_000})
	if got := s.Accounts()["acc"]; got != -50_000 {
		t.Errorf("account = %d, want -50000 (negative permitted)", got)
	}
	if s.Pos[PosKey{PosID: "p", Currency: IDR}] != -50_000 {
		t.Errorf("pos = %d, want -50000", s.Pos[PosKey{PosID: "p", Currency: IDR}])
	}
}

func TestReassign_RetroactivelyShiftsAccountAttribution(t *testing.T) {
	t.Parallel()
	// §5.6 snapshot semantics: changing pos.account_id rewrites
	// historical account-side attribution. Σ(Pos.cash IDR) = Σ(Account)
	// must hold before and after the reassignment.
	s := New()
	_ = s.Apply(RegisterPos{PosID: "p", AccountID: "old", Currency: IDR})
	_ = s.Apply(MoneyIn{PosID: "p", PosCurrency: IDR, AccountIDR: 100_000, PosAmount: 100_000})
	if s.Accounts()["old"] != 100_000 {
		t.Fatalf("pre: old = %d, want 100000", s.Accounts()["old"])
	}
	if err := s.Apply(Reassign{PosID: "p", NewAccountID: "new"}); err != nil {
		t.Fatalf("Reassign: %v", err)
	}
	got := s.Accounts()
	if got["new"] != 100_000 {
		t.Errorf("post: new = %d, want 100000", got["new"])
	}
	if got["old"] != 0 {
		t.Errorf("post: old = %d, want 0 (reattributed away)", got["old"])
	}
	// §10.5 still holds.
	if s.AccountTotal() != s.PosCashTotal(IDR) {
		t.Errorf("§10.5 broken post-reassign: acc=%d pos=%d",
			s.AccountTotal(), s.PosCashTotal(IDR))
	}
}

func TestReassign_UnregisteredPos_Rejected(t *testing.T) {
	t.Parallel()
	s := New()
	if err := s.Apply(Reassign{PosID: "ghost", NewAccountID: "acc"}); !errors.Is(err, ErrUnregisteredPos) {
		t.Errorf("err = %v, want ErrUnregisteredPos", err)
	}
}

func TestRegisterPos_Duplicate_Rejected(t *testing.T) {
	t.Parallel()
	s := New()
	_ = s.Apply(RegisterPos{PosID: "p", AccountID: "a", Currency: IDR})
	if err := s.Apply(RegisterPos{PosID: "p", AccountID: "b", Currency: IDR}); !errors.Is(err, ErrPosAlreadyKnown) {
		t.Errorf("err = %v, want ErrPosAlreadyKnown", err)
	}
}

func TestInterPos_ReallocatesWithinCurrency(t *testing.T) {
	t.Parallel()
	s := New()
	_ = s.Apply(RegisterPos{PosID: "src", AccountID: "a", Currency: IDR})
	_ = s.Apply(RegisterPos{PosID: "dst", AccountID: "a", Currency: IDR})
	_ = s.Apply(MoneyIn{PosID: "src", PosCurrency: IDR, AccountIDR: 100_000, PosAmount: 100_000})
	_ = s.Apply(MoneyIn{PosID: "dst", PosCurrency: IDR, AccountIDR: 100_000, PosAmount: 100_000})
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
// money_in/money_out/inter_pos events plus reassignments and asserts
// after EVERY event that Σ(Account) = Σ(Pos.cash where currency=IDR).
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
// across MULTIPLE accounts and pos, including periodic Pos
// reassignments. Reassign retroactively shifts attribution; §10.5 must
// hold throughout.
func genIDREventStream(rng *rand.Rand, n int) []Event {
	accountIDs := []string{"acc-1", "acc-2"}
	const maxAmount = 1_000_000
	posIDs := []string{"p-A", "p-B", "p-C"}

	out := make([]Event, 0, n+len(posIDs))
	// Register all pos up front against the first account.
	for _, p := range posIDs {
		out = append(out, RegisterPos{PosID: p, AccountID: accountIDs[0], Currency: IDR})
	}
	for i := 0; i < n; i++ {
		switch rng.Intn(11) {
		case 0, 1, 2, 3, 4:
			amount := int64(rng.Intn(maxAmount) + 1)
			out = append(out, MoneyIn{
				PosID:       posIDs[rng.Intn(len(posIDs))],
				PosCurrency: IDR, AccountIDR: amount, PosAmount: amount,
			})
		case 5, 6, 7:
			amount := int64(rng.Intn(maxAmount) + 1)
			out = append(out, MoneyOut{
				PosID:       posIDs[rng.Intn(len(posIDs))],
				PosCurrency: IDR, AccountIDR: amount, PosAmount: amount,
			})
		case 8, 9:
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
		default:
			// Reassign — moves a Pos to a different account. The §10.5
			// property must still hold because Σ over all accounts
			// equals Σ over all pos regardless of attribution.
			out = append(out, Reassign{
				PosID:        posIDs[rng.Intn(len(posIDs))],
				NewAccountID: accountIDs[rng.Intn(len(accountIDs))],
			})
		}
	}
	return out
}

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
	idrAmt := int64(rng.Intn(10000) + 1)
	goldOut := int64(rng.Intn(10) + 1)
	goldIn := goldOut + int64(rng.Intn(5)+1)
	return InterPos{
		Lines: []InterPosLine{
			{PosID: "x1", Currency: IDR, Direction: DirOut, Amount: idrAmt},
			{PosID: "y1", Currency: IDR, Direction: DirIn, Amount: idrAmt},
			{PosID: "x2", Currency: "gold-g", Direction: DirOut, Amount: goldOut},
			{PosID: "y2", Currency: "gold-g", Direction: DirIn, Amount: goldIn},
		},
	}
}
