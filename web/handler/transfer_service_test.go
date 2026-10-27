package handler

// TDD Step 1 (inter-Pos transfer API): pure shape checks + HATEOAS-lite
// link builders for POST /api/v1/transfers. Resolve → validate → insert
// runs through createTransfer (transfer_service.go) so future entries
// stay behavior-identical by construction.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestValidateTransferCreateInput_RejectsBadShape pins the fail-fast
// shape checks that run before any DB I/O. Pure: no DB.
func TestValidateTransferCreateInput_RejectsBadShape(t *testing.T) {
	t.Parallel()
	today := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	goodLine := func() TransferLineInput {
		return TransferLineInput{
			PosID:     uuid.New(),
			Direction: "out",
			Amount:    100,
			Currency:  "idr",
		}
	}
	good := func() TransferCreateInput {
		a, b := goodLine(), goodLine()
		b.Direction = "in"
		return TransferCreateInput{
			Mode:           "borrow",
			EffectiveDate:  today,
			Lines:          []TransferLineInput{a, b},
			IdempotencyKey: "k1",
		}
	}

	cases := map[string]func(*TransferCreateInput){
		"bad mode":      func(in *TransferCreateInput) { in.Mode = "teleport" },
		"empty key":     func(in *TransferCreateInput) { in.IdempotencyKey = "  " },
		"zero date":     func(in *TransferCreateInput) { in.EffectiveDate = time.Time{} },
		"single line":   func(in *TransferCreateInput) { in.Lines = in.Lines[:1] },
		"bad direction": func(in *TransferCreateInput) { in.Lines[0].Direction = "sideways" },
		"zero amount":   func(in *TransferCreateInput) { in.Lines[0].Amount = 0 },
		"empty ccy":     func(in *TransferCreateInput) { in.Lines[0].Currency = "" },
	}
	for name, mutate := range cases {
		in := good()
		mutate(&in)
		if errs := validateTransferCreateInput(in); len(errs) == 0 {
			t.Errorf("%s: expected violations, got none", name)
		}
	}
	if errs := validateTransferCreateInput(good()); len(errs) != 0 {
		t.Errorf("good input: expected no violations, got %v", errs)
	}
}

// TestTransferLinks_ContainsResourceHrefs pins the HATEOAS-lite
// contract: every created transfer carries navigable links for self,
// pos, transactions, and debts.
func TestTransferLinks_ContainsResourceHrefs(t *testing.T) {
	t.Parallel()
	txnID := uuid.NewString()

	links := transferLinks(txnID)

	for _, want := range []string{"self", "pos", "transactions", "debts"} {
		href, ok := links[want]
		if !ok || strings.TrimSpace(href) == "" {
			t.Errorf("links[%q] missing or empty: %v", want, links)
		}
	}
	if !strings.Contains(links["self"], txnID) {
		t.Errorf("self link %q does not contain transfer id", links["self"])
	}
}

// TestTransferActions_ReferenceExistingGETs pins that _actions only
// point at GETs that already exist (no new endpoints in this step).
func TestTransferActions_ReferenceExistingGETs(t *testing.T) {
	t.Parallel()
	actions := transferActions(uuid.NewString())
	if len(actions) == 0 {
		t.Fatal("expected non-empty _actions")
	}
	for _, a := range actions {
		if a.Method != "GET" {
			t.Errorf("action %q uses method %q, want GET", a.Name, a.Method)
		}
		if strings.TrimSpace(a.Href) == "" {
			t.Errorf("action %q has empty href", a.Name)
		}
	}
}
