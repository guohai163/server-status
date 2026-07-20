package store

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeNodeSummaryMarksNodesWithoutMetricsPending(t *testing.T) {
	item := NodeSummary{Status: "online"}
	normalizeNodeSummary(&item)
	if item.Status != "pending" {
		t.Fatalf("expected pending status, got %q", item.Status)
	}
}

func TestNormalizeNodeSummaryPreservesReportedAndDisabledStatus(t *testing.T) {
	now := time.Now()
	reported := NodeSummary{Status: "online", LatestBucketAt: &now}
	normalizeNodeSummary(&reported)
	if reported.Status != "online" {
		t.Fatalf("expected online status, got %q", reported.Status)
	}

	disabled := NodeSummary{Status: "disabled"}
	normalizeNodeSummary(&disabled)
	if disabled.Status != "disabled" {
		t.Fatalf("expected disabled status, got %q", disabled.Status)
	}
}

func TestDiskUsageQueriesExcludeReadOnlyFilesystems(t *testing.T) {
	queries := map[string]string{
		"current": nodeSummarySQL,
		"raw":     rawHistorySQL,
		"hourly":  hourlyHistorySQL,
	}
	for name, query := range queries {
		if !strings.Contains(query, "NOT ('ro' = ANY(filesystem.mount_options))") {
			t.Errorf("%s disk usage query does not exclude read-only filesystems", name)
		}
	}
	if !strings.Contains(nodeSummarySQL, "filesystem.removed_at IS NULL") {
		t.Error("current disk usage query does not exclude removed filesystems")
	}
}
