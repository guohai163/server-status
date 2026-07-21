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

func TestNodeSummaryQueryIncludesLoadAverages(t *testing.T) {
	for _, field := range []string{"status.load_1", "status.load_5", "status.load_15"} {
		if !strings.Contains(nodeSummarySQL, field) {
			t.Errorf("node summary query does not select %s", field)
		}
	}
}

func TestNodeSummaryQueryIncludesMachineAndPackageDetails(t *testing.T) {
	for _, field := range []string{machineTypeLabelKey, "threads_per_package", "monitoring.gpu_devices"} {
		if !strings.Contains(nodeSummarySQL, field) {
			t.Errorf("node summary query does not include %s", field)
		}
	}
}

func TestNodeSummaryQueryPrefersReportedBridgeIP(t *testing.T) {
	expected := "NULLIF(status.labels->>'" + primaryIPLabelKey + "', '')"
	if !strings.Contains(nodeSummarySQL, expected) {
		t.Error("node summary query does not prefer the Agent-reported bridge IP")
	}
}

func TestNodeSummaryQueryPrioritizesPreferredNetworkInterface(t *testing.T) {
	for _, fragment := range []string{
		"monitoring.node_network_preferences",
		"network_interface.id = network_preference.interface_id",
		"network_interface.removed_at IS NULL",
		"network_address.address AS sort_address",
		"primary_address.is_preferred",
	} {
		if !strings.Contains(nodeSummarySQL, fragment) {
			t.Errorf("node summary query does not contain %q", fragment)
		}
	}
	if !strings.Contains(nodeListOrderSQL, "primary_address.is_preferred") ||
		!strings.Contains(nodeListOrderSQL, "primary_address.sort_address") ||
		!strings.Contains(nodeListOrderSQL, primaryIPLabelKey) {
		t.Error("node list is not ordered by the selected dashboard IP")
	}
}
