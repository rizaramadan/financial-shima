package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/otp"
)

// otpExpiryPlusOne is a small helper so tests don't import otp inline.
func otpExpiryPlusOne() time.Duration { return otp.ExpiryDuration + time.Second }

// issueOTP drives auth.Issue directly so verify tests don't depend on the
// /login handler (which is now password-based and doesn't mint OTPs).
func issueOTP(t *testing.T, a *auth.Auth, identifier string) string {
	t.Helper()
	out := a.Issue(identifier)
	if out.Result != auth.Issued {
		t.Fatalf("auth.Issue(%q) = %v, want Issued", identifier, out.Result)
	}
	return out.Code.String()
}

func TestVerifyGet_RendersFormWithHiddenIdentifier(t *testing.T) {
	t.Parallel()
	e, _, _ := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/verify?id=%40shima", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="identifier"`) {
		t.Error("missing hidden identifier input")
	}
	if !strings.Contains(body, `value="@shima"`) {
		t.Errorf("identifier value not echoed; body:\n%s", body)
	}
	if !strings.Contains(body, `name="code"`) {
		t.Error("missing code input")
	}
}

func TestVerifyGet_NoIdentifier_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e, _, _ := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if w.Header().Get("Location") != "/login" {
		t.Errorf("Location = %q, want /login", w.Header().Get("Location"))
	}
}

func TestVerifyPost_CorrectCode_SetsSessionCookieAndRedirects(t *testing.T) {
	t.Parallel()
	e, a, _ := testServer(t)

	code := issueOTP(t, a, "@shima")

	verifyForm := url.Values{
		"identifier": {"@shima"},
		"code":       {code},
	}
	verifyReq := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(verifyForm.Encode()))
	verifyReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, verifyReq)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if w.Header().Get("Location") != "/" {
		t.Errorf("Location = %q, want /", w.Header().Get("Location"))
	}

	// Session cookie present, HttpOnly, Lax, Path=/.
	cookies := w.Result().Cookies()
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			session = c
			break
		}
	}
	if session == nil {
		t.Fatal("session cookie not set")
	}
	if !session.HttpOnly {
		t.Error("session cookie not HttpOnly")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", session.SameSite)
	}
	if session.Value == "" {
		t.Error("session token empty")
	}
	if session.Path != "/" {
		t.Errorf("Path = %q, want /", session.Path)
	}
}

func TestVerifyPost_WrongCode_RendersRejection(t *testing.T) {
	t.Parallel()
	e, a, _ := testServer(t)

	code := issueOTP(t, a, "@shima")
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	verifyForm := url.Values{"identifier": {"@shima"}, "code": {wrong}}
	verifyReq := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(verifyForm.Encode()))
	verifyReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, verifyReq)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "did not match") {
		t.Errorf("body missing rejection message; body:\n%s", w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Error("session cookie set on rejection")
		}
	}
}

// TestVerifyPost_MalformedCode_RendersValidationError pins the structural
// signal a malformed-code submission produces — re-renders the verify form
// (200 + form action="/verify"), preserves the identifier in the hidden
// field, and does NOT set the session cookie. Avoids overfitting to copy
// like "6 digits" which a future rewording would silently break (Beck R8).
func TestVerifyPost_MalformedCode_RendersValidationError(t *testing.T) {
	t.Parallel()
	e, _, _ := testServer(t)

	cases := []string{"", "12345", "abcdef", "1234567"}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			form := url.Values{"identifier": {"@shima"}, "code": {bad}}
			req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			e.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, `action="/verify"`) {
				t.Errorf("body missing verify form re-render for input %q", bad)
			}
			if !strings.Contains(body, `value="@shima"`) {
				t.Errorf("body did not preserve identifier for input %q", bad)
			}
			for _, c := range w.Result().Cookies() {
				if c.Name == SessionCookieName {
					t.Errorf("session cookie set on malformed input %q", bad)
				}
			}
		})
	}
}

// formPost issues a form-encoded POST and returns the recorder.
func formPost(t *testing.T, e *echo.Echo, path string, vals url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w
}

// TestVerifyPost_LockedAfterMaxAttempts drives 3 wrong codes and confirms
// the 4th surfaces the Locked render — exercising the terminal state that
// the production code path produces but the prior suite never observed.
func TestVerifyPost_LockedAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	e, a, _ := testServer(t)

	code := issueOTP(t, a, "@shima")
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}

	for i := 0; i < 2; i++ {
		formPost(t, e, "/verify", url.Values{
			"identifier": {"@shima"}, "code": {wrong},
		})
	}
	w := formPost(t, e, "/verify", url.Values{
		"identifier": {"@shima"}, "code": {wrong},
	})
	if !strings.Contains(w.Body.String(), "Too many attempts") {
		t.Errorf("expected Locked render; body:\n%s", w.Body.String())
	}
	// Even the correct code now is rejected with Locked.
	w = formPost(t, e, "/verify", url.Values{
		"identifier": {"@shima"}, "code": {code},
	})
	if !strings.Contains(w.Body.String(), "Too many attempts") {
		t.Errorf("expected Locked render on correct code post-lock; body:\n%s", w.Body.String())
	}
}

func TestVerifyPost_ExpiredCode(t *testing.T) {
	t.Parallel()
	e, a, _ := testServer(t)

	code := issueOTP(t, a, "@shima")

	// Advance clock past expiry. testServer wires Auth with a Fixed clock
	// we can rebind in the test (production uses System).
	a.Clock = clock.Fixed{T: t0.Add(otpExpiryPlusOne())}

	w := formPost(t, e, "/verify", url.Values{
		"identifier": {"@shima"}, "code": {code},
	})
	if !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("expected Expired render; body:\n%s", w.Body.String())
	}
}

func TestVerifyPost_ReplayReturnsAlreadyUsed(t *testing.T) {
	t.Parallel()
	e, a, _ := testServer(t)

	code := issueOTP(t, a, "@shima")
	// First Verify accepts.
	formPost(t, e, "/verify", url.Values{
		"identifier": {"@shima"}, "code": {code},
	})
	// Replay.
	w := formPost(t, e, "/verify", url.Values{
		"identifier": {"@shima"}, "code": {code},
	})
	if !strings.Contains(w.Body.String(), "already used") {
		t.Errorf("expected Spent render; body:\n%s", w.Body.String())
	}
	// No new session cookie on replay.
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Error("session cookie set on replay")
		}
	}
}

func TestVerifyPost_NoIdentifier_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	e, _, _ := testServer(t)

	form := url.Values{"code": {"123456"}}
	req := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if w.Header().Get("Location") != "/login" {
		t.Errorf("Location = %q", w.Header().Get("Location"))
	}
}
