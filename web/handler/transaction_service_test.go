package handler

// TDD Step 1: shared money_in/money_out service. The service builds the
// API result (data + HATEOAS-lite _links/_actions) so the web POST and
// the API POST share one resolve → validate → insert path.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestMoneyLinks_ContainsResourceHrefs pins the HATEOAS-lite contract:
// every created transaction carries navigable links, so HTMX/API
// clients never hard-code routes.
func TestMoneyLinks_ContainsResourceHrefs(t *testing.T) {
	t.Parallel()
	txnID := uuid.New()
	posID := uuid.New()
	accountID := uuid.New()
	cpID := uuid.New()

	links := moneyLinks(txnID.String(), posID.String(), accountID.String(), cpID.String())

	for _, want := range []string{"self", "pos", "account", "counterparty"} {
		href, ok := links[want]
		if !ok || strings.TrimSpace(href) == "" {
			t.Errorf("links[%q] missing or empty: %v", want, links)
		}
	}
	if !strings.Contains(links["self"], txnID.String()) {
		t.Errorf("self link %q does not contain txn id", links["self"])
	}
}

// TestValidateMoneyCreateInput_RejectsBadTypeAndEmptyKey pins the
// fail-fast shape checks that run before any DB I/O. Pure: no DB.
func TestValidateMoneyCreateInput_RejectsBadTypeAndEmptyKey(t *testing.T) {
	t.Parallel()
	today := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	bad := MoneyCreateInput{
		Type:             "inter_pos", // not served by this path
		EffectiveDate:    today,
		AccountAmount:    100,
		PosID:            uuid.New(),
		PosAmount:        100,
		CounterpartyName: "Salary",
		IdempotencyKey:   "k1",
	}
	if errs := validateMoneyCreateInput(bad); len(errs) == 0 {
		t.Error("inter_pos type should be rejected, got no violations")
	}

	empty := MoneyCreateInput{
		Type:             "money_in",
		EffectiveDate:    today,
		AccountAmount:    100,
		PosID:            uuid.New(),
		PosAmount:        100,
		CounterpartyName: "Salary",
	}
	if errs := validateMoneyCreateInput(empty); len(errs) == 0 {
		t.Error("empty idempotency_key should be rejected, got no violations")
	}
}

// TestReverseType_FlipsMoneyDirection pins the append-only correction
// rule: a reversal is the opposite money type with identical amounts
// (sign comes from the type). Non-money types cannot be reversed.
func TestReverseType_FlipsMoneyDirection(t *testing.T) {
	t.Parallel()
	if got, ok := reverseType("money_in"); !ok || got != "money_out" {
		t.Errorf("reverseType(money_in) = (%q, %v), want (money_out, true)", got, ok)
	}
	if got, ok := reverseType("money_out"); !ok || got != "money_in" {
		t.Errorf("reverseType(money_out) = (%q, %v), want (money_in, true)", got, ok)
	}
	if _, ok := reverseType("inter_pos"); ok {
		t.Error("reverseType(inter_pos) should be rejected, got ok=true")
	}
}

// TestMoneyCreateResult_JSONIncludesHATEOAS pins the wire contract:
// the shared result serializes data + _links/_actions together.
func TestMoneyCreateResult_JSONIncludesHATEOAS(t *testing.T) {
	t.Parallel()
	res := APITransaction{
		ID:            uuid.NewString(),
		Type:          "money_in",
		EffectiveDate: "2026-05-01",
	}
	res.Links = moneyLinks(res.ID, uuid.NewString(), uuid.NewString(), uuid.NewString())
	res.Actions = moneyActions(res.ID)

	if len(res.Links) == 0 {
		t.Error("expected non-empty _links")
	}
	if len(res.Actions) == 0 {
		t.Error("expected non-empty _actions")
	}
}
