// Package template owns the html/template definitions for the web layer.
// Each page is a complete document parsed into its own template — there
// is no shared "body" block (which would collide across pages in a single
// template set). Layout chrome is shared via Go string concatenation,
// keeping all template strings as Go consts (no filesystem dependency).
package template

import (
	"html/template"
	"io"

	"github.com/labstack/echo/v4"
)

// Renderer satisfies echo.Renderer using parsed html/templates.
type Renderer struct {
	t *template.Template
}

func New() *Renderer {
	t := template.New("")
	template.Must(t.New("login").Parse(layoutOpen + loginBody + layoutClose))
	template.Must(t.New("verify").Parse(layoutOpen + verifyBody + layoutClose))
	template.Must(t.New("home").Parse(layoutOpen + homeBody + layoutClose))
	return &Renderer{t: t}
}

func (r *Renderer) Render(w io.Writer, name string, data interface{}, _ echo.Context) error {
	return r.t.ExecuteTemplate(w, name, data)
}

// LoginData drives the login template. Error is non-empty when the user
// just submitted an unknown identifier or hit cooldown.
type LoginData struct {
	Title string
	Error string
}

// VerifyData drives the OTP-entry template. Identifier round-trips so the
// hidden field can replay it on POST.
type VerifyData struct {
	Title      string
	Identifier string
	Error      string
}

// HomeData drives the (Phase-2 placeholder) home view. Phase 9 replaces
// the placeholder body with the real balances/transactions UI.
type HomeData struct {
	Title       string
	DisplayName string
}

const layoutOpen = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Shima &mdash; {{.Title}}</title>
<style>
:root {
  --bg: #fafaf9; --fg: #1c1917; --muted: #57534e; --border: #d6d3d1;
  --accent: #0f172a; --accent-fg: #f8fafc; --focus: #2563eb; --error: #b91c1c;
  --radius: 0.5rem;
  accent-color: var(--focus);
}
::selection { background: color-mix(in oklab, var(--focus) 25%, transparent); }
@media (prefers-color-scheme: dark) {
  :root { --bg: #0c0a09; --fg: #f5f5f4; --muted: #a8a29e; --border: #44403c;
    --accent: #d6d3d1; --accent-fg: #0c0a09; --focus: #93c5fd; --error: #fca5a5; }
}
* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; }
body {
  background: var(--bg); color: var(--fg);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
               "Helvetica Neue", Arial, sans-serif;
  font-size: 16px; line-height: 1.5;
  min-height: 100vh; display: grid;
  align-items: start; justify-items: center;
  padding: max(1.5rem, 12vh) 1.5rem 1.5rem;
}
@media (max-width: 360px) { body { padding: 1rem; } }
main { width: 100%; max-width: 24rem; }
h1 { font-size: 1.875rem; font-weight: 600; margin: 0 0 1.5rem; letter-spacing: -0.02em; }
form { margin: 0; }
.field { margin-bottom: 1.5rem; }
label { display: block; font-size: 0.875rem; font-weight: 500; margin-bottom: 0.5rem; }
.hint { display: block; font-size: 0.8125rem; color: var(--muted); margin: 0.5rem 0 0; }
input { width: 100%; padding: 0.625rem 0.75rem; font: inherit; font-size: max(1rem, 16px);
  color: var(--fg); background: var(--bg); border: 1px solid var(--border);
  border-radius: var(--radius); }
input:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
button { width: 100%; padding: 0.875rem 1rem; font: inherit; font-size: max(1rem, 16px); font-weight: 600;
  color: var(--accent-fg); background: var(--accent); border: 1px solid var(--accent);
  border-radius: var(--radius); cursor: pointer; }
button:hover:not(:disabled) {
  background: color-mix(in oklab, var(--accent) 85%, var(--fg));
  border-color: color-mix(in oklab, var(--accent) 85%, var(--fg));
}
button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
button:disabled { background: var(--border); color: color-mix(in oklab, var(--fg) 60%, transparent);
  border-color: var(--border); cursor: not-allowed; }
.alert { margin: 0 0 1rem; padding: 0.75rem 0.875rem; border-radius: var(--radius);
  background: color-mix(in oklab, var(--error) 12%, var(--bg));
  color: var(--error); font-size: 0.875rem;
  border: 1px solid color-mix(in oklab, var(--error) 30%, transparent); }
/* Subtitle sits directly under h1; tighter coupling than .hint under input. */
.subtitle { margin: -0.5rem 0 1.5rem; color: var(--muted); font-size: 0.9375rem; }
.subtitle strong { color: var(--fg); font-weight: 600; }
.linkbtn { display: inline; background: none; border: 0; padding: 0;
  color: var(--focus); font: inherit; font-size: 0.875rem; cursor: pointer;
  text-decoration: underline; text-underline-offset: 2px; width: auto; }
.linkbtn:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: 2px; }
.aside { margin: 1rem 0 0; text-align: center; font-size: 0.875rem; color: var(--muted); }
.aside form { display: inline; }
</style>
</head>
<body>
<main>
`

const layoutClose = `
</main>
</body>
</html>`

const loginBody = `<h1>Sign in</h1>
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/login">
<div class="field">
<label for="identifier">Telegram</label>
<input id="identifier" name="identifier" inputmode="text"
  placeholder="@shima or 123456789"
  autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false"
  required aria-describedby="identifier-hint">
<p id="identifier-hint" class="hint">@username or numeric ID</p>
</div>
<button type="submit">Continue with Telegram</button>
</form>`

const verifyBody = `<h1>Enter your code</h1>
<p class="subtitle">Sent to <strong>{{.Identifier}}</strong> on Telegram. Code expires in 5 minutes.</p>
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/verify">
<input type="hidden" name="identifier" value="{{.Identifier}}">
<div class="field">
<label for="code">6-digit code</label>
<input id="code" name="code" inputmode="numeric"
  pattern="[0-9]{6}" maxlength="6" minlength="6"
  autocapitalize="off" autocorrect="off" spellcheck="false"
  required autofocus>
</div>
<button type="submit">Verify</button>
</form>
<p class="aside">
<form method="post" action="/login">
<input type="hidden" name="identifier" value="{{.Identifier}}">
<button type="submit" class="linkbtn">Send a new code</button>
</form>
&nbsp;·&nbsp;
<a class="linkbtn" href="/login">Use a different identifier</a>
</p>`

const homeBody = `<h1>Signed in</h1>
<p class="subtitle">As <strong>{{.DisplayName}}</strong>. Balances, transactions, and Pos views ship in Phase 9.</p>
<form method="post" action="/logout">
<button type="submit">Sign out</button>
</form>`
