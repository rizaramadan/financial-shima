package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v4"
)

// APIKeyHeader is the request header the middleware reads.
// Spec §7.2: `x-api-key`. Go's `net/http.Header` canonicalizes header
// names on access, so the lowercase form here matches the spec verbatim
// while still resolving any casing the client sends.
const APIKeyHeader = "x-api-key"

// authChallenge is the value of the `WWW-Authenticate` header the
// middleware sets on every 401 response, per RFC 7235.
const authChallenge = "ApiKey"

// APIKey returns Echo middleware that requires every downstream request
// to present a fixed shared secret in the [APIKeyHeader] header.
//
// Behavior per spec §7.2:
//   - Missing header / empty value → 401, code [APIErrorCodeMissingKey].
//   - Multiple `x-api-key` headers → 401, code [APIErrorCodeMultipleKeys].
//     A single value is the contract; ambiguity is rejected rather than
//     silently picking one (the order is proxy-dependent).
//   - Wrong key                     → 401, code [APIErrorCodeInvalidKey].
//   - Correct key                   → passes to the next handler.
//
// All 401 responses set `WWW-Authenticate: ApiKey` per RFC 7235 and
// serialize as [APIError].
//
// Construction with an empty `expected` panics: an empty configured key
// would let unauthenticated requests through, which is a deploy-time
// error that must surface loudly.
//
// Comparison uses [crypto/subtle.ConstantTimeCompare], which is constant
// time across both length and content (returns 0 for differing lengths
// without leaking timing on contents).
func APIKey(expected string) echo.MiddlewareFunc {
	if expected == "" {
		panic("middleware.APIKey: expected key is empty (refusing to allow unauthenticated requests)")
	}
	expectedBytes := []byte(expected)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			values := c.Request().Header.Values(APIKeyHeader)
			if len(values) > 1 {
				return reject(c, APIErrorCodeMultipleKeys, "multiple x-api-key headers received; send exactly one")
			}
			got := c.Request().Header.Get(APIKeyHeader)
			if got == "" {
				return reject(c, APIErrorCodeMissingKey, "missing x-api-key header")
			}
			if subtle.ConstantTimeCompare([]byte(got), expectedBytes) != 1 {
				return reject(c, APIErrorCodeInvalidKey, "invalid x-api-key")
			}
			return next(c)
		}
	}
}

// reject writes a JSON 401 with `WWW-Authenticate: ApiKey` set, used by
// [APIKey] for every rejection path.
func reject(c echo.Context, code, message string) error {
	c.Response().Header().Set("WWW-Authenticate", authChallenge)
	return c.JSON(http.StatusUnauthorized, APIError{Error: code, Message: message})
}
