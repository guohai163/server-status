package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"time"
)

type Client struct {
	endpoint string
	token    string
	http     *http.Client
}

func newClient(serverURL, token string) *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		Dial:                  (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).Dial,
		MaxIdleConnsPerHost:   2,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
	return &Client{
		endpoint: serverURL + "/api/v1/reports",
		token:    token,
		http:     &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}
}

func (client *Client) send(payload Report) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode report: %v", err)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<uint(attempt-1)) * time.Second)
		}
		retry, err := client.sendOnce(body)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			return err
		}
	}
	return fmt.Errorf("send report after 3 attempts: %v", lastErr)
}

func (client *Client) sendOnce(body []byte) (bool, error) {
	request, err := http.NewRequest("POST", client.endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "server-status-windows-agent/"+Version)
	response, err := client.http.Do(request)
	if err != nil {
		return true, fmt.Errorf("send report: %v", err)
	}
	defer response.Body.Close()
	responseBody, _ := ioutil.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusAccepted {
		retry := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == 429 || response.StatusCode >= 500
		return retry, fmt.Errorf("server returned %s: %s", response.Status, bytes.TrimSpace(responseBody))
	}
	var receipt ReportReceipt
	if err := json.Unmarshal(responseBody, &receipt); err != nil {
		return false, fmt.Errorf("decode receipt: %v", err)
	}
	if receipt.Status != "accepted" {
		return false, fmt.Errorf("unexpected receipt status %q", receipt.Status)
	}
	return false, nil
}
