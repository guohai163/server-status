package server

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReleaseCacheDownloadsVerifiesAndReusesAssets(t *testing.T) {
	amd64 := []byte("linux-amd64-agent")
	arm64 := []byte("linux-arm64-agent")
	checksums := releaseChecksums(amd64, arm64)
	var mu sync.Mutex
	requests := make(map[string]int)
	upstream := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests[request.URL.Path]++
		mu.Unlock()
		switch request.URL.Path {
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = response.Write(checksums)
		case "/releases/download/v1.2.3/server-status-agent-linux-amd64":
			_, _ = response.Write(amd64)
		default:
			http.NotFound(response, request)
		}
	})

	api, _ := testAPI()
	cache := testReleaseCache(t, upstream)
	api.releases = cache
	path := "/agent/releases/v1.2.3/server-status-agent-linux-amd64"

	first := performRequest(api, path)
	if first.Code != http.StatusOK || first.Body.String() != string(amd64) {
		t.Fatalf("unexpected first response: status=%d body=%q", first.Code, first.Body.String())
	}
	if got := first.Header().Get("X-Server-Status-Cache"); got != "MISS" {
		t.Fatalf("expected first response to miss cache, got %q", got)
	}
	restartedAPI, _ := testAPI()
	restartedCache := testReleaseCache(t, upstream)
	restartedCache.directory = cache.directory
	restartedAPI.releases = restartedCache
	second := performRequest(restartedAPI, path)
	if second.Code != http.StatusOK || second.Header().Get("X-Server-Status-Cache") != "HIT" {
		t.Fatalf("expected second response to hit cache, got status=%d cache=%q", second.Code, second.Header().Get("X-Server-Status-Cache"))
	}

	mu.Lock()
	defer mu.Unlock()
	if requests["/releases/download/v1.2.3/checksums.txt"] != 1 || requests["/releases/download/v1.2.3/server-status-agent-linux-amd64"] != 1 {
		t.Fatalf("unexpected upstream requests: %+v", requests)
	}
}

func TestReleaseCacheUsesLatestReleaseURL(t *testing.T) {
	amd64 := []byte("latest-amd64-agent")
	arm64 := []byte("latest-arm64-agent")
	checksums := releaseChecksums(amd64, arm64)
	requested := make(map[string]bool)
	upstream := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requested[request.URL.Path] = true
		switch request.URL.Path {
		case "/releases/latest/download/checksums.txt":
			_, _ = response.Write(checksums)
		case "/releases/latest/download/server-status-agent-linux-arm64":
			_, _ = response.Write(arm64)
		default:
			http.NotFound(response, request)
		}
	})

	api, _ := testAPI()
	api.releases = testReleaseCache(t, upstream)
	response := performRequest(api, "/agent/releases/latest/server-status-agent-linux-arm64")
	if response.Code != http.StatusOK || response.Body.String() != string(arm64) {
		t.Fatalf("unexpected latest response: status=%d body=%q", response.Code, response.Body.String())
	}
	if !requested["/releases/latest/download/checksums.txt"] || !requested["/releases/latest/download/server-status-agent-linux-arm64"] {
		t.Fatalf("latest release URLs were not requested: %+v", requested)
	}
}

func TestReleaseCachePrefersBundledAssetsWithoutUpstream(t *testing.T) {
	amd64 := []byte("bundled-linux-amd64-agent")
	arm64 := []byte("bundled-linux-arm64-agent")
	windowsAMD64 := []byte("bundled-windows-amd64-agent")
	macos := []byte("bundled-macos-agent")
	checksums := append(releaseChecksums(amd64, arm64), []byte(fmt.Sprintf(
		"%x  server-status-agent-windows-amd64.exe\n%x  server-status-agent-macos\n",
		sha256.Sum256(windowsAMD64), sha256.Sum256(macos)))...)
	bundledRoot := t.TempDir()
	releaseDirectory := filepath.Join(bundledRoot, "v1.2.3")
	if err := os.MkdirAll(releaseDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"checksums.txt":                         checksums,
		"server-status-agent-linux-amd64":       amd64,
		"server-status-agent-linux-arm64":       arm64,
		"server-status-agent-macos":             macos,
		"server-status-agent-windows-amd64.exe": windowsAMD64,
	} {
		if err := os.WriteFile(filepath.Join(releaseDirectory, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("v1.2.3", filepath.Join(bundledRoot, "latest")); err != nil {
		t.Fatal(err)
	}

	upstreamRequests := 0
	cache := testReleaseCache(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		upstreamRequests++
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	cache.bundledDirectory = bundledRoot
	api, _ := testAPI()
	api.releases = cache
	for _, version := range []string{"v1.2.3", "latest"} {
		for asset, content := range map[string][]byte{
			"checksums.txt":                         checksums,
			"server-status-agent-linux-amd64":       amd64,
			"server-status-agent-macos":             macos,
			"server-status-agent-windows-amd64.exe": windowsAMD64,
		} {
			response := performRequest(api, "/agent/releases/"+version+"/"+asset)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), content) {
				t.Fatalf("unexpected bundled %s/%s response: status=%d body=%q", version, asset, response.Code, response.Body.String())
			}
			if got := response.Header().Get("X-Server-Status-Cache"); got != "BUNDLED" {
				t.Fatalf("expected bundled response header for %s/%s, got %q", version, asset, got)
			}
		}
	}
	if upstreamRequests != 0 {
		t.Fatalf("bundled assets unexpectedly used upstream %d times", upstreamRequests)
	}
}

func TestReleaseCacheUsesStaleLatestAssetWhenGitHubIsUnavailable(t *testing.T) {
	amd64 := []byte("cached-amd64-agent")
	arm64 := []byte("cached-arm64-agent")
	checksums := releaseChecksums(amd64, arm64)
	available := true
	upstream := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !available {
			http.Error(response, "unavailable", http.StatusServiceUnavailable)
			return
		}
		switch request.URL.Path {
		case "/releases/latest/download/checksums.txt":
			_, _ = response.Write(checksums)
		case "/releases/latest/download/server-status-agent-linux-amd64":
			_, _ = response.Write(amd64)
		default:
			http.NotFound(response, request)
		}
	})

	api, _ := testAPI()
	cache := testReleaseCache(t, upstream)
	api.releases = cache
	path := "/agent/releases/latest/server-status-agent-linux-amd64"
	if response := performRequest(api, path); response.Code != http.StatusOK {
		t.Fatalf("expected initial cache fill to succeed, got %d", response.Code)
	}

	available = false
	cache.latestTTL = -1
	response := performRequest(api, path)
	if response.Code != http.StatusOK || response.Body.String() != string(amd64) {
		t.Fatalf("expected stale cache fallback, got status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Server-Status-Cache") != "HIT" {
		t.Fatalf("expected stale response to be served from cache, got %q", response.Header().Get("X-Server-Status-Cache"))
	}
}

func TestReleaseCacheRejectsChecksumMismatch(t *testing.T) {
	amd64 := []byte("tampered-agent")
	arm64 := []byte("arm64-agent")
	checksums := []byte(fmt.Sprintf("%064x  server-status-agent-linux-amd64\n%x  server-status-agent-linux-arm64\n", 0, sha256.Sum256(arm64)))
	upstream := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch filepath.Base(request.URL.Path) {
		case "checksums.txt":
			_, _ = response.Write(checksums)
		case "server-status-agent-linux-amd64":
			_, _ = response.Write(amd64)
		default:
			http.NotFound(response, request)
		}
	})

	api, _ := testAPI()
	cache := testReleaseCache(t, upstream)
	api.releases = cache
	response := performRequest(api, "/agent/releases/v1.2.3/server-status-agent-linux-amd64")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("expected checksum mismatch to return 502, got %d", response.Code)
	}
	if _, err := os.Stat(filepath.Join(cache.directory, "v1.2.3", "server-status-agent-linux-amd64")); !os.IsNotExist(err) {
		t.Fatalf("mismatched asset must not remain cached, stat error: %v", err)
	}
}

