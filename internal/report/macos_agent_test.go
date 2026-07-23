package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type macOSReportRequest struct {
	payload Report
	auth    string
	err     error
}

func TestMacOSScriptProducesValidReport(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system commands are only available on Darwin")
	}
	if _, err := exec.LookPath("osascript"); err != nil {
		t.Skip("osascript is unavailable")
	}
	received := make(chan macOSReportRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload Report
		err := json.NewDecoder(request.Body).Decode(&payload)
		received <- macOSReportRequest{payload: payload, auth: request.Header.Get("Authorization"), err: err}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusAccepted)
		_, _ = response.Write([]byte(`{"status":"accepted"}`))
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "agent.env")
	config := "SERVER_STATUS_URL='" + server.URL + "'\n" +
		"SERVER_STATUS_AGENT_ID='20000000-0000-4000-8000-000000000001'\n" +
		"SERVER_STATUS_TOKEN='test-token'\n" +
		"SERVER_STATUS_INTERVAL_SECONDS=60\n" +
		"SERVER_STATUS_LABELS='{\"environment\":\"test\"}'\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join("..", "..", "macos-agent", "server-status-agent")
	if _, err := os.ReadFile(scriptPath); err != nil {
		t.Fatalf("read macOS Agent script: %v", err)
	}
	output, err := exec.Command("/bin/zsh", scriptPath, "once", configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("macOS Agent failed: %v\n%s", err, output)
	}
	request := <-received
	if request.err != nil {
		t.Fatalf("decode macOS report: %v", request.err)
	}
	if request.auth != "Bearer test-token" {
		t.Fatalf("unexpected authorization header %q", request.auth)
	}
	payload := request.payload
	if err := payload.Validate(time.Now().UTC()); err != nil {
		t.Fatalf("validate macOS report: %v", err)
	}
	if payload.Agent.OSName != "macOS" || payload.Agent.Architecture == "" {
		t.Fatalf("unexpected macOS identity: %+v", payload.Agent)
	}
	if len(payload.Inventory.CPUPackages) != 1 || len(payload.Inventory.MemoryModules) != 1 {
		t.Fatalf("expected aggregate CPU and memory inventory: %+v", payload.Inventory)
	}
	cpu := payload.Inventory.CPUPackages[0]
	if payload.Agent.Architecture == "arm64" && cpu.PerformanceCores+cpu.EfficiencyCores != cpu.PhysicalCores {
		t.Fatalf("Apple Silicon core classes do not match physical cores: %+v", cpu)
	}
}

