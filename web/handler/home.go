package handler

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// HomeGet renders the post-sign-in home page through the shared layout.
// Phase 9 replaces the placeholder body with the real balances/transactions
// view per spec §6.2; today the page exists so the auth flow has a styled,
// recognizable destination instead of an unstyled fragment.
func (h *Handlers) HomeGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	return c.Render(http.StatusOK, "home", template.HomeData{
		Title:       "Home",
		DisplayName: u.DisplayName,
	})
}

// LogoutPost revokes the session server-side and clears the cookie.
func (h *Handlers) LogoutPost(c echo.Context) error {
	if cookie, err := c.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		h.Auth.Logout(cookie.Value)
	}
	c.SetCookie(&http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.Request().TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	return c.Redirect(http.StatusSeeOther, "/login")
}
