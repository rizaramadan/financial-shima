package clock

import (
	"testing"
	"time"
)

func TestSystem_NowReturnsRecentTime(t *testing.T) {
	t.Parallel()
	got := System{}.Now()
	if got.IsZero() {
		t.Error("System.Now() returned zero time")
	}
	if d := time.Since(got); d > time.Second || d < -time.Second {
		t.Errorf("System.Now() returned %v (off by %v from now)", got, d)
	}
}

func TestFixed_NowIsConstantAcrossCalls(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	c := Fixed{T: when}
	if !c.Now().Equal(when) {
		t.Errorf("first call: got %v, want %v", c.Now(), when)
	}
	if !c.Now().Equal(when) {
		t.Errorf("second call differed; Fixed must be stable across calls")
	}
}
