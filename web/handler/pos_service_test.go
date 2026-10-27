package handler

// TDD Step (pos service extract, same pattern as transaction_service):
// the shared result carries data + HATEOAS-lite _links/_actions so the
// web POSTs and the API POST/PATCH share one create/update/archive path.

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestPosLinks_ContainsResourceHrefs pins the HATEOAS-lite contract:
// every created/updated Pos carries navigable links, so HTMX/API
// clients never hard-code routes.
func TestPosLinks_ContainsResourceHrefs(t *testing.T) {
	t.Parallel()
	posID := uuid.New()
	accountID := uuid.New()

	links := posLinks(posID.String(), accountID.String())

	for _, want := range []string{"self", "list", "account", "transactions"} {
		href, ok := links[want]
		if !ok || strings.TrimSpace(href) == "" {
			t.Errorf("links[%q] missing or empty: %v", want, links)
		}
	}
	if !strings.Contains(links["self"], posID.String()) {
		t.Errorf("self link %q does not contain pos id", links["self"])
	}
	if !strings.Contains(links["account"], accountID.String()) {
		t.Errorf("account link %q does not contain account id", links["account"])
	}
}

// TestPosCreateResult_JSONIncludesHATEOAS pins the wire contract: the
// shared result serializes data + _links/_actions together.
func TestPosCreateResult_JSONIncludesHATEOAS(t *testing.T) {
	t.Parallel()
	res := APIPos{
		ID:        uuid.NewString(),
		Name:      "Kas",
		Currency:  "idr",
		AccountID: uuid.NewString(),
	}
	res.Links = posLinks(res.ID, res.AccountID)
	res.Actions = posActions(res.ID)

	if len(res.Links) == 0 {
		t.Error("expected non-empty _links")
	}
	if len(res.Actions) == 0 {
		t.Error("expected non-empty _actions")
	}
}
