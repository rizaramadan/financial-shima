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

// renderLogin builds an Echo with the project's standard middleware via
// setup.Apply, registers the /login route, and dispatches the request — so
// these tests exercise the same chain production traffic sees.
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

func TestLoginGet_Returns200WithTextHTMLContentType(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get(echo.HeaderContentType)
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want prefix text/html", ct)
	}
}

func TestLoginGet_HasUTF8CharsetDeclaration(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	meta := findFirst(doc, func(n *html.Node) bool {
		return isElement("meta")(n) && strings.EqualFold(attr(n, "charset"), "utf-8")
	})
	if meta == nil {
		t.Fatal(`no <meta charset="utf-8">`)
	}
}

func TestLoginGet_HasHTMLLangAndNonEmptyTitle(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	htmlEl := findFirst(doc, isElement("html"))
	if htmlEl == nil {
		t.Fatal("no <html>")
	}
	if got := attr(htmlEl, "lang"); got == "" {
		t.Error(`<html> missing lang attribute`)
	}

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

func TestLoginGet_HasExactlyOneLabel(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	labels := findAll(doc, isElement("label"))
	if len(labels) != 1 {
		t.Fatalf("found %d <label> elements, want 1", len(labels))
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

func TestLoginGet_IdentifierInputIsTextType(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no <input name="identifier">`)
	}
	got := strings.ToLower(attr(input, "type"))
	if got != "text" && got != "" {
		t.Errorf(`identifier input type = %q, want "text" (or omitted)`, got)
	}
}

func TestLoginGet_IdentifierInputHasLabelWithVisibleText(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no input`)
	}
	id := attr(input, "id")
	if id == "" {
		t.Fatal("identifier input has no id")
	}
	label := findFirst(doc, func(n *html.Node) bool {
		return isElement("label")(n) && attr(n, "for") == id
	})
	if label == nil {
		t.Fatalf(`no <label for=%q>`, id)
	}
	if textOf(label) == "" {
		t.Errorf(`<label for=%q> has no visible text`, id)
	}
}

// TestLoginGet_AutocompleteIsOff: the identifier is a Telegram handle, not a
// username/email, so password managers should not surface saved credentials
// here. autocomplete="off" is the right hint.
func TestLoginGet_AutocompleteIsOff(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no input`)
	}
	if got := attr(input, "autocomplete"); got != "off" {
		t.Errorf(`autocomplete = %q, want "off" (Telegram handle is not a saved username)`, got)
	}
}

// TestLoginGet_DisablesKeyboardCorrections: iOS auto-capitalize, autocorrect,
// and spellcheck would mangle a Telegram username on first keystroke.
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

	submitPred := func(n *html.Node) bool {
		return isElement("button")(n) && strings.ToLower(attr(n, "type")) == "submit"
	}
	btns := findAll(doc, submitPred)
	if len(btns) != 1 {
		t.Fatalf(`found %d <button type="submit">, want 1`, len(btns))
	}
	const want = "Send code via Telegram"
	if got := textOf(btns[0]); got != want {
		t.Errorf("submit button text = %q, want %q", got, want)
	}
}

func TestLoginGet_HasViewportMetaForResponsiveLayout(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	meta := findFirst(doc, func(n *html.Node) bool {
		return isElement("meta")(n) && strings.ToLower(attr(n, "name")) == "viewport"
	})
	if meta == nil {
		t.Fatal(`no <meta name="viewport">`)
	}
	if !strings.Contains(attr(meta, "content"), "width=device-width") {
		t.Errorf(`viewport content missing width=device-width`)
	}
}
