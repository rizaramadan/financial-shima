package pos

import (
	"strings"
	"testing"
)

// Test fixture — the Validate layer doesn't parse UUIDs; this is just a
// non-empty placeholder. Format is irrelevant to the rule under test.
const validAccountID = "11111111-1111-1111-1111-111111111111"

func TestValidate_HappyPath(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{
		Name: "Mortgage", Currency: "idr",
		AccountID: "11111111-1111-1111-1111-111111111111",
		Target:    12_000_000, HasTarget: true,
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidate_NoTargetAllowed(t *testing.T) {
	t.Parallel()
	// Pos without a target ("open" envelope) is valid per spec §4.2.
	errs := Validate(CreateInput{Name: "Anak Sekolah", Currency: "idr", AccountID: validAccountID})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidate_EmptyName_Rejected(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{Name: "  ", Currency: "idr", AccountID: validAccountID})
	if len(errs) == 0 || !contains(errs, "Name is required") {
		t.Errorf("want Name required, got %v", errs)
	}
}

func TestValidate_EmptyCurrency_Rejected(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{Name: "X", Currency: "", AccountID: validAccountID})
	if len(errs) == 0 || !contains(errs, "Currency is required") {
		t.Errorf("want Currency required, got %v", errs)
	}
}

func TestValidate_UppercaseCurrency_Rejected(t *testing.T) {
	t.Parallel()
	// Spec §4.2 — currency regex is lowercase-only. The handler should
	// normalize before validating, but if it doesn't, we reject loudly.
	errs := Validate(CreateInput{Name: "X", Currency: "IDR", AccountID: validAccountID})
	if len(errs) == 0 || !contains(errs, "lowercase") {
		t.Errorf("want lowercase rejection, got %v", errs)
	}
}

func TestValidate_CurrencyWithSpaces_Rejected(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{Name: "X", Currency: "id r", AccountID: validAccountID})
	if len(errs) == 0 || !contains(errs, "lowercase") {
		t.Errorf("want regex rejection, got %v", errs)
	}
}

func TestValidate_GoldGCurrency_Allowed(t *testing.T) {
	t.Parallel()
	// "gold-g" is the operator's gold subdivision — must pass.
	// Non-IDR Pos still requires an account_id (§4.2): it's the IDR
	// account that funded the position.
	errs := Validate(CreateInput{Name: "Tabungan Emas", Currency: "gold-g", AccountID: validAccountID})
	if len(errs) != 0 {
		t.Errorf("gold-g should be valid, got %v", errs)
	}
}

func TestValidate_MissingAccount_Rejected(t *testing.T) {
	t.Parallel()
	// Spec §4.2 — every Pos must reference an Account.
	errs := Validate(CreateInput{Name: "X", Currency: "idr", AccountID: ""})
	if len(errs) == 0 || !contains(errs, "Account is required") {
		t.Errorf("want Account required, got %v", errs)
	}
	// whitespace-only is also rejected (Normalize trims, but Validate
	// must still defend if a caller skips Normalize)
	errs = Validate(CreateInput{Name: "X", Currency: "idr", AccountID: "   "})
	if len(errs) == 0 || !contains(errs, "Account is required") {
		t.Errorf("want Account required for whitespace, got %v", errs)
	}
}

func TestValidate_NegativeTarget_Rejected(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{
		Name: "X", Currency: "idr", AccountID: validAccountID,
		Target: -1, HasTarget: true,
	})
	if len(errs) == 0 || !contains(errs, "zero or positive") {
		t.Errorf("want target rejection, got %v", errs)
	}
}

func TestValidate_ZeroTarget_Allowed(t *testing.T) {
	t.Parallel()
	// Schema CHECK is target >= 0; zero is a valid (if unusual) entry.
	errs := Validate(CreateInput{
		Name: "X", Currency: "idr", AccountID: validAccountID,
		Target: 0, HasTarget: true,
	})
	if len(errs) != 0 {
		t.Errorf("zero target should be valid, got %v", errs)
	}
}

func TestNormalize_TrimsNameAndLowercasesCurrency(t *testing.T) {
	t.Parallel()
	got := Normalize(CreateInput{
		Name: "  Mortgage  ", Currency: " IDR ",
		AccountID: "  " + validAccountID + "  ",
	})
	if got.Name != "Mortgage" {
		t.Errorf("Name: got %q, want %q", got.Name, "Mortgage")
	}
	if got.Currency != "idr" {
		t.Errorf("Currency: got %q, want %q", got.Currency, "idr")
	}
	if got.AccountID != validAccountID {
		t.Errorf("AccountID: got %q, want %q", got.AccountID, validAccountID)
	}
}

func contains(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}
