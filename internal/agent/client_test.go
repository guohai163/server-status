package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/guohai/server-status/internal/report"
)

func TestClientRetriesServerErrors(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(response, "temporary", http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token")
	if err := client.Send(context.Background(), report.Report{}); err != nil {
		t.Fatalf("retryable request failed: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestClientDoesNotRetryAuthenticationErrors(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(response, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(server.URL, "bad-token")
	if err := client.Send(context.Background(), report.Report{}); err == nil {
		t.Fatal("authentication error was accepted")
	}
	if attempts.Load() != 1 {
		t.Fatalf("authentication error was retried %d times", attempts.Load())
	}
}
