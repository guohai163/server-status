package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultReleaseCacheDir   = "/tmp/server-status-release-cache"
	defaultBundledReleaseDir = "/usr/local/share/server-status/agent-releases"
	releaseUpstreamBase      = "https://github.com/guohai163/server-status/releases"
	maxReleaseAssetBytes     = 64 << 20
	maxChecksumsBytes        = 1 << 20
)

var releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$`)

type releaseCache struct {
	directory        string
	bundledDirectory string
	upstreamBase     string
	client           *http.Client
	logger           *slog.Logger
	latestTTL        time.Duration
	mu               sync.Mutex
}

func (api *API) releaseAsset(response http.ResponseWriter, request *http.Request) {
	api.releases.serve(response, request)
}

func newReleaseCache(directory string, logger *slog.Logger) *releaseCache {
	if strings.TrimSpace(directory) == "" {
		directory = defaultReleaseCacheDir
	}
	return &releaseCache{
		directory:        directory,
		bundledDirectory: defaultBundledReleaseDir,
		upstreamBase:     releaseUpstreamBase,
		client:           &http.Client{Timeout: 3 * time.Minute},
		logger:           logger,
		latestTTL:        10 * time.Minute,
	}
}

func (cache *releaseCache) serve(response http.ResponseWriter, request *http.Request) {
	version := request.PathValue("version")
	asset := request.PathValue("asset")
	if !validReleaseVersion(version) || !validReleaseAsset(asset) {
		http.NotFound(response, request)
		return
	}

	_ = http.NewResponseController(response).SetWriteDeadline(time.Now().Add(4 * time.Minute))
	path, hit, err := cache.assetPath(request.Context(), version, asset)
	if err != nil {
		cache.logger.Error("release cache request failed", "version", version, "asset", asset, "error", err)
		http.Error(response, "agent release asset is temporarily unavailable", http.StatusBadGateway)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		cache.logger.Error("open cached release asset failed", "version", version, "asset", asset, "error", err)
		http.Error(response, "agent release asset is temporarily unavailable", http.StatusBadGateway)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(response, "agent release asset is temporarily unavailable", http.StatusBadGateway)
		return
	}

	if asset == "checksums.txt" {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		response.Header().Set("Content-Type", "application/octet-stream")
	}
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, asset))
	cacheStatus := map[bool]string{true: "HIT", false: "MISS"}[hit]
	if cache.isBundledPath(path) {
		cacheStatus = "BUNDLED"
	}
	response.Header().Set("X-Server-Status-Cache", cacheStatus)
	if version == "latest" {
		response.Header().Set("Cache-Control", "public, max-age=300")
	} else {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(response, request, asset, info.ModTime(), file)
}

func (cache *releaseCache) assetPath(ctx context.Context, version, asset string) (string, bool, error) {
	if path, found, err := cache.bundledAssetPath(version, asset); found || err != nil {
		return path, true, err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	checksumsPath, checksumsHit, err := cache.ensureFile(ctx, version, "checksums.txt")
	if err != nil {
		return "", false, err
	}
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return "", false, fmt.Errorf("read cached checksums: %w", err)
	}
	if asset == "checksums.txt" {
		if _, err := parseReleaseChecksums(checksums); err != nil {
			_ = os.Remove(checksumsPath)
			return "", false, fmt.Errorf("validate checksums: %w", err)
		}
		return checksumsPath, checksumsHit, nil
	}

	expected, err := checksumForAsset(checksums, asset)
	if err != nil {
		_ = os.Remove(checksumsPath)
		return "", false, err
	}
	assetPath, assetHit, err := cache.ensureFile(ctx, version, asset)
	if err != nil {
		return "", false, err
	}
	if err := verifyReleaseAsset(assetPath, expected); err != nil {
		_ = os.Remove(assetPath)
		if version == "latest" {
			_ = os.Remove(checksumsPath)
			checksumsPath, _, err = cache.ensureFile(ctx, version, "checksums.txt")
			if err != nil {
				return "", false, err
			}
			checksums, err = os.ReadFile(checksumsPath)
			if err != nil {
				return "", false, err
			}
			expected, err = checksumForAsset(checksums, asset)
			if err != nil {
				return "", false, err
			}
		}
		assetPath, _, err = cache.ensureFile(ctx, version, asset)
		if err != nil {
			return "", false, err
		}
		if err := verifyReleaseAsset(assetPath, expected); err != nil {
			_ = os.Remove(assetPath)
			return "", false, err
		}
		return assetPath, false, nil
	}
	return assetPath, checksumsHit && assetHit, nil
}

func (cache *releaseCache) bundledAssetPath(version, asset string) (string, bool, error) {
	if strings.TrimSpace(cache.bundledDirectory) == "" {
		return "", false, nil
	}
	directory := filepath.Join(cache.bundledDirectory, version)
	checksumsPath := filepath.Join(directory, "checksums.txt")
	info, err := os.Stat(checksumsPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, fmt.Errorf("inspect bundled checksums: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", true, errors.New("bundled checksums file is empty or not regular")
	}
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return "", true, fmt.Errorf("read bundled checksums: %w", err)
	}
	if _, err := parseReleaseChecksums(checksums); err != nil {
		return "", true, fmt.Errorf("validate bundled checksums: %w", err)
	}
	if asset == "checksums.txt" {
		return checksumsPath, true, nil
	}
	expected, err := checksumForAsset(checksums, asset)
	if err != nil {
		return "", true, err
	}
	assetPath := filepath.Join(directory, asset)
	info, err = os.Stat(assetPath)
	if err != nil {
		return "", true, fmt.Errorf("inspect bundled %s: %w", asset, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", true, fmt.Errorf("bundled %s is empty or not regular", asset)
	}
	if err := verifyReleaseAsset(assetPath, expected); err != nil {
		return "", true, fmt.Errorf("verify bundled %s: %w", asset, err)
	}
	return assetPath, true, nil
}

func (cache *releaseCache) isBundledPath(path string) bool {
	if strings.TrimSpace(cache.bundledDirectory) == "" {
		return false
	}
	relative, err := filepath.Rel(cache.bundledDirectory, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (cache *releaseCache) ensureFile(ctx context.Context, version, asset string) (string, bool, error) {
	directory := filepath.Join(cache.directory, version)
	path := filepath.Join(directory, asset)
	staleAvailable := false
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		if version != "latest" || time.Since(info.ModTime()) < cache.latestTTL {
			return path, true, nil
		}
		staleAvailable = true
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("inspect release cache: %w", err)
	}

	if err := cache.downloadFile(ctx, version, asset, path); err != nil {
		if staleAvailable {
			cache.logger.Warn("release cache refresh failed; using stale asset", "version", version, "asset", asset, "error", err)
			return path, true, nil
		}
		return "", false, err
	}
	return path, false, nil
}

func (cache *releaseCache) downloadFile(ctx context.Context, version, asset, destination string) error {
	if err := os.MkdirAll(cache.directory, 0o750); err != nil {
		return fmt.Errorf("create release cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(cache.directory, ".download-*")
	if err != nil {
		return fmt.Errorf("create release cache file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	upstreamURL := cache.upstreamURL(version, asset)
	upstreamRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("create upstream request: %w", err)
	}
	upstreamResponse, err := cache.client.Do(upstreamRequest)
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("download %s: %w", asset, err)
	}
	defer upstreamResponse.Body.Close()
	if upstreamResponse.StatusCode != http.StatusOK {
		_ = temporary.Close()
		return fmt.Errorf("download %s: upstream returned %s", asset, upstreamResponse.Status)
	}

	limit := int64(maxReleaseAssetBytes)
	if asset == "checksums.txt" {
		limit = maxChecksumsBytes
	}
	written, copyErr := io.Copy(temporary, io.LimitReader(upstreamResponse.Body, limit+1))
	if copyErr != nil {
		_ = temporary.Close()
		return fmt.Errorf("cache %s: %w", asset, copyErr)
	}
	if written > limit {
		_ = temporary.Close()
		return fmt.Errorf("download %s exceeds %d bytes", asset, limit)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync cached %s: %w", asset, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close cached %s: %w", asset, err)
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return fmt.Errorf("set cached %s permissions: %w", asset, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create release version cache directory: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish cached %s: %w", asset, err)
	}
	cache.logger.Info("release asset cached", "version", version, "asset", asset, "bytes", written)
	return nil
}

func (cache *releaseCache) upstreamURL(version, asset string) string {
	if version == "latest" {
		return cache.upstreamBase + "/latest/download/" + asset
	}
	return cache.upstreamBase + "/download/" + version + "/" + asset
}

func validReleaseVersion(version string) bool {
	return version == "latest" || len(version) <= 64 && releaseVersionPattern.MatchString(version)
}

func validReleaseAsset(asset string) bool {
	switch asset {
	case "checksums.txt",
		"server-status-agent-linux-amd64", "server-status-agent-linux-arm64",
		"server-status-agent-macos",
		"server-status-agent-windows-386.exe", "server-status-agent-windows-amd64.exe",
		"server-status-smartctl-windows-setup.exe", "server-status-smartctl-source.tar.gz":
		return true
	default:
		return false
	}
}

func parseReleaseChecksums(content []byte) (map[string]string, error) {
	checksums := make(map[string]string, 8)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || !validReleaseAsset(fields[1]) || fields[1] == "checksums.txt" {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		checksums[fields[1]] = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, asset := range []string{"server-status-agent-linux-amd64", "server-status-agent-linux-arm64"} {
		if checksums[asset] == "" {
			return nil, fmt.Errorf("checksums.txt does not contain a valid digest for %s", asset)
		}
	}
	return checksums, nil
}

func checksumForAsset(content []byte, asset string) (string, error) {
	checksums, err := parseReleaseChecksums(content)
	if err != nil {
		return "", err
	}
	checksum := checksums[asset]
	if checksum == "" {
		return "", fmt.Errorf("checksums.txt does not contain %s", asset)
	}
	return checksum, nil
}

func verifyReleaseAsset(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open release asset for verification: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return fmt.Errorf("hash release asset: %w", err)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if actual != expected {
		return fmt.Errorf("SHA-256 verification failed: expected %s, got %s", expected, actual)
	}
	return nil
}
