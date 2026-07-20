package server

import "testing"

func TestReleaseCacheDirectoryConfiguration(t *testing.T) {
	t.Setenv("SERVER_STATUS_DATABASE_URL", "postgres://example")
	t.Setenv("SERVER_STATUS_ADMIN_TOKEN", testAdminToken)
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
