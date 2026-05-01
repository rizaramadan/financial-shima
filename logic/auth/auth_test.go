package auth

import (
	"bytes"
	"testing"
	"time"

	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/otp"
	"github.com/rizaramadan/financial-shima/logic/user"
)

var t0 = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

// freshAuth returns an Auth with a deterministic Clock + entropy and a
// 256-byte source ring (enough for many OTPs and session tokens).
func freshAuth(t *testing.T, when time.Time) *Auth {
	t.Helper()
	src := bytes.NewReader(make([]byte, 1024))
	for i := range make([]byte, 1024) {
		_ = i
	}
	// Initialise the buffer with a known pattern so successive Generate calls
	// yield distinct codes deterministically.
	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = byte(i)
	}
	src = bytes.NewReader(buf)
	return New(user.Seeded(), clock.Fixed{T: when}, src, idgen.Fixed{Value: "tok-test"})
}

func TestIssue_KnownUser_ReturnsIssuedAndCode(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	o := a.Issue("@shima")
	if o.Result != Issued {
		t.Fatalf("result = %v, want Issued", o.Result)
	}
	if o.User.DisplayName != "Shima" {
		t.Errorf("user = %+v, want Shima", o.User)
	}
	if o.Code.String() == "" {
		t.Error("code is empty")
	}
}

func TestIssue_UnknownUser_ReturnsUserNotFound(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	o := a.Issue("@stranger")
	if o.Result != UserNotFound {
		t.Errorf("result = %v, want UserNotFound", o.Result)
	}
}

func TestIssue_TwiceWithinCooldown_ReturnsCooldownActive(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	first := a.Issue("@shima")
	if first.Result != Issued {
		t.Fatalf("first = %v", first.Result)
	}
	// Move clock forward less than ResendCooldown.
	a.Clock = clock.Fixed{T: t0.Add(otp.ResendCooldown - 1*time.Second)}
	second := a.Issue("@shima")
	if second.Result != CooldownActive {
		t.Errorf("second = %v, want CooldownActive", second.Result)
	}
}

func TestIssue_AfterCooldown_AllowsNewOTP(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	_ = a.Issue("@shima")
	a.Clock = clock.Fixed{T: t0.Add(otp.ResendCooldown + 1*time.Second)}
	second := a.Issue("@shima")
	if second.Result != Issued {
		t.Errorf("second = %v, want Issued (cooldown elapsed)", second.Result)
	}
}

func TestVerify_CorrectCode_ReturnsVerifiedWithSession(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	out := a.Issue("@shima")
	if out.Result != Issued {
		t.Fatal("issue failed")
	}

	v := a.Verify("@shima", out.Code)
	if v.Result != Verified {
		t.Fatalf("verify = %v, want Verified", v.Result)
	}
	if v.Session.Token == "" {
		t.Error("session token empty")
	}
	if v.Session.UserID != "shima" {
		t.Errorf("session.UserID = %q", v.Session.UserID)
	}
	if !v.Session.ExpiresAt.Equal(t0.Add(SessionLifetime)) {
		t.Errorf("session expires = %v, want %v", v.Session.ExpiresAt, t0.Add(SessionLifetime))
	}

	u, ok := a.ResolveSession(v.Session.Token)
	if !ok || u.DisplayName != "Shima" {
		t.Errorf("ResolveSession = (%+v, %v)", u, ok)
	}
}

func TestVerify_WrongCode_Rejected(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	_ = a.Issue("@shima")
	v := a.Verify("@shima", otp.NewCode(0))
	if v.Result != Rejected {
		t.Errorf("verify = %v, want Rejected", v.Result)
	}
}

func TestVerify_NoOTPIssued_ReturnsNoOTP(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	v := a.Verify("@shima", otp.NewCode(123456))
	if v.Result != NoOTP {
		t.Errorf("verify = %v, want NoOTP", v.Result)
	}
}

func TestVerify_UnknownIdentifier_ReturnsNoOTP(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	v := a.Verify("@stranger", otp.NewCode(123456))
	if v.Result != NoOTP {
		t.Errorf("verify = %v, want NoOTP (do not disclose unknown user)", v.Result)
	}
}

func TestVerify_LocksAfterMaxWrongAttempts(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	out := a.Issue("@shima")

	// Three wrong attempts. Final one should return Locked.
	wrong := otp.NewCode(0)
	if out.Code.String() == "000000" {
		wrong = otp.NewCode(1) // pick a guaranteed-distinct value
	}
	for i := 1; i < otp.MaxAttempts; i++ {
		v := a.Verify("@shima", wrong)
		if v.Result != Rejected {
			t.Fatalf("attempt %d = %v, want Rejected", i, v.Result)
		}
	}
	v := a.Verify("@shima", wrong)
	if v.Result != Locked {
		t.Fatalf("final attempt = %v, want Locked", v.Result)
	}
	// Even the correct code now bounces.
	v = a.Verify("@shima", out.Code)
	if v.Result != Locked {
		t.Errorf("post-lock with correct code = %v, want Locked", v.Result)
	}
}

