package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
	e, _, rec := testServer(t)

	// Drive Issue first so a code exists.
	loginForm := url.Values{"identifier": {"@shima"}}
	loginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	e.ServeHTTP(httptest.NewRecorder(), loginReq)

	last, ok := rec.Last()
	if !ok {
		t.Fatal("no OTP recorded")
	}

	verifyForm := url.Values{
		"identifier": {"@shima"},
		"code":       {last.Code},
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
	e, _, rec := testServer(t)

	loginForm := url.Values{"identifier": {"@shima"}}
	loginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	e.ServeHTTP(httptest.NewRecorder(), loginReq)
	last, _ := rec.Last()

	wrong := "000000"
	if last.Code == wrong {
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
			if !strings.Contains(w.Body.String(), "6 digits") {
				t.Errorf("body missing '6 digits' message for input %q", bad)
			}
		})
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
