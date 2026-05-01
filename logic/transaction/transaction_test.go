package transaction

import (
	"strings"
	"testing"
	"time"

	"github.com/rizaramadan/financial-shima/logic/money"
)

var today = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

func validIDRMoneyIn() MoneyInput {
	return MoneyInput{
		EffectiveDate:    today,
		Account:          AccountRef{ID: "acc-1"},
		AccountAmount:    money.New(100_000, "IDR"),
		Pos:              PosRef{ID: "pos-1", Currency: "idr"},
		PosAmount:        money.New(100_000, "IDR"),
		CounterpartyName: "Salary",
	}
}

func TestValidateMoneyIn_HappyPath(t *testing.T) {
	t.Parallel()
	errs := ValidateMoneyIn(validIDRMoneyIn(), today)
	if len(errs) != 0 {
		t.Errorf("happy path errors: %v", errs)
	}
}

func TestValidateMoneyIn_FutureDate_Rejected(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.EffectiveDate = today.Add(24 * time.Hour)
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "future") {
		t.Errorf("expected future-date error, got %v", errs)
	}
}

func TestValidateMoneyIn_PastDate_Allowed(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.EffectiveDate = today.Add(-30 * 24 * time.Hour)
	errs := ValidateMoneyIn(in, today)
	if len(errs) != 0 {
		t.Errorf("past date should be allowed: %v", errs)
	}
}

func TestValidateMoneyIn_ZeroDate_Required(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.EffectiveDate = time.Time{}
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "required") {
		t.Errorf("expected required error, got %v", errs)
	}
}

func TestValidateMoneyIn_ArchivedAccount_Rejected(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.Account.Archived = true
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "account is archived") {
		t.Errorf("expected archived account error, got %v", errs)
	}
}

func TestValidateMoneyIn_ArchivedPos_Rejected(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.Pos.Archived = true
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "pos is archived") {
		t.Errorf("expected archived pos error, got %v", errs)
	}
}

func TestValidateMoneyIn_NegativeAmounts_Rejected(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.AccountAmount = money.New(-100, "idr")
	in.PosAmount = money.New(-100, "idr")
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "account amount must be positive") {
		t.Errorf("expected account amount error, got %v", errs)
	}
	if !containsContaining(errs, "pos amount must be positive") {
		t.Errorf("expected pos amount error, got %v", errs)
	}
}

func TestValidateMoneyIn_ZeroAmounts_Rejected(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.AccountAmount = money.New(0, "idr")
	in.PosAmount = money.New(0, "idr")
	errs := ValidateMoneyIn(in, today)
	if len(errs) < 2 {
		t.Errorf("zero amounts should produce errors, got %v", errs)
	}
}

func TestValidateMoneyIn_AccountNotIDR_Rejected(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.AccountAmount = money.New(100, "usd")
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "account amount must be IDR") {
		t.Errorf("expected IDR-only error, got %v", errs)
	}
}

func TestValidateMoneyIn_IDRPos_AmountsMustMatch(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.PosAmount = money.New(50_000, "idr") // mismatch with account 100_000
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "must equal pos amount") {
		t.Errorf("expected IDR-pos amount-equality error, got %v", errs)
	}
}

func TestValidateMoneyIn_NonIDRPos_AmountsMayDiffer(t *testing.T) {
	t.Parallel()
	in := MoneyInput{
		EffectiveDate:    today,
		Account:          AccountRef{ID: "acc-1"},
		AccountAmount:    money.New(6_000_000, "idr"), // Rp 6M
		Pos:              PosRef{ID: "pos-1", Currency: "gold-g"},
		PosAmount:        money.New(5, "gold-g"), // 5 grams
		CounterpartyName: "Bullion Store",
	}
	errs := ValidateMoneyIn(in, today)
	if len(errs) != 0 {
		t.Errorf("cross-currency Pos should allow diff amounts: %v", errs)
	}
}

func TestValidateMoneyIn_PosCurrencyMismatch_Rejected(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.PosAmount = money.New(100_000, "usd") // pos is idr
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "pos currency mismatch") {
		t.Errorf("expected currency mismatch, got %v", errs)
	}
}

func TestValidateMoneyIn_CounterpartyEmpty_Rejected(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.CounterpartyName = "  "
	errs := ValidateMoneyIn(in, today)
	if !containsContaining(errs, "counterparty is required") {
		t.Errorf("expected required, got %v", errs)
	}
}

func TestValidateMoneyIn_CounterpartyBadChars_Rejected(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"Salary!", "@boss", "name<script>", "comma,name"} {
		t.Run(bad, func(t *testing.T) {
			in := validIDRMoneyIn()
			in.CounterpartyName = bad
			errs := ValidateMoneyIn(in, today)
			if !containsContaining(errs, "invalid characters") {
				t.Errorf("expected invalid-char error for %q, got %v", bad, errs)
			}
		})
	}
}

func TestValidateMoneyIn_CounterpartyValid(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"Salary", "Bank Transfer", "John_Doe", "Vendor-2", "ABC 123"} {
		t.Run(ok, func(t *testing.T) {
			in := validIDRMoneyIn()
			in.CounterpartyName = ok
			errs := ValidateMoneyIn(in, today)
			for _, e := range errs {
				if strings.Contains(e, "counterparty") {
					t.Errorf("expected %q to validate, got %v", ok, errs)
				}
			}
		})
	}
}

func TestValidateMoneyOut_AppliesSameRules(t *testing.T) {
	t.Parallel()
	in := validIDRMoneyIn()
	in.AccountAmount = money.New(-1, "idr") // bad
	errs := ValidateMoneyOut(in, today)
	if !containsContaining(errs, "positive") {
		t.Errorf("ValidateMoneyOut should reject same as MoneyIn, got %v", errs)
	}
}

