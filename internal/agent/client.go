package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/guohai/server-status/internal/report"
)

type Client struct {
	endpoint string
	token    string
	http     *http.Client
}

func NewClient(serverURL, token string) *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	return &Client{
		endpoint: serverURL + "/api/v1/reports",
		token:    token,
		http:     &http.Client{Transport: transport, Timeout: 20 * time.Second},
	}
}

func (client *Client) Send(ctx context.Context, payload report.Report) (report.ReportReceipt, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return report.ReportReceipt{}, fmt.Errorf("encode report: %w", err)
	}
	var lastError error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<(attempt-1)) * time.Second
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return report.ReportReceipt{}, ctx.Err()
			case <-timer.C:
			}
		}
		receipt, retryable, err := client.sendOnce(ctx, body)
		if err == nil {
			return receipt, nil
		}
		lastError = err
		if !retryable {
			return report.ReportReceipt{}, err
		}
	}
	return report.ReportReceipt{}, fmt.Errorf("send report after 3 attempts: %w", lastError)
}

func (client *Client) sendOnce(ctx context.Context, body []byte) (report.ReportReceipt, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return report.ReportReceipt{}, false, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "server-status-agent/"+Version)

	response, err := client.http.Do(request)
	if err != nil {
		return report.ReportReceipt{}, ctx.Err() == nil, fmt.Errorf("send report: %w", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusAccepted {
		retryable := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return report.ReportReceipt{}, retryable, fmt.Errorf("server returned %s: %s", response.Status, bytes.TrimSpace(responseBody))
	}
	var receipt report.ReportReceipt
	if err := json.Unmarshal(responseBody, &receipt); err != nil {
		return report.ReportReceipt{}, false, fmt.Errorf("decode report receipt: %w", err)
	}
	if receipt.Status != "accepted" {
		return report.ReportReceipt{}, false, fmt.Errorf("unexpected report receipt status %q", receipt.Status)
	}
	return receipt, false, nil
}
