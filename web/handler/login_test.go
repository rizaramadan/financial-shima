package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"golang.org/x/net/html"
)

// renderLogin registers the route on a fresh Echo and dispatches via
// e.ServeHTTP so the test exercises the framework's routing path, not just
// LoginGet in isolation. Production middleware is intentionally not applied
// here — that's what cmd/server tests cover.
func renderLogin(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
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

func TestLoginGet_HasHTMLLangAndNonEmptyTitle(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	htmlEl := findFirst(doc, isElement("html"))
	if htmlEl == nil {
		t.Fatal("no <html> element")
	}
	if got := attr(htmlEl, "lang"); got == "" {
		t.Error(`<html> missing lang attribute (accessibility / SEO baseline)`)
	}

	title := findFirst(doc, isElement("title"))
	if title == nil {
		t.Fatal("no <title> element")
	}
	if textOf(title) == "" {
		t.Error("<title> is empty")
	}
}

func TestLoginGet_FormUsesPOSTMethod(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	forms := findAll(doc, isElement("form"))
	if len(forms) != 1 {
		t.Fatalf("found %d <form> elements, want exactly 1", len(forms))
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

func TestLoginGet_HasExactlyOneIdentifierInput(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	inputs := findAll(doc, identifierInputPred)
	if len(inputs) != 1 {
		t.Fatalf(`found %d <input name="identifier">, want exactly 1`, len(inputs))
	}
}

func TestLoginGet_IdentifierInputIsTextType(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no <input name="identifier">`)
	}
	// type defaults to "text", but we assert it explicitly so a regression
	// to type="password" or type="hidden" fails loudly.
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
		t.Fatal(`no <input name="identifier">`)
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
	if text := textOf(label); text == "" {
		t.Errorf(`<label for=%q> has no visible text content`, id)
	}
}

func TestLoginGet_IdentifierInputUsesMobileFriendlyAttributes(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no <input name="identifier">`)
	}

	cases := []struct{ key, want string }{
		{"autocomplete", "username"},
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

	t.Run("required", func(t *testing.T) {
		if !hasAttr(input, "required") {
			t.Error("missing required attribute on identifier input")
		}
	})
}

func TestLoginGet_HasNoAutofocus_PerMobileUXReview(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	input := findFirst(doc, identifierInputPred)
	if input == nil {
		t.Fatal(`no <input name="identifier">`)
	}
	if hasAttr(input, "autofocus") {
		t.Error(`identifier input has "autofocus"; removed by Round 2 mobile UX review`)
	}
}

func TestLoginGet_HasExactlyOneSubmitButtonWithVisibleText(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	submitPred := func(n *html.Node) bool {
		return isElement("button")(n) && strings.ToLower(attr(n, "type")) == "submit"
	}
	btns := findAll(doc, submitPred)
	if len(btns) != 1 {
		t.Fatalf(`found %d <button type="submit">, want exactly 1`, len(btns))
	}
	if textOf(btns[0]) == "" {
		t.Error(`<button type="submit"> has no visible text content`)
	}
}

func TestLoginGet_HasViewportMetaForResponsiveLayout(t *testing.T) {
	t.Parallel()
	doc := parseDoc(t, renderLogin(t))

	meta := findFirst(doc, func(n *html.Node) bool {
		return isElement("meta")(n) && strings.ToLower(attr(n, "name")) == "viewport"
	})
	if meta == nil {
		t.Fatal(`no <meta name="viewport"> in <head>`)
	}
	content := attr(meta, "content")
	if !strings.Contains(content, "width=device-width") {
		t.Errorf(`viewport content = %q, want to include "width=device-width"`, content)
	}
}
