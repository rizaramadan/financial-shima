package idgen

import (
	"bytes"
	"strings"
	"testing"
)

func TestCrypto_NewID_Returns43CharURLSafeString(t *testing.T) {
	t.Parallel()
	id := Crypto{}.NewID()
	if got, want := len(id), 43; got != want {
		t.Errorf("len = %d, want %d (32 bytes base64-rawurl-encoded)", got, want)
	}
	for _, r := range id {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			t.Errorf("char %q not URL-safe", r)
		}
	}
}

func TestCrypto_NewID_DistinctOnRepeat(t *testing.T) {
	t.Parallel()
	c := Crypto{}
	a, b := c.NewID(), c.NewID()
	if a == b {
		t.Errorf("two calls returned the same ID %q (entropy source broken?)", a)
	}
}

func TestCrypto_NewID_DeterministicWithFixedReader(t *testing.T) {
	t.Parallel()
	// Same reader bytes ⇒ same encoded string.
	c1 := Crypto{Reader: bytes.NewReader(make([]byte, 64))}
	c2 := Crypto{Reader: bytes.NewReader(make([]byte, 64))}
	if c1.NewID() != c2.NewID() {
		t.Error("equal entropy bytes produced different IDs")
	}
}

func TestCrypto_NewID_PanicsOnEntropyFailure(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on short reader")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "entropy") {
			t.Errorf("panic = %v, want string mentioning 'entropy'", r)
		}
	}()
	// Empty reader returns io.EOF on first read.
	_ = Crypto{Reader: bytes.NewReader(nil)}.NewID()
}

func TestFixed_NewID_StableValue(t *testing.T) {
	t.Parallel()
	f := Fixed{Value: "test-token-abc"}
	if got := f.NewID(); got != "test-token-abc" {
		t.Errorf("got %q, want test-token-abc", got)
	}
	if f.NewID() != f.NewID() {
		t.Error("Fixed must be stable across calls")
	}
}
