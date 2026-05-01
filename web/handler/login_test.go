package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"golang.org/x/net/html"
)

// renderLogin executes LoginGet and returns the recorder.
// One construction site for the request — keeps individual tests focused on
// asserting one behavior each.
func renderLogin(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := LoginGet(c); err != nil {
		t.Fatalf("LoginGet returned error: %v", err)
	}
	return rec
}

// parseDoc parses the recorded body as HTML. Panics on parse failure (the
// handler's contract is to emit valid HTML).
func parseDoc(t *testing.T, rec *httptest.ResponseRecorder) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("html.Parse: %v", err)
	}
	return doc
}

// findFirst walks the tree and returns the first node matching pred.
func findFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	if pred(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findFirst(c, pred); got != nil {
			return got
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func isElement(name string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == name
	}
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

func TestLoginGet_FormPostsToLoginEndpoint(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	doc := parseDoc(t, rec)

	form := findFirst(doc, isElement("form"))
	if form == nil {
		t.Fatal("no <form> element in body")
	}
	if got := strings.ToLower(attr(form, "method")); got != "post" {
		t.Errorf("form method = %q, want post", got)
	}
	if got := attr(form, "action"); got != "/login" {
		t.Errorf("form action = %q, want /login", got)
	}
}

func TestLoginGet_HasIdentifierInputLabelledForAccessibility(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	doc := parseDoc(t, rec)

	input := findFirst(doc, func(n *html.Node) bool {
		return isElement("input")(n) && attr(n, "name") == "identifier"
	})
	if input == nil {
		t.Fatal(`no <input name="identifier"> in body`)
	}
	id := attr(input, "id")
	if id == "" {
		t.Fatal("identifier input has no id (cannot be associated with a label)")
	}

	label := findFirst(doc, func(n *html.Node) bool {
		return isElement("label")(n) && attr(n, "for") == id
	})
	if label == nil {
		t.Errorf(`no <label for=%q> associated with the identifier input`, id)
	}
}

func TestLoginGet_IdentifierInputUsesMobileFriendlyAttributes(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	doc := parseDoc(t, rec)

	input := findFirst(doc, func(n *html.Node) bool {
		return isElement("input")(n) && attr(n, "name") == "identifier"
	})
	if input == nil {
		t.Fatal(`no <input name="identifier">`)
	}

	cases := []struct{ key, want string }{
		{"autocomplete", "username"},
		{"autocapitalize", "off"},
		{"autocorrect", "off"},
		{"spellcheck", "false"},
		{"required", ""}, // boolean attribute; presence is what matters
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			got := attr(input, c.key)
			if c.key == "required" {
				// boolean attribute: empty string when present-without-value or with value
				if !hasAttr(input, "required") {
					t.Errorf("missing %q attribute on identifier input", c.key)
				}
				return
			}
			if got != c.want {
				t.Errorf("input %s = %q, want %q", c.key, got, c.want)
			}
		})
	}
}

func TestLoginGet_HasNoAutofocus_PerMobileUXReview(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	doc := parseDoc(t, rec)

	input := findFirst(doc, func(n *html.Node) bool {
		return isElement("input")(n) && attr(n, "name") == "identifier"
	})
	if input == nil {
		t.Fatal(`no <input name="identifier">`)
	}
	if hasAttr(input, "autofocus") {
		t.Error(`identifier input has "autofocus"; removed by Round 2 mobile UX review (pops keyboard on page load)`)
	}
}

func TestLoginGet_HasSubmitButton(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	doc := parseDoc(t, rec)

	btn := findFirst(doc, func(n *html.Node) bool {
		return isElement("button")(n) && strings.ToLower(attr(n, "type")) == "submit"
	})
	if btn == nil {
		t.Fatal(`no <button type="submit"> in body`)
	}
}

func TestLoginGet_HasViewportMetaForResponsiveLayout(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	doc := parseDoc(t, rec)

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

func TestLoginGet_HasErrorRegionForLiveAlerts(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	doc := parseDoc(t, rec)

	region := findFirst(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode &&
			strings.EqualFold(attr(n, "role"), "alert") &&
			strings.EqualFold(attr(n, "aria-live"), "polite")
	})
	if region == nil {
		t.Fatal(`no element with role="alert" aria-live="polite" (error region for Phase 2 form-error rendering)`)
	}
}

func TestLoginGet_HasCSRFTokenPlaceholder(t *testing.T) {
	t.Parallel()
	rec := renderLogin(t)
	doc := parseDoc(t, rec)

	tok := findFirst(doc, func(n *html.Node) bool {
		return isElement("input")(n) &&
			strings.ToLower(attr(n, "type")) == "hidden" &&
			attr(n, "name") == "csrf"
	})
	if tok == nil {
		t.Fatal(`no <input type="hidden" name="csrf"> placeholder (Phase 2 will populate the value)`)
	}
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
