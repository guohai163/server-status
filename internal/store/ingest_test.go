package store

import (
	"strings"
	"testing"
)

func TestNonNilStringsConvertsMissingJSONArraysForPostgres(t *testing.T) {
	values := nonNilStrings(nil)
	if values == nil || len(values) != 0 {
		t.Fatalf("expected a non-nil empty slice, got %#v", values)
	}

	existing := []string{"rw"}
	values = nonNilStrings(existing)
	if len(values) != 1 || values[0] != "rw" {
		t.Fatalf("expected existing values to be preserved, got %#v", values)
	}
}

func TestPostgresMACAddressAcceptsOnlySixByteAddresses(t *testing.T) {
	if got := postgresMACAddress("00-11-22-33-44-55"); got != "00:11:22:33:44:55" {
		t.Fatalf("unexpected normalized MAC address %q", got)
	}
	if got := postgresMACAddress("00:00:00:00:00:00:00:e0"); got != "" {
		t.Fatalf("expected EUI-64 address to be omitted, got %q", got)
	}
	if got := postgresMACAddress("not-a-mac"); got != "" {
		t.Fatalf("expected invalid address to be omitted, got %q", got)
	}
}

func TestHardwareHealthFreshnessQueriesRejectOlderSnapshots(t *testing.T) {
	for name, query := range map[string]string{
		"temperature": temperatureSnapshotFreshnessSQL,
		"storage":     storageHealthSnapshotFreshnessSQL,
	} {
		if !strings.Contains(query, "bucket_at > $2") || !strings.Contains(query, "NOT EXISTS") {
			t.Errorf("%s freshness query does not protect newer snapshots", name)
		}
	}
}
