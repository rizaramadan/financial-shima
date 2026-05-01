package handler

import (
	"fmt"
	"html"
	"net/http"

	"github.com/labstack/echo/v4"

	mw "github.com/rizaramadan/financial-shima/web/middleware"
)

// HomeGet is a minimal placeholder for "/" that confirms session resolution
// works end-to-end. Phase 3 will replace it with the real balances view.
func (h *Handlers) HomeGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	// Phase 2 placeholder: just confirms the session was resolved.
	body := fmt.Sprintf(`<!doctype html><meta charset="utf-8"><title>Shima</title>`+
		`<p>Signed in as <strong>%s</strong>.</p>`, html.EscapeString(u.DisplayName))
	return c.HTML(http.StatusOK, body)
}