// --- inter_pos ---

func validReallocation() InterPosInput {
	return InterPosInput{
		EffectiveDate: today,
		Mode:          ModeReallocation,
		Lines: []InterPosLine{
			{Pos: PosRef{ID: "p-out", Currency: "idr"}, Direction: DirOut, Amount: money.New(50_000, "idr")},
			{Pos: PosRef{ID: "p-in", Currency: "idr"}, Direction: DirIn, Amount: money.New(50_000, "idr")},
		},
	}
}

func TestValidateInterPos_HappyPath(t *testing.T) {
	t.Parallel()
	errs := ValidateInterPos(validReallocation(), today)
	if len(errs) != 0 {
		t.Errorf("happy path errors: %v", errs)
	}
}

func TestValidateInterPos_RequiresOutAndIn(t *testing.T) {
	t.Parallel()
	in := validReallocation()
	in.Lines = in.Lines[:1] // only an "out" line
	errs := ValidateInterPos(in, today)
	if !containsContaining(errs, "at least one 'in' line") {
		t.Errorf("expected missing 'in' line error, got %v", errs)
	}

	in = validReallocation()
	in.Lines = in.Lines[1:] // only an "in" line
	errs = ValidateInterPos(in, today)
	if !containsContaining(errs, "at least one 'out' line") {
		t.Errorf("expected missing 'out' line error, got %v", errs)
	}
}

func TestValidateInterPos_PerCurrencyTotalsMustMatch(t *testing.T) {
	t.Parallel()
	in := validReallocation()
	in.Lines[1].Amount = money.New(40_000, "idr") // Σ(in)=40k, Σ(out)=50k
	errs := ValidateInterPos(in, today)
	if !containsContaining(errs, "Σ(out)") {
		t.Errorf("expected per-currency sum mismatch, got %v", errs)
	}
}

func TestValidateInterPos_CrossCurrency_DoesNotSumAcross(t *testing.T) {
	t.Parallel()
	// spec §10.6: per-currency totals reconcile separately. An out in IDR and
	// an in in gold-g are valid, even though the two amounts have nothing to
	// do with each other numerically.
	in := InterPosInput{
		EffectiveDate: today,
		Mode:          ModeBorrow,
		Lines: []InterPosLine{
			{Pos: PosRef{ID: "rp", Currency: "idr"}, Direction: DirOut,
				Amount: money.New(6_000_000, "idr")},
			{Pos: PosRef{ID: "gp", Currency: "gold-g"}, Direction: DirIn,
				Amount: money.New(5, "gold-g")},
		},
	}
	errs := ValidateInterPos(in, today)
	// One IDR line out, no IDR line in → IDR doesn't reconcile.
	// One gold-g in, no gold-g out → gold-g doesn't reconcile.
	// So 2 reconciliation errors expected.
	got := 0
	for _, e := range errs {
		if strings.Contains(e, "Σ") {
			got++
		}
	}
	if got != 2 {
		t.Errorf("got %d Σ errors, want 2 (idr + gold-g); errs=%v", got, errs)
	}
}

func TestValidateInterPos_PosArchived_Rejected(t *testing.T) {
	t.Parallel()
	in := validReallocation()
	in.Lines[0].Pos.Archived = true
	errs := ValidateInterPos(in, today)
	if !containsContaining(errs, "pos is archived") {
		t.Errorf("expected archived pos error, got %v", errs)
	}
}

func TestValidateInterPos_LineAmountCurrencyMismatch_Rejected(t *testing.T) {
	t.Parallel()
	in := validReallocation()
	in.Lines[0].Amount = money.New(50_000, "usd") // pos is idr
	errs := ValidateInterPos(in, today)
	if !containsContaining(errs, "does not match pos currency") {
		t.Errorf("expected currency mismatch, got %v", errs)
	}
}

func TestValidateInterPos_NegativeOrZeroLineAmount_Rejected(t *testing.T) {
	t.Parallel()
	for name, amt := range map[string]int64{"zero": 0, "negative": -10} {
		t.Run(name, func(t *testing.T) {
			in := validReallocation()
			in.Lines[0].Amount = money.New(amt, "idr")
			in.Lines[1].Amount = money.New(amt, "idr") // keep totals matching to not double-trigger
			errs := ValidateInterPos(in, today)
			if !containsContaining(errs, "must be positive") {
				t.Errorf("expected positive-amount error, got %v", errs)
			}
		})
	}
}

func TestValidateInterPos_BadDirection_Rejected(t *testing.T) {
	t.Parallel()
	in := validReallocation()
	in.Lines[0].Direction = "sideways"
	errs := ValidateInterPos(in, today)
	if !containsContaining(errs, "direction must be") {
		t.Errorf("expected bad-direction error, got %v", errs)
	}
}

func TestValidateInterPos_BadMode_Rejected(t *testing.T) {
	t.Parallel()
	in := validReallocation()
	in.Mode = "barter"
	errs := ValidateInterPos(in, today)
	if !containsContaining(errs, "mode must be") {
		t.Errorf("expected bad-mode error, got %v", errs)
	}
}

func TestValidateInterPos_FutureDate_Rejected(t *testing.T) {
	t.Parallel()
	in := validReallocation()
	in.EffectiveDate = today.Add(24 * time.Hour)
	errs := ValidateInterPos(in, today)
	if !containsContaining(errs, "future") {
		t.Errorf("expected future-date error, got %v", errs)
	}
}

func containsContaining(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
