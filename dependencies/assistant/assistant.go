// Package assistant wraps the external OTP-delivery webhook (spec §7.3).
// The HTTP client lives here per CLAUDE.md §5: external systems are
// "Dependencies," and the Logic layer is insulated from them.
package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Client sends an OTP delivery request to the configured webhook.
//
// Spec §7.3 contract:
//   - URL = ${OTP_ASSISTANT_URL}/api/webhook/claude
//   - Headers: x-api-key, Content-Type: application/json
//   - Body: {"message": "send OTP <code> to <display_name>"}
//   - Timeout: 5 seconds
//   - Retries: none
type Client interface {
	SendOTP(ctx context.Context, code, displayName string) error
}

// HTTPClient is the production implementation. Construct via NewHTTPClient
// so the embedded *http.Client is configured with the spec's 5s timeout.
type HTTPClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewHTTPClient(baseURL, apiKey string) *HTTPClient {
	return &HTTPClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 5 * time.Second}, // spec §7.3
	}
}

func (c *HTTPClient) SendOTP(ctx context.Context, code, displayName string) error {
	body, err := json.Marshal(map[string]string{
		"message": fmt.Sprintf("send OTP %s to %s", code, displayName),
	})
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/webhook/claude", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("assistant returned %s", resp.Status)
	}
	return nil
}

// Recorder is a test/dev fake that captures every send and never errors
// (unless ErrToReturn is set). Useful for end-to-end tests where a real
// network call is undesirable, and for local dev where the operator just
// wants the OTP printed to a log instead of texted.
type Recorder struct {
	mu          sync.Mutex
	Sent        []SentMessage
	ErrToReturn error
}

type SentMessage struct {
	Code        string
	DisplayName string
	At          time.Time
}

func (r *Recorder) SendOTP(ctx context.Context, code, displayName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ErrToReturn != nil {
		return r.ErrToReturn
	}
	r.Sent = append(r.Sent, SentMessage{Code: code, DisplayName: displayName, At: time.Now()})
	return nil
}

// Last returns the most recent sent message; useful for tests and the
// dev-mode "see the code in the log" experience.
func (r *Recorder) Last() (SentMessage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.Sent) == 0 {
		return SentMessage{}, false
	}
	return r.Sent[len(r.Sent)-1], true
}
