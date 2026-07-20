package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	appversion "github.com/guohai/server-status/internal/version"
)

const (
	maxUpdateBinaryBytes    = 64 << 20
	maxUpdateChecksumsBytes = 1 << 20
)

var ErrUpdateApplied = errors.New("agent update applied")

type Updater struct {
	serverURL       string
	currentVersion  string
	operatingSystem string
	architecture    string
	http            *http.Client
	executablePath  func() (string, error)
}

func NewUpdater(serverURL, currentVersion string) *Updater {
	return &Updater{
		serverURL:       strings.TrimRight(serverURL, "/"),
		currentVersion:  currentVersion,
		operatingSystem: runtime.GOOS,
		architecture:    runtime.GOARCH,
		http:            &http.Client{Timeout: 5 * time.Minute},
		executablePath:  os.Executable,
	}
}

func (updater *Updater) Apply(ctx context.Context, targetVersion string) error {
	rawTargetVersion := targetVersion
	targetVersion, ok := appversion.Normalize(rawTargetVersion)
	if !ok {
		return fmt.Errorf("invalid target version %q", rawTargetVersion)
	}
	comparison, ok := appversion.Compare(updater.currentVersion, targetVersion)
	if !ok {
		return fmt.Errorf("cannot compare current version %q with %q", updater.currentVersion, targetVersion)
	}
	if comparison >= 0 {
		return fmt.Errorf("target version %s is not newer than %s", targetVersion, updater.currentVersion)
	}
	asset, err := updateAssetName(updater.operatingSystem, updater.architecture)
	if err != nil {
		return err
	}
	checksums, err := updater.downloadBytes(ctx, targetVersion, "checksums.txt", maxUpdateChecksumsBytes)
	if err != nil {
		return err
	}
	expectedChecksum, err := updateChecksumForAsset(checksums, asset)
	if err != nil {
		return err
	}

	executablePath, err := updater.executablePath()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executablePath); resolveErr == nil {
		executablePath = resolved
	}
	temporary, err := os.CreateTemp(filepath.Dir(executablePath), ".server-status-agent-update-*")
	if err != nil {
		return fmt.Errorf("create update beside executable: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	actualChecksum, err := updater.downloadBinary(ctx, targetVersion, asset, temporary)
	if err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync downloaded update: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close downloaded update: %w", err)
	}
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("SHA-256 verification failed: expected %s, got %s", expectedChecksum, actualChecksum)
	}
	if err := os.Chmod(temporaryPath, 0o755); err != nil {
		return fmt.Errorf("make downloaded update executable: %w", err)
	}
	if err := verifyUpdateVersion(ctx, temporaryPath, targetVersion); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, executablePath); err != nil {
		return fmt.Errorf("replace agent executable: %w", err)
	}
	return nil
}

func (updater *Updater) downloadBytes(ctx context.Context, version, asset string, limit int64) ([]byte, error) {
	response, err := updater.request(ctx, version, asset)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("download %s exceeds %d bytes", asset, limit)
	}
	return content, nil
}

func (updater *Updater) downloadBinary(ctx context.Context, version, asset string, destination io.Writer) (string, error) {
	response, err := updater.request(ctx, version, asset)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, digest), io.LimitReader(response.Body, maxUpdateBinaryBytes+1))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	if written > maxUpdateBinaryBytes {
		return "", fmt.Errorf("download %s exceeds %d bytes", asset, maxUpdateBinaryBytes)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (updater *Updater) request(ctx context.Context, version, asset string) (*http.Response, error) {
	url := fmt.Sprintf("%s/agent/releases/v%s/%s", updater.serverURL, version, asset)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create update request: %w", err)
	}
	request.Header.Set("User-Agent", "server-status-agent/"+Version)
	response, err := updater.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("download %s: central returned %s", asset, response.Status)
	}
	return response, nil
}

func updateAssetName(operatingSystem, architecture string) (string, error) {
	if operatingSystem != "linux" {
		return "", fmt.Errorf("automatic updates do not support operating system %q", operatingSystem)
	}
	switch architecture {
	case "amd64", "arm64":
		return "server-status-agent-linux-" + architecture, nil
	default:
		return "", fmt.Errorf("automatic updates do not support architecture %q", architecture)
	}
}

func updateChecksumForAsset(content []byte, asset string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[1] != asset || len(fields[0]) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err == nil {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read update checksums: %w", err)
	}
	return "", fmt.Errorf("checksums.txt does not contain a valid digest for %s", asset)
}

func verifyUpdateVersion(ctx context.Context, executablePath, expectedVersion string) error {
	verifyContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(verifyContext, executablePath, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("run downloaded Agent version check: %w: %s", err, strings.TrimSpace(string(output)))
	}
	actualVersion, ok := appversion.Normalize(strings.TrimSpace(string(output)))
	if !ok || actualVersion != expectedVersion {
		return fmt.Errorf("downloaded Agent reports version %q, expected %q", strings.TrimSpace(string(output)), expectedVersion)
	}
	return nil
}