func TestVerify_AfterExpiry_ReturnsExpired(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	out := a.Issue("@shima")
	a.Clock = clock.Fixed{T: t0.Add(otp.ExpiryDuration + 1*time.Second)}
	v := a.Verify("@shima", out.Code)
	if v.Result != Expired {
		t.Errorf("verify = %v, want Expired", v.Result)
	}
}

func TestResolveSession_ExpiredTokenDeletedAndRejected(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	out := a.Issue("@shima")
	v := a.Verify("@shima", out.Code)

	a.Clock = clock.Fixed{T: v.Session.ExpiresAt.Add(1 * time.Second)}
	if _, ok := a.ResolveSession(v.Session.Token); ok {
		t.Error("expired session still resolved")
	}
}

func TestResolveSession_UnknownTokenRejected(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	if _, ok := a.ResolveSession("not-a-real-token"); ok {
		t.Error("garbage token resolved")
	}
}

func TestLogout_RevokesSession(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)
	out := a.Issue("@shima")
	v := a.Verify("@shima", out.Code)
	a.Logout(v.Session.Token)
	if _, ok := a.ResolveSession(v.Session.Token); ok {
		t.Error("logged-out session still resolves")
	}
}

// TestAuth_ConcurrentIssue_NoMapRaceAndAtMostOneIssued exercises two
// orthogonal guarantees:
//
//  1. The mutex protects the otps map from concurrent read+write data
//     races. Detected by `-race` (when cgo is available); without it, the
//     test still asserts a property the mutex provides.
//  2. AT MOST ONE Issued result. The cooldown branch is what serializes
//     the result count once the first write commits — but only if the
//     mutex prevents the read-then-write window where multiple goroutines
//     could each see "no prev record" and all write. Without the mutex
//     the assertion would fire when read-before-any-write actually races
//     across multiple goroutines.
//
// Beck Phase-2-5 R4 noted the previous "exactly 1" framing was overly
// confident; this version is honest about which layer guarantees what.
func TestAuth_ConcurrentIssue_NoMapRaceAndAtMostOneIssued(t *testing.T) {
	t.Parallel()
	a := freshAuth(t, t0)

	const N = 16
	results := make(chan IssueResult, N)
	for i := 0; i < N; i++ {
		go func() { results <- a.Issue("@shima").Result }()
	}
	issued, cooldowns, other := 0, 0, 0
	for i := 0; i < N; i++ {
		switch r := <-results; r {
		case Issued:
			issued++
		case CooldownActive:
			cooldowns++
		default:
			other++
			t.Errorf("unexpected concurrent result: %v", r)
		}
	}
	if issued > 1 {
		t.Errorf("got %d Issued, want at most 1 (mutex must serialize the read-then-write window)", issued)
	}
	if issued+cooldowns+other != N {
		t.Errorf("result counts don't sum: issued=%d cooldowns=%d other=%d N=%d",
			issued, cooldowns, other, N)
	}
}

// TestVerify_TwoSessions_GetDistinctTokens pins the spec §3.4 contract that
// session tokens are opaque random strings — two successful verifies must
// not produce the same token, even when the test fixture's IDGen is
// deterministic. Beck Phase-2-5 R10: previously the suite used Fixed which
// would mask a token-collision bug.
func TestVerify_TwoSessions_GetDistinctTokens(t *testing.T) {
	t.Parallel()
	src := bytes.NewReader(make([]byte, 1024))
	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = byte(i + 1)
	}
	src = bytes.NewReader(buf)
	a := New(user.Seeded(), clock.Fixed{T: t0}, src, &idgen.Counter{Prefix: "tok"})

	o1 := a.Issue("@shima")
	v1 := a.Verify("@shima", o1.Code)
	if v1.Result != Verified {
		t.Fatalf("first verify: %v", v1.Result)
	}
	// Move clock past cooldown so the second Issue runs.
	a.Clock = clock.Fixed{T: t0.Add(otp.ResendCooldown + time.Second)}
	o2 := a.Issue("@riza_ramadan")
	v2 := a.Verify("@riza_ramadan", o2.Code)
	if v2.Result != Verified {
		t.Fatalf("second verify: %v", v2.Result)
	}
	if v1.Session.Token == v2.Session.Token {
		t.Errorf("session tokens collided: %q == %q (low-entropy IDGen would silently hijack sessions)",
			v1.Session.Token, v2.Session.Token)
	}
}

func TestNew_PanicsOnMissingDependencies(t *testing.T) {
	t.Parallel()
	users := user.Seeded()
	cases := []struct {
		name string
		f    func()
	}{
		{"nil Clock", func() {
			_ = New(users, nil, bytes.NewReader([]byte{0, 0, 0, 0}), idgen.Fixed{Value: "x"})
		}},
		{"nil Source", func() {
			_ = New(users, clock.Fixed{T: t0}, nil, idgen.Fixed{Value: "x"})
		}},
		{"nil IDGen", func() {
			_ = New(users, clock.Fixed{T: t0}, bytes.NewReader([]byte{0, 0, 0, 0}), nil)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("%s: did not panic", c.name)
				}
			}()
			c.f()
		})
	}
}
