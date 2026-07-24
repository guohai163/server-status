package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	smartctlReleaseAsset  = "server-status-smartctl-windows-setup.exe"
	smartctlReleaseSHA256 = "896337fcc253220614cf8cdbd5cf2321c5aa326a37a04160a672a281e6104c70"
	maxDependencyBytes    = 64 << 20
)

var releasedWindowsAgentPattern = regexp.MustCompile(`^windows-legacy-([0-9]+\.[0-9]+\.[0-9]+(?:[.-][0-9A-Za-z.-]+)?)$`)

func smartctlReleaseVersion(agentVersion string) string {
	match := releasedWindowsAgentPattern.FindStringSubmatch(strings.TrimSpace(agentVersion))
	if len(match) == 2 {
		return "v" + match[1]
	}
	return "latest"
}

func newReleaseDownloadClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		Dial:                  (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).Dial,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: 3 * time.Minute}
}

func downloadReleaseAsset(client *http.Client, serverURL, version, asset, expectedSHA256, directory string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("release download client is required")
	}
	if len(expectedSHA256) != sha256.Size*2 {
		return "", fmt.Errorf("expected SHA-256 for %s is invalid", asset)
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return "", fmt.Errorf("expected SHA-256 for %s is invalid", asset)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", fmt.Errorf("create dependency directory: %v", err)
	}
	temporary, err := os.CreateTemp(directory, ".smartctl-setup-*.exe")
	if err != nil {
		return "", fmt.Errorf("create dependency download: %v", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()

	endpoint := strings.TrimRight(serverURL, "/") + "/agent/releases/" + version + "/" + asset
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create dependency request: %v", err)
	}
	request.Header.Set("User-Agent", "server-status-windows-agent/"+Version)
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download %s: %v", asset, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: server returned %s", asset, response.Status)
	}
	if response.ContentLength > maxDependencyBytes {
		return "", fmt.Errorf("download %s exceeds %d bytes", asset, maxDependencyBytes)
	}
	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, digest), io.LimitReader(response.Body, maxDependencyBytes+1))
	if copyErr != nil {
		return "", fmt.Errorf("save %s: %v", asset, copyErr)
	}
	if written == 0 || written > maxDependencyBytes {
		return "", fmt.Errorf("download %s has invalid size %d", asset, written)
	}
	actualSHA256 := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actualSHA256, expectedSHA256) {
		return "", fmt.Errorf("download %s failed SHA-256 verification: expected %s, got %s", asset, expectedSHA256, actualSHA256)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync %s: %v", asset, err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close %s: %v", asset, err)
	}
	keep = true
	return temporaryPath, nil
}

func smartctlInstallerArguments(architecture, directory string) []string {
	componentArchitecture := "x64"
	if architecture == "386" {
		componentArchitecture = "x32"
	}
	return []string{"/S", "/SO", "smartctl,drivedb,doc," + componentArchitecture, "/D=" + filepath.Clean(directory)}
}

func smartctlExecutablePath(directory string) string {
	return smartctlExecutableIn(filepath.Join(directory, "smartmontools"))
}

func smartctlExecutableIn(smartmontoolsDirectory string) string {
	return filepath.Join(smartmontoolsDirectory, "bin", "smartctl.exe")
}
