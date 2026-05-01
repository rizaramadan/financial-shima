// Package middleware contains Echo middleware that touches per-request state.
package middleware

import (
	"github.com/labstack/echo/v4"

	"github.com/rizaramadan/financial-shima/logic/auth"
	"github.com/rizaramadan/financial-shima/logic/user"
)

// SessionContextKey is the Echo context key under which the resolved User
// (if any) is stored. Handlers read it via c.Get(SessionContextKey).
const SessionContextKey = "session_user"

// SessionCookieName must match handler.SessionCookieName. Duplicated here to
// avoid an import cycle.
const SessionCookieName = "shima_session"

// Session resolves the session cookie and attaches the user (if any) to the
// echo.Context. The middleware is non-blocking: if the cookie is missing or
// invalid, it simply doesn't set the user — handlers decide whether anonymous
// access is allowed for their route.
func Session(a *auth.Auth) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cookie, err := c.Cookie(SessionCookieName)
			if err == nil && cookie.Value != "" {
				if u, ok := a.ResolveSession(cookie.Value); ok {
					c.Set(SessionContextKey, u)
				}
			}
			return next(c)
		}
	}
}

// CurrentUser returns the resolved user attached by Session middleware, if
// any. Handlers that require auth call this and respond with redirect on
// false.
func CurrentUser(c echo.Context) (user.User, bool) {
	v := c.Get(SessionContextKey)
	if v == nil {
		return user.User{}, false
	}
	u, ok := v.(user.User)
	return u, ok
}