func TestReleaseCacheRejectsUnknownVersionsAndAssets(t *testing.T) {
	api, _ := testAPI()
	for _, path := range []string{
		"/agent/releases/main/server-status-agent-linux-amd64",
		"/agent/releases/v1.2.3/arbitrary-file",
	} {
		response := performRequest(api, path)
		if response.Code != http.StatusNotFound {
			t.Errorf("expected %s to return 404, got %d", path, response.Code)
		}
	}

	response := performRequest(api, "/agent/releases/v1.2.3/../checksums.txt")
	if response.Code < 300 || response.Code >= 400 {
		t.Errorf("expected path traversal to be redirected, got %d", response.Code)
	}
}

func TestReleaseCacheServesWindowsAgent(t *testing.T) {
	amd64 := []byte("linux-amd64-agent")
	arm64 := []byte("linux-arm64-agent")
	windows := []byte("windows-386-agent")
	checksums := append(releaseChecksums(amd64, arm64), []byte(fmt.Sprintf(
		"%x  server-status-agent-windows-386.exe\n", sha256.Sum256(windows)))...)
	cache := testReleaseCache(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = response.Write(checksums)
		case "/releases/download/v1.2.3/server-status-agent-windows-386.exe":
			_, _ = response.Write(windows)
		default:
			http.NotFound(response, request)
		}
	}))
	api, _ := testAPI()
	api.releases = cache
	response := performRequest(api, "/agent/releases/v1.2.3/server-status-agent-windows-386.exe")
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), windows) {
		t.Fatalf("unexpected Windows asset response: status=%d body=%q", response.Code, response.Body.Bytes())
	}
}

func TestReleaseCacheServesMacOSAgent(t *testing.T) {
	amd64 := []byte("linux-amd64-agent")
	arm64 := []byte("linux-arm64-agent")
	macos := []byte("#!/bin/zsh\nprint macos-agent\n")
	checksums := append(releaseChecksums(amd64, arm64), []byte(fmt.Sprintf(
		"%x  server-status-agent-macos\n", sha256.Sum256(macos)))...)
	cache := testReleaseCache(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = response.Write(checksums)
		case "/releases/download/v1.2.3/server-status-agent-macos":
			_, _ = response.Write(macos)
		default:
			http.NotFound(response, request)
		}
	}))
	api, _ := testAPI()
	api.releases = cache
	response := performRequest(api, "/agent/releases/v1.2.3/server-status-agent-macos")
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), macos) {
		t.Fatalf("unexpected macOS asset response: status=%d body=%q", response.Code, response.Body.Bytes())
	}
}

func testReleaseCache(t *testing.T, upstream http.Handler) *releaseCache {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache := newReleaseCache(t.TempDir(), logger)
	cache.upstreamBase = "https://release.test/releases"
	cache.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response := httptest.NewRecorder()
		upstream.ServeHTTP(response, request)
		return response.Result(), nil
	})}
	return cache
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func performRequest(api *API, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, request)
	return response
}

func releaseChecksums(amd64, arm64 []byte) []byte {
	return []byte(fmt.Sprintf(
		"%x  server-status-agent-linux-amd64\n%x  server-status-agent-linux-arm64\n",
		sha256.Sum256(amd64), sha256.Sum256(arm64),
	))
}
