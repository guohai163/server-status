package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpdaterAtomicallyReplacesBinaryAndPreservesConfig(t *testing.T) {
	targetVersion := "1.2.0"
	newBinary := []byte("#!/bin/sh\nprintf '1.2.0\\n'\n")
	asset, err := updateAssetName("linux", runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	server := updateTestServer(t, targetVersion, asset, newBinary, fmt.Sprintf("%x", sha256.Sum256(newBinary)))

	directory := t.TempDir()
	binaryPath := filepath.Join(directory, "server-status-agent")
	configPath := filepath.Join(directory, "agent.env")
	oldBinary := []byte("old-agent-binary")
	config := []byte("SERVER_STATUS_AGENT_ID=existing-id\nSERVER_STATUS_TOKEN=existing-token\n")
	if err := os.WriteFile(binaryPath, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}

	updater := NewUpdater(server.URL, "1.1.0")
	updater.operatingSystem = "linux"
	updater.executablePath = func() (string, error) { return binaryPath, nil }
	if err := updater.Apply(context.Background(), targetVersion); err != nil {
		t.Fatal(err)
	}
	updatedBinary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updatedBinary) != string(newBinary) {
		t.Fatalf("unexpected updated binary: %q", updatedBinary)
	}
	unchangedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchangedConfig) != string(config) {
		t.Fatalf("Agent identity configuration changed: %q", unchangedConfig)
	}
}

func TestUpdaterKeepsCurrentBinaryOnChecksumFailure(t *testing.T) {
	targetVersion := "1.2.0"
	newBinary := []byte("#!/bin/sh\nprintf '1.2.0\\n'\n")
	asset, err := updateAssetName("linux", runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	server := updateTestServer(t, targetVersion, asset, newBinary, fmt.Sprintf("%064x", 0))

	directory := t.TempDir()
	binaryPath := filepath.Join(directory, "server-status-agent")
	oldBinary := []byte("old-agent-binary")
	if err := os.WriteFile(binaryPath, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	updater := NewUpdater(server.URL, "1.1.0")
	updater.operatingSystem = "linux"
	updater.executablePath = func() (string, error) { return binaryPath, nil }
	if err := updater.Apply(context.Background(), targetVersion); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	currentBinary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(currentBinary) != string(oldBinary) {
		t.Fatalf("current binary changed after failed update: %q", currentBinary)
	}
}

func TestUpdateAssetNameRejectsUnsupportedPlatforms(t *testing.T) {
	if _, err := updateAssetName("darwin", "arm64"); err == nil {
		t.Fatal("non-Linux automatic update was accepted")
	}
	if _, err := updateAssetName("linux", "386"); err == nil {
		t.Fatal("unsupported Linux architecture was accepted")
	}
}

func updateTestServer(t *testing.T, version, asset string, binary []byte, checksum string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agent/releases/v" + version + "/checksums.txt":
			_, _ = fmt.Fprintf(response, "%s  %s\n", checksum, asset)
		case "/agent/releases/v" + version + "/" + asset:
			_, _ = response.Write(binary)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
