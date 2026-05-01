package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// loginPageHTML is the rendered Phase-1 login page. Every attribute below is
// asserted by a test in login_test.go. Do NOT fmt.Sprintf into this string;
// when interpolation is needed, switch to html/template (not text/template).
const loginPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Sign in to Shima</title>
<style>
:root {
  --bg: #fafaf9;
  --fg: #1c1917;
  --muted: #57534e;
  --border: #d6d3d1;
  --accent: #0f172a;
  --accent-fg: #f8fafc;
  --focus: #2563eb;
  --radius: 0.5rem;
  accent-color: var(--focus);
}
::selection {
  background: color-mix(in oklab, var(--focus) 25%, transparent);
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0c0a09;
    --fg: #f5f5f4;
    --muted: #a8a29e;
    --border: #44403c;
    --accent: #e7e5e4;
    --accent-fg: #1c1917;
    --focus: #93c5fd;
  }
}
* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; }
body {
  background: var(--bg);
  color: var(--fg);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
               "Helvetica Neue", Arial, sans-serif;
  font-size: 16px;
  line-height: 1.5;
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 1.5rem;
}
@media (max-width: 360px) {
  body { padding: 1rem; }
}
main {
  width: 100%;
  max-width: 24rem;
}
h1 {
  font-size: 1.875rem;
  font-weight: 650;
  margin: 0 0 1.75rem;
  letter-spacing: -0.02em;
}
form { margin: 0; }
.field { margin-bottom: 1.5rem; }
label {
  display: block;
  font-size: 0.875rem;
  font-weight: 500;
  margin-bottom: 0.25rem;
}
.hint {
  display: block;
  font-size: 0.8125rem;
  color: var(--muted);
  margin: 0 0 0.5rem;
}
input[type="text"] {
  width: 100%;
  padding: 0.625rem 0.75rem;
  font: inherit;
  font-size: 1rem;
  color: var(--fg);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
input[type="text"]:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 1px;
  border-color: var(--focus);
}
button {
  width: 100%;
  padding: 0.875rem 1rem;
  font: inherit;
  font-size: 1rem;
  font-weight: 600;
  color: var(--accent-fg);
  background: var(--accent);
  border: 1px solid var(--accent);
  border-radius: var(--radius);
  cursor: pointer;
}
button:hover:not(:disabled) {
  background: color-mix(in oklab, var(--accent) 92%, var(--fg));
  border-color: color-mix(in oklab, var(--accent) 92%, var(--fg));
}
button:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}
button:disabled {
  background: var(--border);
  color: var(--muted);
  border-color: var(--border);
  cursor: not-allowed;
}
</style>
</head>
<body>
<main>
<h1>Sign in to Shima</h1>
<form method="post" action="/login">
<div class="field">
<label for="identifier">Telegram username or ID</label>
<small id="identifier-hint" class="hint">e.g. @shima or 123456789</small>
<input
  id="identifier"
  name="identifier"
  type="text"
  inputmode="text"
  autocomplete="off"
  autocapitalize="off"
  autocorrect="off"
  spellcheck="false"
  required
  aria-describedby="identifier-hint"
>
</div>
<button type="submit">Send code via Telegram</button>
</form>
</main>
</body>
</html>`

// LoginGet serves the login page.
//
// HTTP contract: 200 OK, Content-Type "text/html; charset=UTF-8", body is the
// rendered login form per loginPageHTML. Returns a non-nil error only if the
// underlying response writer fails (client disconnect mid-write); never errors
// from request validation in Phase 1.
func LoginGet(c echo.Context) error {
	return c.HTML(http.StatusOK, loginPageHTML)
}
