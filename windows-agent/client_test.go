package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
)

func TestClientSendsAuthenticatedReport(t *testing.T) {
	client := newClient("http://central.test", "node-token")
	client.http.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/v1/reports" {
			t.Errorf("unexpected path %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer node-token" {
			t.Errorf("unexpected authorization header")
		}
		body := bytes.NewBuffer(nil)
		fmt.Fprint(body, `{"status":"accepted","agent_update":{"version":"9.9.9"}}`)
		return &http.Response{
			StatusCode: http.StatusAccepted, Status: "202 Accepted",
			Header: make(http.Header), Body: ioutil.NopCloser(body), Request: request,
		}, nil
	})
	if err := client.send(Report{Version: reportVersion}); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
