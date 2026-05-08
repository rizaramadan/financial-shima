// Package auth coordinates the spec §3.2 login flow: lookup a user by their
// Telegram identifier, issue an OTP, verify the OTP, and mint a session.
//
// Stores live in-memory in this package per Phase 2 scope ("in-memory store,
// stubbed assistant"). The interface is shaped so a future Phase swaps in a
// DB-backed store without changing handlers.
package auth

import (
	"io"
	"sync"
	"time"

	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/otp"
	"github.com/rizaramadan/financial-shima/logic/user"
)

const SessionLifetime = 7 * 24 * time.Hour // spec §3.4

// IssueResult enumerates the outcomes Issue exposes to the caller.
type IssueResult int

const (
	Issued       IssueResult = iota // OTP minted; caller now sends it to the assistant
	UserNotFound                    // identifier did not match a seeded user
	CooldownActive                  // last OTP issued < ResendCooldown ago
)

func (r IssueResult) String() string {
	switch r {
	case Issued:
		return "Issued"
	case UserNotFound:
		return "UserNotFound"
	case CooldownActive:
		return "CooldownActive"
	default:
		return "Result(?)"
	}
}

type IssueOutcome struct {
	Result IssueResult
	User   user.User // populated when Result == Issued or CooldownActive
	Code   otp.Code  // populated only when Result == Issued (caller hands to assistant)
}

// VerifyResult enumerates the outcomes Verify exposes.
type VerifyResult int

const (
	Verified VerifyResult = iota
	NoOTP                 // no record exists for the identifier
	Locked
	Expired
	Rejected
	Spent // OTP was already accepted; this is a replay (spec §3.2 one-time-use)
)

func (r VerifyResult) String() string {
	switch r {
	case Verified:
		return "Verified"
	case NoOTP:
		return "NoOTP"
	case Locked:
		return "Locked"
	case Expired:
		return "Expired"
	case Rejected:
		return "Rejected"
	case Spent:
		return "Spent"
	default:
		return "Result(?)"
	}
}

type VerifyOutcome struct {
	Result  VerifyResult
	Session Session // populated only when Result == Verified
}

// Session is what callers receive on Verified. The Token is the cookie value.
type Session struct {
	Token     string
	UserID    string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Auth wires Users + Clock + entropy + token source. All fields except mu
// are constructor inputs; nil values for Clock/Source/IDGen panic on use,
// surfacing config errors at boot rather than at first request.
type Auth struct {
	Users  []user.User
	Clock  clock.Clock
	Source io.Reader   // randomness for OTP digits
	IDGen  idgen.IDGen // session token source

	mu       sync.Mutex
	otps     map[string]otp.Record // user.ID → record
	sessions map[string]Session    // token → session
}

// New constructs a ready Auth. It panics on missing dependencies — a Phase 2
// failure here is a deploy bug, not a runtime condition worth recovering.
func New(users []user.User, c clock.Clock, src io.Reader, ig idgen.IDGen) *Auth {
	if c == nil {
		panic("auth.New: nil Clock")
	}
	if src == nil {
		panic("auth.New: nil Source")
	}
	if ig == nil {
		panic("auth.New: nil IDGen")
	}
	return &Auth{
		Users:    users,
		Clock:    c,
		Source:   src,
		IDGen:    ig,
		otps:     map[string]otp.Record{},
		sessions: map[string]Session{},
	}
}

// Issue runs the §3.2 step 2-4 flow. It does NOT call the assistant — that's
// I/O — and instead returns the Code so the handler can hand it off and
// surface assistant errors back to the user.
func (a *Auth) Issue(identifier string) IssueOutcome {
	u, ok := user.Find(identifier, a.Users)
	if !ok {
		return IssueOutcome{Result: UserNotFound}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.Clock.Now()
	if prev, exists := a.otps[u.ID]; exists {
		// Cooldown applies even after a Locked record so a brute-forcer who
		// burned attempts can't immediately request a fresh code (Skeet R6
		// review). Successful (Cleared) records bypass — the user just
		// signed in, follow-up flows shouldn't pay the wait.
		if !prev.Cleared && now.Sub(prev.IssuedAt) < otp.ResendCooldown {
			return IssueOutcome{Result: CooldownActive, User: u}
		}
	}

	code := otp.Generate(a.Source)
	a.otps[u.ID] = otp.NewRecord(code, now)
	return IssueOutcome{Result: Issued, User: u, Code: code}
}

// Verify runs the §3.2 step 6 flow + mints a Session on Accepted.
func (a *Auth) Verify(identifier string, submitted otp.Code) VerifyOutcome {
	u, ok := user.Find(identifier, a.Users)
	if !ok {
		// Same surface as NoOTP — don't disclose which side missed.
		return VerifyOutcome{Result: NoOTP}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	rec, exists := a.otps[u.ID]
	if !exists {
		return VerifyOutcome{Result: NoOTP}
	}

	now := a.Clock.Now()
	res, post := rec.Verify(submitted, now)
	a.otps[u.ID] = post

	switch res {
	case otp.Accepted:
		s := Session{
			Token:     a.IDGen.NewID(),
			UserID:    u.ID,
			IssuedAt:  now,
			ExpiresAt: now.Add(SessionLifetime),
		}
		a.sessions[s.Token] = s
		return VerifyOutcome{Result: Verified, Session: s}
	case otp.Locked:
		return VerifyOutcome{Result: Locked}
	case otp.Expired:
		return VerifyOutcome{Result: Expired}
	case otp.Rejected:
		return VerifyOutcome{Result: Rejected}
	case otp.Spent:
		return VerifyOutcome{Result: Spent}
	}
	// otp.Verify returns one of the five cases above; fall-through is a bug.
	return VerifyOutcome{Result: Rejected}
}

// MintSession creates a session for u and stores it. It bypasses OTP — the
// caller is responsible for whatever credential check authorizes this (e.g.
// the LOGIN_PASSWORD env-var check in the login handler).
func (a *Auth) MintSession(u user.User) Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.Clock.Now()
	s := Session{
		Token:     a.IDGen.NewID(),
		UserID:    u.ID,
		IssuedAt:  now,
		ExpiresAt: now.Add(SessionLifetime),
	}
	a.sessions[s.Token] = s
	return s
}

// ResolveSession returns the user attached to a session token if it exists
// and has not expired. This is what middleware calls on every request.
func (a *Auth) ResolveSession(token string) (user.User, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	s, ok := a.sessions[token]
	if !ok {
		return user.User{}, false
	}
	if !a.Clock.Now().Before(s.ExpiresAt) {
		delete(a.sessions, token)
		return user.User{}, false
	}
	for _, u := range a.Users {
		if u.ID == s.UserID {
			return u, true
		}
	}
	// Session points to a user no longer in the seed — reject.
	delete(a.sessions, token)
	return user.User{}, false
}

// Logout removes the session record so the cookie value becomes inert
// even before its TTL expires (spec §3.4).
func (a *Auth) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}
