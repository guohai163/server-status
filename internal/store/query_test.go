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

func TestHistoryQueriesPreaggregateResourceMetrics(t *testing.T) {
	for name, query := range map[string]string{"raw": rawHistorySQL, "hourly": hourlyHistorySQL} {
		if strings.Contains(query, "JOIN LATERAL") {
			t.Errorf("%s history query still performs per-point lateral aggregation", name)
		}
		for _, fragment := range []string{"GROUP BY filesystem_sample.", "GROUP BY network_sample."} {
			if !strings.Contains(query, fragment) {
				t.Errorf("%s history query does not contain %q", name, fragment)
			}
		}
	}
	if !strings.Contains(rawHistorySQL, "sum(disk_read_bytes_delta)") || !strings.Contains(rawHistorySQL, "sum(interval_seconds)") {
		t.Error("raw history query does not calculate weighted throughput across time buckets")
	}
}

func TestHistoryQueryResolution(t *testing.T) {
	tests := []struct {
		duration      time.Duration
		resolution    string
		bucketMinutes int
	}{
		{time.Hour, "minute", 1},
		{6 * time.Hour, "minute", 1},
		{24 * time.Hour, "5minute", 5},
		{7 * 24 * time.Hour, "hour", 0},
	}
	for _, test := range tests {
		spec := historyQueries(test.duration)
		if spec.resolution != test.resolution || spec.bucketMinutes != test.bucketMinutes {
			t.Errorf("duration %s selected resolution %q/%d, want %q/%d", test.duration, spec.resolution, spec.bucketMinutes, test.resolution, test.bucketMinutes)
		}
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
	for _, field := range []string{machineTypeLabelKey, systemModelLabelKey, "threads_per_package", "monitoring.gpu_devices", "node.tags"} {
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

func TestGPUHistoryQueriesUsePerDeviceRawAndHourlyMetrics(t *testing.T) {
	queries := map[string]struct {
		query string
		table string
		value string
	}{
		"raw": {
			query: rawGPUHistorySQL,
			table: "monitoring.gpu_metric_samples",
			value: "avg(memory_usage_percent)",
		},
		"hourly": {
			query: hourlyGPUHistorySQL,
			table: "monitoring.gpu_metric_hourly",
			value: "sample.memory_usage_avg",
		},
	}
	for name, item := range queries {
		for _, fragment := range []string{
			item.table,
			"gpu.id = sample.gpu_id",
			"sample.node_id = $1::uuid",
			item.value,
			"gpu.device_index",
		} {
			if !strings.Contains(item.query, fragment) {
				t.Errorf("%s GPU history query does not contain %q", name, fragment)
			}
		}
	}
}

func TestNodeDetailQueriesIncludeHardwareHealth(t *testing.T) {
	for _, fragment := range []string{
		"monitoring.storage_health_current_metrics",
		"health.read_error_rate_normalized",
		"health.uncorrectable_sectors",
		"device.raid_passthrough",
	} {
		if !strings.Contains(blockDeviceDetailSQL, fragment) {
			t.Errorf("block device detail query does not contain %q", fragment)
		}
	}
	for _, fragment := range []string{"monitoring.temperature_current_metrics", "temperature_celsius", "critical_celsius"} {
		if !strings.Contains(temperatureDetailSQL, fragment) {
			t.Errorf("temperature detail query does not contain %q", fragment)
		}
	}
}
