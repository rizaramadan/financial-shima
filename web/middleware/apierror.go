package middleware

import "github.com/labstack/echo/v4"

// APIError is the JSON error body shape for `/api/v1` responses per spec
// §7.2. Every rejection from `/api/v1` middleware and handlers must
// serialize through this type so the LLM caller parses one contract.
//
// The Go field is named [APIError.Code] (not Error) so it doesn't shadow
// the `error` interface at call sites — `ae.Code == APIErrorCodeMissingKey`
// reads correctly; `ae.Error == ...` would read as a category error. The
// JSON tag preserves the spec's `"error"` key on the wire.
type APIError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

// Error codes from the APIKey middleware. Each names a distinct failure
// mode so a caller can decide whether to send a key, rotate the key, or
// fix its request shape — distinctions the LLM caller cannot make from a
// single shared "unauthorized" code.
const (
	APIErrorCodeMissingKey         = "missing_api_key"
	APIErrorCodeInvalidKey         = "invalid_api_key"
	APIErrorCodeMultipleKeyHeaders = "multiple_api_key_headers"
)

// WriteAPIError writes a JSON error response with the given status,
// code, and message, using the [APIError] body shape. Future `/api/v1`
// handlers should call this for every non-success response so the
// body shape stays uniform across the API.
//
// 401 responses from the [APIKey] middleware additionally set
// `WWW-Authenticate: ApiKey` per RFC 7235; see the apikey.go reject
// helper which composes [WriteAPIError] with the challenge header.
func WriteAPIError(c echo.Context, status int, code, message string) error {
	return c.JSON(status, APIError{Code: code, Message: message})
}
