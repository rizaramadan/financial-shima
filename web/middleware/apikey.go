package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v4"
)

// APIKeyHeader is the request header the middleware reads.
// Spec §7.2: `x-api-key`.
const APIKeyHeader = "x-api-key"

// APIKey returns Echo middleware that requires every downstream request
// to present a fixed shared secret in the [APIKeyHeader] header.
//
// Behavior per spec §7.2:
//   - Missing or wrong key  → 401 with JSON body
//     {"error": "unauthorized", "message": "missing or invalid x-api-key"}.
//   - Correct key           → request passes to the next handler.
//
// Construction with an empty `expected` panics: an empty configured key
// would let unauthenticated requests through, which is a deploy-time error
// that must surface loudly rather than silently.
//
// Comparison is constant-time across both length and content via
// [crypto/subtle.ConstantTimeCompare], which returns 0 for differing
// lengths without leaking timing on contents.
func APIKey(expected string) echo.MiddlewareFunc {
	if expected == "" {
		panic("middleware.APIKey: expected key is empty (refusing to allow unauthenticated requests)")
	}
	expectedBytes := []byte(expected)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			got := c.Request().Header.Get(APIKeyHeader)
			if subtle.ConstantTimeCompare([]byte(got), expectedBytes) != 1 {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error":   "unauthorized",
					"message": "missing or invalid x-api-key",
				})
			}
			return next(c)
		}
	}
}
