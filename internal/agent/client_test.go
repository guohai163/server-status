package agent

import (
	"context"
	"encoding/json"
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
		_ = json.NewEncoder(response).Encode(report.ReportReceipt{Status: "accepted"})
	}))
	defer server.Close()

	client := NewClient(server.URL, "token")
	if _, err := client.Send(context.Background(), report.Report{}); err != nil {
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
	if _, err := client.Send(context.Background(), report.Report{}); err == nil {
		t.Fatal("authentication error was accepted")
	}
	if attempts.Load() != 1 {
		t.Fatalf("authentication error was retried %d times", attempts.Load())
	}
}

func TestClientReadsAgentUpdateDirective(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(response).Encode(report.ReportReceipt{
			Status:      "accepted",
			AgentUpdate: &report.AgentUpdate{Version: "1.2.3"},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "token")
	receipt, err := client.Send(context.Background(), report.Report{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.AgentUpdate == nil || receipt.AgentUpdate.Version != "1.2.3" {
		t.Fatalf("unexpected update directive: %+v", receipt.AgentUpdate)
	}
}
