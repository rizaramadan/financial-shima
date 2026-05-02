package middleware

import "github.com/labstack/echo/v4"

// APIError is the JSON error body shape for `/api/v1` responses per spec
// §7.2. Every rejection from `/api/v1` middleware and handlers must
// serialize through this type so the LLM caller parses one contract.
//
// Two identifiers travel with every error:
//
//   - [APIError.Site] is a stable per-call-site code in the form `FS-NNNN`,
//     curated in `docs/errors.md`. When something fails, `FS-0042` is
//     the precise pinpoint — operators grep the registry to find the
//     exact emission site without parsing the message.
//   - [APIError.Code] is the spec category (`validation_failed`,
//     `internal_error`, …) — what the LLM caller programs against.
//
// The Go field name `Code` (not `Error`) keeps it from shadowing the
// `error` interface at call sites — `ae.Code == APIErrorCodeMissingKey`
// reads correctly. The JSON tag preserves the spec's `"error"` key.
type APIError struct {
	Site    string `json:"site"`
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

// Generic error codes for `/api/v1` handler responses. Defined here so
// every handler in the family pulls from one source — the alternative
// (each handler inventing its own string at the call site) drifts the
// contract within a few endpoints.
const (
	APIErrorCodeInternal           = "internal_error"
	APIErrorCodeServiceUnavailable = "service_unavailable"
	APIErrorCodeValidation         = "validation_failed"
	APIErrorCodeNotFound           = "not_found"
	APIErrorCodeConflict           = "conflict"
)

// WriteAPIError writes a JSON error response with the given status,
// site, code, and message, using the [APIError] body shape. Every
// `/api/v1` non-success response should call this so the body shape
// stays uniform across the API.
//
// `site` is a stable per-call-site identifier of the form `FS-NNNN`
// curated in `docs/errors.md`. Two emission points must never share a
// site code — the pinpoint guarantee depends on uniqueness.
//
// 401 responses from the [APIKey] middleware additionally set
// `WWW-Authenticate: ApiKey` per RFC 7235; see the apikey.go reject
// helper which composes [WriteAPIError] with the challenge header.
func WriteAPIError(c echo.Context, status int, site, code, message string) error {
	return c.JSON(status, APIError{Site: site, Code: code, Message: message})
}

// LogError logs an internal failure with the call-site code prefixed,
// so server logs and the corresponding API response share one
// pinpoint. Use this in front of every `WriteAPIError` whose `code` is
// `APIErrorCodeInternal` or otherwise warrants log inspection (5xx,
// transient DB failures, lock contention).
//
// Format mirrors echo's logger: a printf-style template plus args.
func LogError(c echo.Context, site, format string, args ...interface{}) {
	c.Logger().Errorf("["+site+"] "+format, args...)
}
