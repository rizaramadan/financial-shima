package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClient_SendOTP_PostsExpectedShape(t *testing.T) {
	t.Parallel()
	var (
		gotMethod string
		gotPath   string
		gotKey    string
		gotCT     string
		gotBody   map[string]string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "test-key")
	if err := c.SendOTP(context.Background(), "123456", "Shima"); err != nil {
		t.Fatalf("SendOTP: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/webhook/claude" {
		t.Errorf("path = %q, want /api/webhook/claude", gotPath)
	}
	if gotKey != "test-key" {
		t.Errorf("x-api-key = %q, want test-key", gotKey)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}
	if gotBody["message"] != "send OTP 123456 to Shima" {
		t.Errorf("body message = %q, want spec-shaped string", gotBody["message"])
	}
}

func TestHTTPClient_SendOTP_NonSuccessReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "k")
	err := c.SendOTP(context.Background(), "123456", "Shima")
	if err == nil {
		t.Fatal("expected error on 502")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want it to mention 502", err)
	}
}

func TestRecorder_RecordsSentMessages(t *testing.T) {
	t.Parallel()
	r := &Recorder{}
	if err := r.SendOTP(context.Background(), "111111", "Riza"); err != nil {
		t.Fatalf("SendOTP: %v", err)
	}
	if err := r.SendOTP(context.Background(), "222222", "Shima"); err != nil {
		t.Fatalf("SendOTP: %v", err)
	}
	if len(r.Sent) != 2 {
		t.Fatalf("Sent len = %d, want 2", len(r.Sent))
	}
	last, ok := r.Last()
	if !ok || last.Code != "222222" || last.DisplayName != "Shima" {
		t.Errorf("Last = (%+v, %v)", last, ok)
	}
}

func TestRecorder_RespectsErrToReturn(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	r := &Recorder{ErrToReturn: want}
	if err := r.SendOTP(context.Background(), "x", "y"); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if len(r.Sent) != 0 {
		t.Error("error path should not record")
	}
}
