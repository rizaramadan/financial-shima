// Package otp encodes the spec §3.2-§3.3 rules for one-time passcodes.
// It is pure: no time.Now(), no rand, no globals. Callers inject `now` per
// call, and the Record value is treated immutably (Verify returns a new
// Record reflecting the post-attempt state).
package otp

import (
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// Generate samples a uniformly random Code in [0, 999999] from r.
// Production binds r to crypto/rand.Reader; tests bind to bytes.NewReader.
// Panics on entropy failure (same rationale as idgen.Crypto).
func Generate(r io.Reader) Code {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		panic("otp.Generate: entropy source failed: " + err.Error())
	}
	// uint32 mod 1_000_000 introduces a tiny modulo bias (< 1 in 4e9 deviation
	// across the 6-digit space) — negligible for 5-minute one-time codes
	// against any adversary not running compute against ExpiryDuration.
	n := int(binary.BigEndian.Uint32(buf[:]) % 1_000_000)
	return NewCode(n)
}

// Code is a 6-digit decimal one-time passcode. Stored as fixed-width string
// so leading zeros survive round-trips through HTTP forms / databases.
type Code struct {
	digits string // length 6, "0"-"9" only
}

// NewCode wraps an integer in [0, 999999] as a zero-padded 6-digit Code.
// Panics on out-of-range input — callers should generate values via the
// designated source (Issue, below) which guarantees the range.
func NewCode(n int) Code {
	if n < 0 || n > 999999 {
		panic(fmt.Sprintf("otp.NewCode: %d out of range [0, 999999]", n))
	}
	return Code{digits: fmt.Sprintf("%06d", n)}
}

// String returns the 6-digit representation. Useful for sending to the
// assistant and for form comparison; do NOT log this value.
func (c Code) String() string { return c.digits }

const (
	// ExpiryDuration is how long an issued OTP remains valid (spec §3.3).
	// Boundary is inclusive: a code submitted exactly at issued+5m succeeds.
	ExpiryDuration = 5 * time.Minute

	// MaxAttempts is how many wrong submissions lock an OTP (spec §3.3).
	// On the MaxAttempts-th wrong submission the record locks; subsequent
	// attempts return Locked even with the correct code.
	MaxAttempts = 3

	// ResendCooldown is the spec §3.3 floor between resend requests for the
	// same identifier. Enforced by the issuer, not by Record.
	ResendCooldown = 60 * time.Second
)

// Result enumerates the user-visible outcomes of Verify.
type Result int

const (
	Accepted Result = iota
	Rejected
	Locked  // too many wrong attempts
	Expired // past ExpiryDuration since IssuedAt
	Spent   // Verify already Accepted on this Record (replay)
)

func (r Result) String() string {
	switch r {
	case Accepted:
		return "Accepted"
	case Rejected:
		return "Rejected"
	case Locked:
		return "Locked"
	case Expired:
		return "Expired"
	case Spent:
		return "Spent"
	default:
		return fmt.Sprintf("Result(%d)", int(r))
	}
}

// Record is the server-side persistence shape for an issued OTP. Append-only
// in spirit: Verify returns a new Record rather than mutating the receiver,
// so callers can store-or-discard atomically.
type Record struct {
	Code     Code
	IssuedAt time.Time
	Attempts int
	Locked   bool
	Cleared  bool // true after a successful Verify — the OTP is now spent.
}

// NewRecord constructs a fresh Record for the given Code at issuance time.
func NewRecord(c Code, issuedAt time.Time) Record {
	return Record{Code: c, IssuedAt: issuedAt}
}

// Verify runs the spec §3.2 rules:
//   - if locked or cleared → Locked
//   - if now > IssuedAt + ExpiryDuration → Expired
//   - constant-time compare of submitted vs. stored Code
//   - on match: Cleared=true, Result=Accepted
//   - on miss: Attempts++; if Attempts >= MaxAttempts → Locked, else Rejected
//
// The returned Record reflects the post-attempt state and supersedes the
// receiver in storage. The receiver is not modified.
func (r Record) Verify(submitted Code, now time.Time) (Result, Record) {
	if r.Cleared {
		return Spent, r
	}
	if r.Locked {
		return Locked, r
	}
	if now.Sub(r.IssuedAt) > ExpiryDuration {
		return Expired, r
	}

	// Constant-time compare so an attacker cannot derive the code from
	// per-byte timing — strings are short but the principle holds.
	if subtle.ConstantTimeCompare([]byte(r.Code.digits), []byte(submitted.digits)) == 1 {
		out := r
		out.Cleared = true
		return Accepted, out
	}

	out := r
	out.Attempts++
	if out.Attempts >= MaxAttempts {
		out.Locked = true
		return Locked, out
	}
	return Rejected, out
}

// String redacts the Code so the Record can be logged. Spec §10.1 mood:
// "Money is integer cents" — the analogous discipline here is "OTP codes
// never appear in logs."
func (r Record) String() string {
	return fmt.Sprintf("otp.Record{IssuedAt:%s Attempts:%d Locked:%t Cleared:%t Code:[redacted]}",
		r.IssuedAt.Format(time.RFC3339), r.Attempts, r.Locked, r.Cleared)
}
