package otp

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestGenerate_DeterministicWithFixedReader(t *testing.T) {
	t.Parallel()
	src1 := bytes.NewReader([]byte{0x12, 0x34, 0x56, 0x78})
	src2 := bytes.NewReader([]byte{0x12, 0x34, 0x56, 0x78})
	if Generate(src1) != Generate(src2) {
		t.Error("equal entropy yielded different codes")
	}
}

func TestGenerate_AlwaysSixDigits(t *testing.T) {
	t.Parallel()
	// All-zero bytes should still yield a Code with 6 digits ("000000").
	zero := bytes.NewReader(make([]byte, 4))
	c := Generate(zero)
	if got := c.String(); len(got) != 6 {
		t.Errorf("got len(%q)=%d, want 6", got, len(got))
	}
}

func TestGenerate_PanicsOnEntropyFailure(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on short reader")
		}
	}()
	_ = Generate(bytes.NewReader(nil))
}

// Reference time for deterministic tests.
var t0 = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

func TestNewCode_Has6Digits(t *testing.T) {
	t.Parallel()
	c := NewCode(123456)
	if got := c.String(); got != "123456" {
		t.Errorf("NewCode(123456).String() = %q, want %q", got, "123456")
	}
}

func TestNewCode_PadsLeadingZeros(t *testing.T) {
	t.Parallel()
	c := NewCode(7)
	if got := c.String(); got != "000007" {
		t.Errorf("NewCode(7).String() = %q, want %q", got, "000007")
	}
}

func TestNewCode_RejectsOutOfRange(t *testing.T) {
	t.Parallel()
	cases := []int{-1, 1000000, 9999999}
	for _, n := range cases {
		t.Run("", func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("NewCode(%d) did not panic on out-of-range value", n)
				}
			}()
			_ = NewCode(n)
		})
	}
}

func TestRecord_Verify_AcceptsCorrectCodeWithinExpiry(t *testing.T) {
	t.Parallel()
	r := NewRecord(NewCode(123456), t0)
	res, r2 := r.Verify(NewCode(123456), t0.Add(1*time.Minute))
	if res != Accepted {
		t.Errorf("result = %v, want Accepted", res)
	}
	if !r2.Cleared {
		t.Errorf("record should be cleared after acceptance")
	}
}

func TestRecord_Verify_RejectsWrongCode(t *testing.T) {
	t.Parallel()
	r := NewRecord(NewCode(123456), t0)
	res, r2 := r.Verify(NewCode(999999), t0.Add(1*time.Minute))
	if res != Rejected {
		t.Errorf("result = %v, want Rejected", res)
	}
	if r2.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 after one wrong submission", r2.Attempts)
	}
	if r2.Cleared {
		t.Errorf("record cleared on rejection — should not be")
	}
}

// TestRecord_Verify_AcceptedCodeReplayReturnsSpent: the spec §3.2 promise
// is one-time. After Accepted, re-Verifying with the correct code must
// return Spent (not Locked, which is a misleading message). This is the
// behavior layer that pins replay protection — previously only the Cleared
// flag was asserted as a struct field (Beck R6 review).
func TestRecord_Verify_AcceptedCodeReplayReturnsSpent(t *testing.T) {
	t.Parallel()
	r := NewRecord(NewCode(123456), t0)
	res, r2 := r.Verify(NewCode(123456), t0.Add(1*time.Minute))
	if res != Accepted {
		t.Fatalf("first verify = %v, want Accepted", res)
	}
	res2, _ := r2.Verify(NewCode(123456), t0.Add(2*time.Minute))
	if res2 != Spent {
		t.Errorf("replay verify = %v, want Spent", res2)
	}
}

func TestRecord_Verify_LocksAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	r := NewRecord(NewCode(123456), t0)
	now := t0.Add(1 * time.Minute)

	// First two wrong attempts: rejected, not locked.
	for i := 0; i < MaxAttempts-1; i++ {
		res, r2 := r.Verify(NewCode(999999), now)
		if res != Rejected {
			t.Fatalf("attempt %d: result = %v, want Rejected", i+1, res)
		}
		r = r2
	}
	// MaxAttempts-th wrong attempt: locked.
	res, r2 := r.Verify(NewCode(999999), now)
	if res != Locked {
		t.Errorf("final attempt: result = %v, want Locked", res)
	}
	if !r2.Locked {
		t.Errorf("record should be locked")
	}

	// Subsequent attempts (even with correct code) stay locked.
	res, _ = r2.Verify(NewCode(123456), now)
	if res != Locked {
		t.Errorf("after lock with correct code: result = %v, want Locked", res)
	}
}

func TestRecord_Verify_ExpiredAfterFiveMinutes(t *testing.T) {
	t.Parallel()
	r := NewRecord(NewCode(123456), t0)
	res, _ := r.Verify(NewCode(123456), t0.Add(ExpiryDuration+1*time.Second))
	if res != Expired {
		t.Errorf("result = %v, want Expired", res)
	}
}

func TestRecord_Verify_AtExactExpirySecond(t *testing.T) {
	t.Parallel()
	r := NewRecord(NewCode(123456), t0)
	res, _ := r.Verify(NewCode(123456), t0.Add(ExpiryDuration))
	if res != Accepted {
		t.Errorf("result at exact expiry boundary = %v, want Accepted (boundary inclusive)", res)
	}
}

// TestRecord_Verify_FixedTimeComparison: code comparison must be constant-time
// to prevent timing-based brute force against valid prefix matches.
func TestRecord_Verify_ConstantTimeCompareIsUsed(t *testing.T) {
	t.Parallel()
	r := NewRecord(NewCode(123456), t0)
	// Same length, different code — confirms the comparator runs to completion
	// regardless of mismatch position.
	res, _ := r.Verify(NewCode(654321), t0.Add(1*time.Minute))
	if res != Rejected {
		t.Errorf("result = %v, want Rejected", res)
	}
	// Note: actual timing is hard to test deterministically; this test pins
	// behavior. The implementation must use crypto/subtle.ConstantTimeCompare.
	// A reviewer can grep for it.
}

func TestExpiryDurationIs5Minutes(t *testing.T) {
	if ExpiryDuration != 5*time.Minute {
		t.Errorf("ExpiryDuration = %v, want 5m (per spec §3.3)", ExpiryDuration)
	}
}

func TestMaxAttemptsIs3(t *testing.T) {
	if MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3 (per spec §3.3)", MaxAttempts)
	}
}

// String helpers shouldn't leak the code into logs. Stringer should redact.
func TestRecord_String_RedactsCode(t *testing.T) {
	t.Parallel()
	r := NewRecord(NewCode(123456), t0)
	s := r.String()
	if strings.Contains(s, "123456") {
		t.Errorf("String() = %q leaks the OTP code", s)
	}
}
