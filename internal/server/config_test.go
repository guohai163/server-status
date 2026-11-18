package server

import (
	"strings"
	"testing"
)

func TestReleaseCacheDirectoryConfiguration(t *testing.T) {
	t.Setenv("SERVER_STATUS_DATABASE_URL", "postgres://example")
	t.Setenv("SERVER_STATUS_ADMIN_TOKEN", testAdminToken)
	t.Setenv("SERVER_STATUS_WEB_ACCESS_KEY", testWebAccessKey)
	t.Setenv("SERVER_STATUS_RELEASE_CACHE_DIR", "")
	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.ReleaseCacheDir != defaultReleaseCacheDir {
		t.Fatalf("unexpected default release cache directory: %s", config.ReleaseCacheDir)
	}

	t.Setenv("SERVER_STATUS_RELEASE_CACHE_DIR", "/var/cache/server-status")
	config, err = ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.ReleaseCacheDir != "/var/cache/server-status" {
		t.Fatalf("unexpected configured release cache directory: %s", config.ReleaseCacheDir)
	}
}

func TestWebAccessKeyConfiguration(t *testing.T) {
	t.Setenv("SERVER_STATUS_DATABASE_URL", "postgres://example")
	t.Setenv("SERVER_STATUS_ADMIN_TOKEN", testAdminToken)
	for _, key := range []string{"", "too-short", testAdminToken} {
		t.Setenv("SERVER_STATUS_WEB_ACCESS_KEY", key)
		if _, err := ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "SERVER_STATUS_WEB_ACCESS_KEY") {
			t.Fatalf("expected web access key validation error, got %v", err)
		}
	}
	t.Setenv("SERVER_STATUS_WEB_ACCESS_KEY", testWebAccessKey)
	config, err := ConfigFromEnv()
	if err != nil || config.WebAccessKey != testWebAccessKey {
		t.Fatalf("valid web access key configuration rejected: %v", err)
	}
}
