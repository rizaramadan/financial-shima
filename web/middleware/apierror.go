package middleware

// APIError is the JSON error body shape for `/api/v1` responses per spec
// §7.2. Every rejection from `/api/v1` middleware and handlers must
// serialize through this type so the LLM caller parses one contract.
type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Error codes for the APIKey middleware. Each names a distinct failure
// mode so a caller can decide whether to send a key, rotate the key, or
// fix its request shape — distinctions the LLM caller cannot make from a
// single shared "unauthorized" code.
const (
	APIErrorCodeMissingKey   = "missing_api_key"
	APIErrorCodeInvalidKey   = "invalid_api_key"
	APIErrorCodeMultipleKeys = "multiple_api_key_headers"
)
