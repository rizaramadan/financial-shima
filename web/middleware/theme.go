package middleware

import (
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/web/template"
)

// ThemeCookieName is the cookie that holds the user's explicit theme
// choice. Empty / missing = "auto" (let CSS prefers-color-scheme decide).
// Allowed values: "light", "dark".
const ThemeCookieName = "shima_theme"

// Theme reads the theme cookie and stashes its sanitized value into
// echo.Context under template.ThemeContextKey. The Renderer reads it
// to populate the {{themeAttr}} placeholder on the rendered <html>.
//
// Unknown values are ignored (treated as auto) — defends against a
// bad cookie surviving from an older format.
func Theme() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if cookie, err := c.Cookie(ThemeCookieName); err == nil {
				switch cookie.Value {
				case "light", "dark":
					c.Set(template.ThemeContextKey, cookie.Value)
				}
			}
			return next(c)
		}
	}
}