func TestMacOSScriptAppliesAutomaticUpdate(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system commands are only available on Darwin")
	}
	sourcePath := filepath.Join("..", "..", "macos-agent", "server-status-agent")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	current := bytes.Replace(source, []byte("AGENT_VERSION='dev'"), []byte("AGENT_VERSION='1.0.0'"), 1)
	candidate := bytes.Replace(source, []byte("AGENT_VERSION='dev'"), []byte("AGENT_VERSION='1.1.0'"), 1)
	if bytes.Equal(current, source) || bytes.Equal(candidate, source) {
		t.Fatal("test could not inject macOS Agent versions")
	}
	digest := sha256.Sum256(candidate)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/reports":
			var payload Report
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				http.Error(response, err.Error(), http.StatusBadRequest)
				return
			}
			if err := payload.Validate(time.Now().UTC()); err != nil {
				http.Error(response, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			response.WriteHeader(http.StatusAccepted)
			_, _ = response.Write([]byte(`{"status":"accepted","agent_update":{"version":"1.1.0"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/agent/releases/v1.1.0/checksums.txt":
			_, _ = fmt.Fprintf(response, "%x  server-status-agent-macos\n", digest)
		case request.Method == http.MethodGet && request.URL.Path == "/agent/releases/v1.1.0/server-status-agent-macos":
			_, _ = response.Write(candidate)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	temporaryDirectory := t.TempDir()
	agentPath := filepath.Join(temporaryDirectory, "server-status-agent")
	if err := os.WriteFile(agentPath, current, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(temporaryDirectory, "agent.env")
	config := "SERVER_STATUS_URL='" + server.URL + "'\n" +
		"SERVER_STATUS_AGENT_ID='20000000-0000-4000-8000-000000000001'\n" +
		"SERVER_STATUS_TOKEN='test-token'\n" +
		"SERVER_STATUS_INTERVAL_SECONDS=60\n" +
		"SERVER_STATUS_LABELS='{}'\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command("/bin/zsh", agentPath, "once", configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("macOS Agent update failed: %v\n%s", err, output)
	}
	installed, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, candidate) {
		t.Fatalf("automatic update did not atomically install the verified candidate\n%s", output)
	}
	version, err := exec.Command("/bin/zsh", agentPath, "--version").CombinedOutput()
	if err != nil || string(bytes.TrimSpace(version)) != "1.1.0" {
		t.Fatalf("updated Agent version = %q, err=%v", version, err)
	}
}

func TestMacOSScriptRejectsUpdateWithInvalidChecksum(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system commands are only available on Darwin")
	}
	sourcePath := filepath.Join("..", "..", "macos-agent", "server-status-agent")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	current := bytes.Replace(source, []byte("AGENT_VERSION='dev'"), []byte("AGENT_VERSION='1.0.0'"), 1)
	candidate := bytes.Replace(source, []byte("AGENT_VERSION='dev'"), []byte("AGENT_VERSION='1.1.0'"), 1)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/reports":
			response.WriteHeader(http.StatusAccepted)
			_, _ = response.Write([]byte(`{"status":"accepted","agent_update":{"version":"1.1.0"}}`))
		case "/agent/releases/v1.1.0/checksums.txt":
			_, _ = response.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  server-status-agent-macos\n"))
		case "/agent/releases/v1.1.0/server-status-agent-macos":
			_, _ = response.Write(candidate)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	temporaryDirectory := t.TempDir()
	agentPath := filepath.Join(temporaryDirectory, "server-status-agent")
	if err := os.WriteFile(agentPath, current, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(temporaryDirectory, "agent.env")
	config := "SERVER_STATUS_URL='" + server.URL + "'\n" +
		"SERVER_STATUS_AGENT_ID='20000000-0000-4000-8000-000000000001'\n" +
		"SERVER_STATUS_TOKEN='test-token'\n" +
		"SERVER_STATUS_INTERVAL_SECONDS=60\n" +
		"SERVER_STATUS_LABELS='{}'\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command("/bin/zsh", agentPath, "once", configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("report should remain successful after a rejected update: %v\n%s", err, output)
	}
	installed, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, current) {
		t.Fatal("invalid update replaced the current Agent")
	}
	if !bytes.Contains(output, []byte("SHA-256 verification failed")) {
		t.Fatalf("missing checksum failure diagnostic\n%s", output)
	}
}

func TestMacOSScriptOnlyAcceptsNewerSemanticVersions(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("zsh version comparison is only validated on Darwin")
	}
	scriptPath := filepath.Join("..", "..", "macos-agent", "server-status-agent")
	tests := []struct {
		current string
		target  string
		newer   bool
	}{
		{current: "1.0.0", target: "1.0.1", newer: true},
		{current: "1.2.0", target: "2.0.0", newer: true},
		{current: "1.0.0-beta.1", target: "1.0.0", newer: true},
		{current: "1.0.0", target: "1.0.0", newer: false},
		{current: "1.1.0", target: "1.0.9", newer: false},
		{current: "1.0.0", target: "1.0.1-beta.1", newer: true},
	}
	for _, test := range tests {
		command := exec.Command("/bin/zsh", "-c", `source "$1"; version_is_newer "$2" "$3"`, "test", scriptPath, test.current, test.target)
		err := command.Run()
		if got := err == nil; got != test.newer {
			t.Errorf("version_is_newer(%q, %q) = %v, want %v", test.current, test.target, got, test.newer)
		}
	}
}
