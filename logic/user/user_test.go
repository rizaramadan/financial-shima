package user

import "testing"

func TestSeeded_HasTwoCanonicalUsers(t *testing.T) {
	t.Parallel()
	got := Seeded()
	if len(got) != 2 {
		t.Fatalf("got %d users, want 2", len(got))
	}
	names := []string{got[0].DisplayName, got[1].DisplayName}
	wantSet := map[string]bool{"Riza": false, "Shima": false}
	for _, n := range names {
		if _, ok := wantSet[n]; !ok {
			t.Errorf("unexpected display name %q", n)
		}
		wantSet[n] = true
	}
	for n, present := range wantSet {
		if !present {
			t.Errorf("missing display name %q", n)
		}
	}
}

func TestSeeded_ReturnsFreshSlice(t *testing.T) {
	t.Parallel()
	a := Seeded()
	a[0].DisplayName = "MUTATED"
	b := Seeded()
	if b[0].DisplayName == "MUTATED" {
		t.Error("Seeded leaks shared slice state across calls")
	}
}

func TestFind_MatchesExactIdentifier(t *testing.T) {
	t.Parallel()
	users := Seeded()
	got, ok := Find("@shima", users)
	if !ok {
		t.Fatal("Find returned ok=false for known identifier")
	}
	if got.DisplayName != "Shima" {
		t.Errorf("got %q, want Shima", got.DisplayName)
	}
}

func TestFind_CaseInsensitive(t *testing.T) {
	t.Parallel()
	users := Seeded()
	for _, q := range []string{"@SHIMA", "Shima", "ShImA", "@shIMa"} {
		t.Run(q, func(t *testing.T) {
			got, ok := Find(q, users)
			if !ok || got.DisplayName != "Shima" {
				t.Errorf("Find(%q) = (%+v, %v), want Shima", q, got, ok)
			}
		})
	}
}

func TestFind_AcceptsBothWithAndWithoutAtPrefix(t *testing.T) {
	t.Parallel()
	users := Seeded()
	got1, ok1 := Find("@shima", users)
	got2, ok2 := Find("shima", users)
	if !ok1 || !ok2 || got1 != got2 {
		t.Errorf("@shima and shima should match the same user, got (%+v,%v) and (%+v,%v)",
			got1, ok1, got2, ok2)
	}
}

func TestFind_ReturnsNotFoundOnUnknownIdentifier(t *testing.T) {
	t.Parallel()
	users := Seeded()
	for _, q := range []string{"", "   ", "@", "@unknown", "stranger"} {
		t.Run(q, func(t *testing.T) {
			_, ok := Find(q, users)
			if ok {
				t.Errorf("Find(%q) = ok=true, want false", q)
			}
		})
	}
}

func TestFind_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	users := Seeded()
	got, ok := Find("  @shima  ", users)
	if !ok || got.DisplayName != "Shima" {
		t.Errorf("trimmed input did not match: got (%+v, %v)", got, ok)
	}
}
