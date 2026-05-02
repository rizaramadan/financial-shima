package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v4"
)

// APIKeyHeader is the request header the middleware reads.
// Spec §7.2: `x-api-key`. Go's `net/http.Header` canonicalizes header
// names on access, so the lowercase form here matches the spec verbatim
// while still resolving any casing the client sends (e.g. `X-Api-Key`).
const APIKeyHeader = "x-api-key"

// APIKeyAuthChallenge is the value of the `WWW-Authenticate` header set
// on every 401 response from the [APIKey] middleware, per RFC 7235.
// Exported so future 401-emitting middleware can stay aligned, and so
// tests can pin the contract from a single source.
const APIKeyAuthChallenge = "ApiKey"

// APIKey returns Echo middleware that requires every downstream request
// to present a fixed shared secret in the [APIKeyHeader] header.
//
// Behavior per spec §7.2:
//   - Multiple `x-api-key` headers → 401, [APIErrorCodeMultipleKeyHeaders].
//     A single value is the contract; ambiguity is rejected rather than
//     silently picking one (the order is proxy-dependent). This branch
//     fires before the value-comparison branches, so a request with two
//     headers — even if one happens to match — is rejected for the
//     structural problem.
//   - Missing header / empty value → 401, [APIErrorCodeMissingKey].
//   - Wrong key                     → 401, [APIErrorCodeInvalidKey].
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
//
// The `expected` value is captured for the lifetime of the returned
// handler; rotating the key requires restarting the process.
func APIKey(expected string) echo.MiddlewareFunc {
	if expected == "" {
		panic("middleware.APIKey: expected key is empty (refusing to allow unauthenticated requests)")
	}
	expectedBytes := []byte(expected)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			values := c.Request().Header.Values(APIKeyHeader)
			switch {
			case len(values) > 1:
				return reject(c, APIErrorCodeMultipleKeyHeaders, "multiple x-api-key headers; send exactly one")
			case len(values) == 0 || values[0] == "":
				return reject(c, APIErrorCodeMissingKey, "missing x-api-key header")
			}
			if subtle.ConstantTimeCompare([]byte(values[0]), expectedBytes) != 1 {
				return reject(c, APIErrorCodeInvalidKey, "invalid x-api-key")
			}
			return next(c)
		}
	}
}

// reject writes the 401 JSON body and sets `WWW-Authenticate: ApiKey`,
// composing [WriteAPIError] with the auth challenge. Used for every
// rejection path inside [APIKey].
func reject(c echo.Context, code, message string) error {
	c.Response().Header().Set("WWW-Authenticate", APIKeyAuthChallenge)
	return WriteAPIError(c, http.StatusUnauthorized, code, message)
}
