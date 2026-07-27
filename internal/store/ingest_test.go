package store

import (
	"strings"
	"testing"

	"github.com/guohai/server-status/internal/report"
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
		"RAID":        raidStatusSnapshotFreshnessSQL,
	} {
		if !strings.Contains(query, "bucket_at > $2") || !strings.Contains(query, "NOT EXISTS") {
			t.Errorf("%s freshness query does not protect newer snapshots", name)
		}
	}
}

func TestStorageCounterDeltaUsesAcceptedSnapshotAndPreservesRetryDelta(t *testing.T) {
	current := uint64(120)
	previous := int64(100)
	previousDelta := int64(5)
	if delta := storageCounterDelta(&current, &previous, &previousDelta, false); delta == nil || *delta != 20 {
		t.Fatalf("new snapshot delta = %v, want 20", delta)
	}
	if delta := storageCounterDelta(&current, &previous, &previousDelta, true); delta == nil || *delta != 25 {
		t.Fatalf("same-bucket retry delta = %v, want 25", delta)
	}
	reset := uint64(10)
	if delta := storageCounterDelta(&reset, &previous, &previousDelta, false); delta == nil || *delta != 0 {
		t.Fatalf("reset counter delta = %v, want 0", delta)
	}
	if delta := storageCounterDelta(&current, nil, nil, false); delta != nil {
		t.Fatalf("first snapshot established a false delta: %v", delta)
	}
	metric := report.StorageHealth{RiskLevel: "healthy"}
	addStorageWarning(&metric, "scsi_ecc_errors_increased")
	if metric.RiskLevel != "warning" || len(metric.RiskReasons) != 1 || metric.RiskReasons[0] != "scsi_ecc_errors_increased" {
		t.Fatalf("counter increase did not promote storage risk: %+v", metric)
	}
}
