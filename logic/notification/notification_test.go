package notification

import (
	"sort"
	"testing"

	"github.com/rizaramadan/financial-shima/logic/user"
)

func users() []user.User {
	return []user.User{
		{ID: "riza", DisplayName: "Riza", TelegramIdentifier: "@riza"},
		{ID: "shima", DisplayName: "Shima", TelegramIdentifier: "@shima"},
	}
}

func ids(us []user.User) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.ID)
	}
	sort.Strings(out)
	return out
}

func TestRecipientsFor_Seed_NoNotifications(t *testing.T) {
	t.Parallel()
	got := RecipientsFor(SourceSeed, "riza", users())
	if len(got) != 0 {
		t.Errorf("seed should produce no recipients, got %v", ids(got))
	}
}

func TestRecipientsFor_Web_ExcludesCreator(t *testing.T) {
	t.Parallel()
	got := RecipientsFor(SourceWeb, "riza", users())
	want := []string{"shima"}
	if g := ids(got); !equal(g, want) {
		t.Errorf("got %v, want %v", g, want)
	}
}

func TestRecipientsFor_API_NotifiesAll(t *testing.T) {
	t.Parallel()
	got := RecipientsFor(SourceAPI, "", users())
	want := []string{"riza", "shima"}
	if g := ids(got); !equal(g, want) {
		t.Errorf("got %v, want %v", g, want)
	}
}

func TestRecipientsFor_Web_EmptyCreatedBy_NotifiesAll(t *testing.T) {
	// Defensive: web should always carry created_by in production, but if it
	// doesn't, fall through to "notify everyone" rather than skip silently.
	t.Parallel()
	got := RecipientsFor(SourceWeb, "", users())
	if len(got) != 2 {
		t.Errorf("web with empty createdBy should still notify all, got %d", len(got))
	}
}

func TestRecipientsFor_NewSliceEachCall(t *testing.T) {
	t.Parallel()
	a := RecipientsFor(SourceAPI, "", users())
	if len(a) > 0 {
		a[0].DisplayName = "MUTATED"
	}
	b := RecipientsFor(SourceAPI, "", users())
	if len(b) > 0 && b[0].DisplayName == "MUTATED" {
		t.Error("RecipientsFor leaks shared state across calls")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
