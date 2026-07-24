package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSmartctlReleaseVersionUsesMatchingAgentRelease(t *testing.T) {
	for input, want := range map[string]string{
		"windows-legacy-1.2.3":      "v1.2.3",
		"windows-legacy-1.2.3-rc.1": "v1.2.3-rc.1",
		"windows-legacy-dev":        "latest",
		"invalid":                   "latest",
	} {
		if got := smartctlReleaseVersion(input); got != want {
			t.Errorf("smartctlReleaseVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDownloadReleaseAsset(t *testing.T) {
	content := []byte("signed smartmontools installer")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/agent/releases/v1.2.3/"+smartctlReleaseAsset {
			t.Errorf("unexpected request path %q", request.URL.Path)
		}
		if !strings.HasPrefix(request.Header.Get("User-Agent"), "server-status-windows-agent/") {
			t.Errorf("unexpected User-Agent %q", request.Header.Get("User-Agent"))
		}
		_, _ = response.Write(content)
	}))
	defer server.Close()

	expectedSHA256 := fmt.Sprintf("%x", sha256.Sum256(content))
	path, err := downloadReleaseAsset(server.Client(), server.URL, "v1.2.3", smartctlReleaseAsset, expectedSHA256, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, content) || filepath.Ext(path) != ".exe" {
		t.Fatalf("unexpected downloaded asset: path=%q content=%q", path, got)
	}
}

func TestDownloadReleaseAssetRejectsErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "missing", http.StatusNotFound)
	}))
	defer server.Close()
	if _, err := downloadReleaseAsset(server.Client(), server.URL, "latest", smartctlReleaseAsset, smartctlReleaseSHA256, t.TempDir()); err == nil {
		t.Fatal("expected dependency download failure")
	}
}

func TestDownloadReleaseAssetRejectsChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("tampered installer"))
	}))
	defer server.Close()
	if _, err := downloadReleaseAsset(server.Client(), server.URL, "latest", smartctlReleaseAsset, smartctlReleaseSHA256, t.TempDir()); err == nil {
		t.Fatal("expected dependency checksum failure")
	}
}

func TestSmartctlInstallerArgumentsSelectArchitectureAndComponents(t *testing.T) {
	for architecture, component := range map[string]string{"amd64": "x64", "386": "x32"} {
		got := smartctlInstallerArguments(architecture, filepath.Join("C:", "ServerStatus", "smartmontools.new"))
		if len(got) != 4 || got[0] != "/S" || got[1] != "/SO" || got[2] != "smartctl,drivedb,doc,"+component || !strings.HasPrefix(got[3], "/D=") {
			t.Fatalf("unexpected %s installer arguments: %#v", architecture, got)
		}
	}
	if got := smartctlExecutablePath(filepath.Join("C:", "ServerStatus")); !strings.HasSuffix(got, filepath.Join("smartmontools", "bin", "smartctl.exe")) {
		t.Fatalf("unexpected smartctl installation path %q", got)
	}
}
