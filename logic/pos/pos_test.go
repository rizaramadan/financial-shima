package pos

import (
	"strings"
	"testing"
)

func TestValidate_HappyPath(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{
		Name: "Mortgage", Currency: "idr",
		Target: 12_000_000, HasTarget: true,
	})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidate_NoTargetAllowed(t *testing.T) {
	t.Parallel()
	// Pos without a target ("open" envelope) is valid per spec §4.2.
	errs := Validate(CreateInput{Name: "Anak Sekolah", Currency: "idr"})
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestValidate_EmptyName_Rejected(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{Name: "  ", Currency: "idr"})
	if len(errs) == 0 || !contains(errs, "Name is required") {
		t.Errorf("want Name required, got %v", errs)
	}
}

func TestValidate_EmptyCurrency_Rejected(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{Name: "X", Currency: ""})
	if len(errs) == 0 || !contains(errs, "Currency is required") {
		t.Errorf("want Currency required, got %v", errs)
	}
}

func TestValidate_UppercaseCurrency_Rejected(t *testing.T) {
	t.Parallel()
	// Spec §4.2 — currency regex is lowercase-only. The handler should
	// normalize before validating, but if it doesn't, we reject loudly.
	errs := Validate(CreateInput{Name: "X", Currency: "IDR"})
	if len(errs) == 0 || !contains(errs, "lowercase") {
		t.Errorf("want lowercase rejection, got %v", errs)
	}
}

func TestValidate_CurrencyWithSpaces_Rejected(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{Name: "X", Currency: "id r"})
	if len(errs) == 0 || !contains(errs, "lowercase") {
		t.Errorf("want regex rejection, got %v", errs)
	}
}

func TestValidate_GoldGCurrency_Allowed(t *testing.T) {
	t.Parallel()
	// "gold-g" is the operator's gold subdivision — must pass.
	errs := Validate(CreateInput{Name: "Tabungan Emas", Currency: "gold-g"})
	if len(errs) != 0 {
		t.Errorf("gold-g should be valid, got %v", errs)
	}
}

func TestValidate_NegativeTarget_Rejected(t *testing.T) {
	t.Parallel()
	errs := Validate(CreateInput{
		Name: "X", Currency: "idr",
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
		Name: "X", Currency: "idr",
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
	})
	if got.Name != "Mortgage" {
		t.Errorf("Name: got %q, want %q", got.Name, "Mortgage")
	}
	if got.Currency != "idr" {
		t.Errorf("Currency: got %q, want %q", got.Currency, "idr")
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
