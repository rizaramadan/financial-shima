package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/net/html"

	"github.com/rizaramadan/financial-shima/dependencies/assistant"
	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/clock"
	"github.com/rizaramadan/financial-shima/logic/idgen"
	"github.com/rizaramadan/financial-shima/logic/user"
	tplpkg "github.com/rizaramadan/financial-shima/web/template"
)

var t0 = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

// testServer constructs an Echo wired with the project renderer + handler,
// using deterministic Auth (Fixed clock, fixed entropy, fixed IDGen) and a
// Recorder assistant. Tests can read back rec.Sent to learn the issued OTP.
func testServer(t *testing.T) (*echo.Echo, *auth.Auth, *assistant.Recorder) {
	t.Helper()
	src := bytes.NewReader(make([]byte, 1024))
	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = byte(i + 1)
	}
	src = bytes.NewReader(buf)
	a := auth.New(user.Seeded(), clock.Fixed{T: t0}, src, idgen.Fixed{Value: "tok-test"})
	rec := &assistant.Recorder{}
	h := New(a, rec, nil) // nil pool: handler tests run without DB
	h.LoginPassword = "test-password"

	e := echo.New()
	e.Renderer = tplpkg.New()
	e.GET("/login", h.LoginGet)
	e.POST("/login", h.LoginPost)
	e.GET("/verify", h.VerifyGet)
	e.POST("/verify", h.VerifyPost)
	return e, a, rec
}

func renderLogin(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	e, _, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func parseDoc(t *testing.T, rec *httptest.ResponseRecorder) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("html.Parse: %v", err)
	}
	return doc
}

func walk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

func findAll(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	walk(n, func(node *html.Node) {
		if pred(node) {
			out = append(out, node)
		}
	})
	return out
}

func findFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	all := findAll(n, pred)
	if len(all) == 0 {
		return nil
	}
	return all[0]
}

func textOf(n *html.Node) string {
	var b strings.Builder
	walk(n, func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
	})
	return strings.TrimSpace(b.String())
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func isElement(name string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == name
	}
}

func identifierInputPred(n *html.Node) bool {
	return isElement("input")(n) && attr(n, "name") == "identifier"
}

func TestLoginGet_Returns200WithUTF8HTMLContentType(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get(echo.HeaderContentType)
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want prefix text/html", ct)
	}
	if !strings.Contains(strings.ToLower(ct), "charset=utf-8") {
		t.Errorf("Content-Type = %q, want charset=utf-8", ct)
	}
}

func TestLoginGet_HTMLLangIsEN(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	htmlEl := findFirst(doc, isElement("html"))
	if htmlEl == nil {
		t.Fatal("no <html>")
	}
	if got := attr(htmlEl, "lang"); got != "en" {
		t.Errorf(`<html lang> = %q, want "en"`, got)
	}
}

func TestLoginGet_TitleContainsSignIn(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	title := findFirst(doc, isElement("title"))
	if title == nil {
		t.Fatal("no <title>")
	}
	if got := textOf(title); !strings.Contains(got, "Sign in") {
		t.Errorf("title = %q, want it to contain %q", got, "Sign in")
	}
}

func TestLoginGet_FormUsesPOSTMethod(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	forms := findAll(doc, isElement("form"))
	if len(forms) != 1 {
		t.Fatalf("found %d forms, want 1", len(forms))
	}
	if got := strings.ToLower(attr(forms[0], "method")); got != "post" {
		t.Errorf("method = %q, want post", got)
	}
}

func TestLoginGet_FormPostsToLoginPath(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	form := findFirst(doc, isElement("form"))
	if form == nil {
		t.Fatal("no form")
	}
	if got := attr(form, "action"); got != "/login" {
		t.Errorf("action = %q, want /login", got)
	}
}

func TestLoginGet_HasExactlyOneIdentifierInput(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	inputs := findAll(doc, identifierInputPred)
	if len(inputs) != 1 {
		t.Fatalf("found %d inputs, want 1", len(inputs))
	}
}

func TestLoginGet_IdentifierInputAcceptsPlainText(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal("no input")
	}
	switch got := strings.ToLower(attr(input, "type")); got {
	case "", "text":
	default:
		t.Errorf(`type = %q, want "" or "text"`, got)
	}
}

func TestLoginGet_AutocompleteIsOff(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal("no input")
	}
	if got := attr(input, "autocomplete"); got != "off" {
		t.Errorf(`autocomplete = %q, want "off"`, got)
	}
}

func TestLoginGet_IdentifierInputIsRequired(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal("no input")
	}
	if !hasAttr(input, "required") {
		t.Error("required missing")
	}
}

func TestLoginGet_HasSubmitButtonWithExactCopy(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	pred := func(n *html.Node) bool {
		return isElement("button")(n) && strings.ToLower(attr(n, "type")) == "submit"
	}
	btns := findAll(doc, pred)
	if len(btns) != 1 {
		t.Fatalf("found %d submit buttons, want 1", len(btns))
	}
	if got := textOf(btns[0]); got != "Sign in" {
		t.Errorf("button text = %q, want Sign in", got)
	}
}

// --- password-login tests ---

func TestLoginPost_KnownUserCorrectPassword_SetsSessionCookieAndRedirectsHome(t *testing.T) {
	t.Parallel()
	e, _, _ := testServer(t)

	form := url.Values{"identifier": {"@shima"}, "password": {"test-password"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d (303)", w.Code, http.StatusSeeOther)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	var sessionSet bool
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName && c.Value != "" {
			sessionSet = true
		}
	}
	if !sessionSet {
		t.Error("session cookie not set on successful login")
	}
}

func TestLoginPost_UnknownUser_RendersUserNotFound(t *testing.T) {
	t.Parallel()
	e, _, _ := testServer(t)

	form := url.Values{"identifier": {"@stranger"}, "password": {"test-password"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "User not found") {
		t.Error("body missing 'User not found' message")
	}
}

func TestLoginPost_WrongPassword_RendersInvalidPassword(t *testing.T) {
	t.Parallel()
	e, _, _ := testServer(t)

	form := url.Values{"identifier": {"@shima"}, "password": {"nope"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Invalid password") {
		t.Error("body missing 'Invalid password' message")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Error("session cookie set on wrong password")
		}
	}
}

func TestLoginPost_LoginPasswordUnset_RejectsEvenEmptyPassword(t *testing.T) {
	t.Parallel()
	src := bytes.NewReader(make([]byte, 1024))
	buf := make([]byte, 1024)
	for i := range buf {
		buf[i] = byte(i + 1)
	}
	src = bytes.NewReader(buf)
	a := auth.New(user.Seeded(), clock.Fixed{T: t0}, src, idgen.Fixed{Value: "tok"})
	h := New(a, &assistant.Recorder{}, nil)
	// h.LoginPassword left as zero value (unset).
	e := echo.New()
	e.Renderer = tplpkg.New()
	e.POST("/login", h.LoginPost)

	form := url.Values{"identifier": {"@shima"}, "password": {""}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Error("session cookie set despite LOGIN_PASSWORD being unset")
		}
	}
}
