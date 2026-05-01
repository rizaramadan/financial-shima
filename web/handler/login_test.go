package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"golang.org/x/net/html"

	"github.com/rizaramadan/financial-shima/web/setup"
)

func renderLogin(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	setup.Apply(e)
	e.GET("/login", LoginGet)
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

// TestLoginGet_Returns200WithUTF8HTMLContentType: the user-observable behavior
// is that non-ASCII characters render correctly. The Content-Type header
// (which Echo sets via c.HTML) is what tells the browser to decode the bytes
// as UTF-8 — not the <meta charset>, which is a fallback if no header is sent.
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
		t.Errorf("Content-Type = %q, want to include charset=utf-8", ct)
	}
}

func TestLoginGet_HTMLLangIsEN(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	htmlEl := findFirst(doc, isElement("html"))
	if htmlEl == nil {
		t.Fatal("no <html> element")
	}
	if got := attr(htmlEl, "lang"); got != "en" {
		t.Errorf(`<html lang> = %q, want "en"`, got)
	}
}

func TestLoginGet_HasNonEmptyTitle(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	title := findFirst(doc, isElement("title"))
	if title == nil {
		t.Fatal("no <title>")
	}
	if textOf(title) == "" {
		t.Error("<title> empty")
	}
}

func TestLoginGet_FormUsesPOSTMethod(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	forms := findAll(doc, isElement("form"))
	if len(forms) != 1 {
		t.Fatalf("found %d <form> elements, want 1", len(forms))
	}
	if got := strings.ToLower(attr(forms[0], "method")); got != "post" {
		t.Errorf("form method = %q, want post", got)
	}
}

func TestLoginGet_FormPostsToLoginPath(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	form := findFirst(doc, isElement("form"))
	if form == nil {
		t.Fatal("no <form>")
	}
	if got := attr(form, "action"); got != "/login" {
		t.Errorf("form action = %q, want /login", got)
	}
}

// TestLoginGet_EveryInputHasAssociatedLabel: the invariant is "every visible
// input is labelled," not "there is exactly one label." If Phase 2 adds an
// OTP field this test still holds; a count-based test would have to change.
func TestLoginGet_EveryInputHasAssociatedLabel(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	visibleInput := func(n *html.Node) bool {
		if !isElement("input")(n) {
			return false
		}
		t := strings.ToLower(attr(n, "type"))
		return t != "hidden" && t != "submit" && t != "button" && t != "reset"
	}
	labelByFor := map[string]bool{}
	for _, l := range findAll(doc, isElement("label")) {
		if id := attr(l, "for"); id != "" {
			labelByFor[id] = true
		}
	}
	for _, in := range findAll(doc, visibleInput) {
		id := attr(in, "id")
		if id == "" {
			t.Errorf("visible input %q has no id (cannot be labelled)", attr(in, "name"))
			continue
		}
		if !labelByFor[id] {
			t.Errorf(`visible input id=%q has no <label for=%q>`, id, id)
		}
	}
}

func TestLoginGet_HasExactlyOneIdentifierInput(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	inputs := findAll(doc, identifierInputPred)
	if len(inputs) != 1 {
		t.Fatalf(`found %d <input name="identifier">, want 1`, len(inputs))
	}
}

// TestLoginGet_IdentifierInputOmitsTypeAttribute: HTML defaults <input> to
// type="text". Omitting the attribute is a deliberate choice that pins the
// default; an explicit "text" or any other value would change the test.
func TestLoginGet_IdentifierInputOmitsTypeAttribute(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no <input name="identifier">`)
	}
	if got := attr(input, "type"); got != "" {
		t.Errorf(`type = %q, want omitted (rely on HTML default of text)`, got)
	}
}

func TestLoginGet_IdentifierInputLabelHasExactCopy(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no input`)
	}
	id := attr(input, "id")
	if id == "" {
		t.Fatal("input has no id")
	}
	label := findFirst(doc, func(n *html.Node) bool {
		return isElement("label")(n) && attr(n, "for") == id
	})
	if label == nil {
		t.Fatalf(`no <label for=%q>`, id)
	}
	const want = "Telegram username or numeric ID"
	if got := textOf(label); got != want {
		t.Errorf("label text = %q, want %q", got, want)
	}
}

func TestLoginGet_AutocompleteIsOff(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no input`)
	}
	if got := attr(input, "autocomplete"); got != "off" {
		t.Errorf(`autocomplete = %q, want "off"`, got)
	}
}

func TestLoginGet_DisablesKeyboardCorrections(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no input`)
	}
	cases := []struct{ key, want string }{
		{"autocapitalize", "off"},
		{"autocorrect", "off"},
		{"spellcheck", "false"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			if got := attr(input, c.key); got != c.want {
				t.Errorf("input %s = %q, want %q", c.key, got, c.want)
			}
		})
	}
}

func TestLoginGet_IdentifierInputIsRequired(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no input`)
	}
	if !hasAttr(input, "required") {
		t.Error("identifier input missing required attribute")
	}
}

func TestLoginGet_HasExactlyOneSubmitButtonWithExactCopy(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))
	pred := func(n *html.Node) bool {
		return isElement("button")(n) && strings.ToLower(attr(n, "type")) == "submit"
	}
	btns := findAll(doc, pred)
	if len(btns) != 1 {
		t.Fatalf(`found %d submit buttons, want 1`, len(btns))
	}
	const want = "Send code"
	if got := textOf(btns[0]); got != want {
		t.Errorf("button text = %q, want %q", got, want)
	}
}
