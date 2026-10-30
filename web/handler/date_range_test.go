package handler

import (
	"testing"
	"time"
)

func TestReversedRangeError(t *testing.T) {
	t.Parallel()
	mk := func(s string) time.Time {
		tm, err := time.Parse(dateLayout, s)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	if got := reversedRangeError(mk("2026-12-01"), mk("2026-11-30")); got == "" {
		t.Error("reversed range: want an error message, got empty")
	}
	if got := reversedRangeError(mk("2026-11-30"), mk("2026-11-30")); got != "" {
		t.Errorf("equal dates: want no error, got %q", got)
	}
	if got := reversedRangeError(mk("2026-11-01"), mk("2026-11-30")); got != "" {
		t.Errorf("ordered range: want no error, got %q", got)
	}
}
