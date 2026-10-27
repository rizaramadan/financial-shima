package balance

import (
	"testing"
	"time"
)

// TDD: bucket parsing / truncation / net drive GET /api/v1/recap.
// All pure: no time.Now, no DB.
func TestParseRecapBucket_Valid(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"week", "month", "year"} {
		b, err := ParseRecapBucket(s)
		if err != nil {
			t.Errorf("ParseRecapBucket(%q): %v", s, err)
		}
		if string(b) != s {
			t.Errorf("ParseRecapBucket(%q) = %q", s, b)
		}
	}
}

func TestParseRecapBucket_Invalid(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "day", "WEEK", "quarter"} {
		if _, err := ParseRecapBucket(s); err == nil {
			t.Errorf("ParseRecapBucket(%q): expected error, got nil", s)
		}
	}
}

func TestTruncateToBucket_Boundaries(t *testing.T) {
	t.Parallel()
	// Wednesday 2026-05-13 15:04 UTC.
	mid := time.Date(2026, 5, 13, 15, 4, 5, 0, time.UTC)
	if got := TruncateToBucket(mid, RecapWeek); !got.Equal(time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("week truncate = %v, want Monday 2026-05-11", got)
	}
	if got := TruncateToBucket(mid, RecapMonth); !got.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month truncate = %v, want 2026-05-01", got)
	}
	if got := TruncateToBucket(mid, RecapYear); !got.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("year truncate = %v, want 2026-01-01", got)
	}
	// Sunday belongs to the week starting the previous Monday.
	sun := time.Date(2026, 5, 17, 23, 59, 0, 0, time.UTC)
	if got := TruncateToBucket(sun, RecapWeek); !got.Equal(time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("sunday week truncate = %v, want Monday 2026-05-11", got)
	}
}

func TestRecapNet_InMinusOut(t *testing.T) {
	t.Parallel()
	if got := RecapNet(800_000, 300_000); got != 500_000 {
		t.Errorf("RecapNet = %d, want 500000", got)
	}
	if got := RecapNet(0, 50_000); got != -50_000 {
		t.Errorf("RecapNet all-out = %d, want -50000", got)
	}
}
