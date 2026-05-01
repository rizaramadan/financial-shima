// Package idgen supplies an injectable source of opaque random IDs (session
// tokens, idempotency keys, etc.). Production wires Crypto; tests wire Fixed.
package idgen

import (
	"crypto/rand"
	"encoding/base64"
	"io"
)

// IDGen returns a fresh opaque ID on each call. The bytes are URL-safe and
// have at least 192 bits of entropy in the production wiring.
type IDGen interface {
	NewID() string
}

// Crypto reads from the OS CSPRNG (crypto/rand.Reader by default) and
// emits a 32-byte URL-safe base64-encoded string (43 characters before
// padding-stripping). Suitable for session tokens and OTP-issue idempotency.
type Crypto struct {
	// Reader is the entropy source. nil means use crypto/rand.Reader.
	Reader io.Reader
}

func (c Crypto) NewID() string {
	r := c.Reader
	if r == nil {
		r = rand.Reader
	}
	var buf [32]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		// crypto/rand.Reader does not error in practice. Falling back to a
		// panic is the right move: a degraded RNG is a security failure,
		// not a recoverable condition.
		panic("idgen: entropy source failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

// Fixed returns the configured Value on every call. Tests bind it to a
// known string so assertions don't have to grep random output.
type Fixed struct {
	Value string
}

func (f Fixed) NewID() string { return f.Value }
