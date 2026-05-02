package handler

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	mw "github.com/rizaramadan/financial-shima/web/middleware"
	"github.com/rizaramadan/financial-shima/web/template"
)

// SettingsGet renders /settings — currently just the theme switcher
// (light / dark / auto). Future per-user settings land here too.
func (h *Handlers) SettingsGet(c echo.Context) error {
	u, ok := mw.CurrentUser(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	current := "auto"
	if cookie, err := c.Cookie(mw.ThemeCookieName); err == nil {
		switch cookie.Value {
		case "light", "dark":
			current = cookie.Value
		}
	}
	return c.Render(http.StatusOK, "settings", template.SettingsData{
		Title:        "Settings",
		DisplayName:  u.DisplayName,
		CurrentTheme: current,
		UnreadCount:  h.loadBellCount(c.Request().Context(), c, u.ID),
	})
}

// SettingsThemePost sets / clears the theme cookie. Three values:
//
//	"light" / "dark"  → 1-year cookie, explicit override on <html>.
//	"auto"            → cookie cleared, CSS @media takes over.
//
// Any other value is rejected by ignoring the form (cookie left
// untouched). The redirect target is `/settings` so the user sees
// the new state immediately.
func (h *Handlers) SettingsThemePost(c echo.Context) error {
	if _, ok := mw.CurrentUser(c); !ok {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	v := c.FormValue("theme")
	switch v {
	case "light", "dark":
		c.SetCookie(&http.Cookie{
			Name:     mw.ThemeCookieName,
			Value:    v,
			Path:     "/",
			MaxAge:   60 * 60 * 24 * 365, // 1 year
			HttpOnly: false,              // not sensitive; readable from JS too
			SameSite: http.SameSiteLaxMode,
			Secure:   c.Request().TLS != nil,
		})
	case "auto":
		c.SetCookie(&http.Cookie{
			Name:     mw.ThemeCookieName,
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Secure:   c.Request().TLS != nil,
		})
	default:
		// Unknown value — leave existing cookie alone.
	}
	return c.Redirect(http.StatusSeeOther, "/settings")
}
