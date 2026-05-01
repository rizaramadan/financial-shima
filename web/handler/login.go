package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

const loginFormHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Sign in — Shima</title>
</head>
<body>
<main>
<h1>Sign in</h1>
<form method="post" action="/login">
<label for="identifier">Telegram ID or @username</label>
<input id="identifier" name="identifier" type="text" autocomplete="username" autofocus required>
<button type="submit">Send code</button>
</form>
</main>
</body>
</html>`

// LoginGet renders the login form. Phase 1: static HTML; templating arrives later.
func LoginGet(c echo.Context) error {
	return c.HTML(http.StatusOK, loginFormHTML)
}
