package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type NodeSummary struct {
	NodeID                string            `json:"node_id"`
	AgentID               string            `json:"agent_id"`
	Hostname              string            `json:"hostname"`
	DisplayName           string            `json:"display_name,omitempty"`
	OSName                string            `json:"os_name"`
	OSVersion             string            `json:"os_version,omitempty"`
	Architecture          string            `json:"architecture"`
	AgentVersion          string            `json:"agent_version"`
	Labels                map[string]string `json:"labels"`
	LastSeenAt            time.Time         `json:"last_seen_at"`
	Status                string            `json:"status"`
	PrimaryIP             string            `json:"primary_ip,omitempty"`
	SecondsSinceLastSeen  int64             `json:"seconds_since_last_seen"`
	LatestBucketAt        *time.Time        `json:"latest_bucket_at,omitempty"`
	CPUUsagePercent       float64           `json:"cpu_usage_percent"`
	Load1                 float64           `json:"load_1"`
	Load5                 float64           `json:"load_5"`
	Load15                float64           `json:"load_15"`
	MemoryTotalBytes      int64             `json:"memory_total_bytes"`
	MemoryUsedBytes       int64             `json:"memory_used_bytes"`
	MemoryUsagePercent    float64           `json:"memory_usage_percent"`
	UptimeSeconds         int64             `json:"uptime_seconds"`
	CPUPackageCount       int               `json:"cpu_package_count"`
	CPUPhysicalCoreCount  int               `json:"cpu_physical_core_count"`
	CPULogicalThreadCount int               `json:"cpu_logical_thread_count"`
	CPUModels             []string          `json:"cpu_models"`
	MemoryModuleCount     int               `json:"memory_module_count"`
	InventoryMemoryBytes  int64             `json:"inventory_memory_bytes"`
	MemoryModels          []string          `json:"memory_models"`
	DiskCount             int               `json:"disk_count"`
	DiskTotalBytes        int64             `json:"disk_total_bytes"`
	DiskModels            []string          `json:"disk_models"`
	DiskUsagePercent      float64           `json:"disk_usage_percent"`
	DiskReadBytesPerSec   float64           `json:"disk_read_bytes_per_second"`
	DiskWriteBytesPerSec  float64           `json:"disk_write_bytes_per_second"`
	NetworkRXBytesPerSec  float64           `json:"network_rx_bytes_per_second"`
	NetworkTXBytesPerSec  float64           `json:"network_tx_bytes_per_second"`
}

type FilesystemStatus struct {
	FilesystemID   string     `json:"filesystem_id"`
	DeviceName     string     `json:"device_name"`
	FilesystemType string     `json:"filesystem_type"`
	MountPoint     string     `json:"mount_point"`
	BucketAt       *time.Time `json:"bucket_at,omitempty"`
	TotalBytes     int64      `json:"total_bytes"`
	UsedBytes      int64      `json:"used_bytes"`
	AvailableBytes int64      `json:"available_bytes"`
	UsedPercent    float64    `json:"used_percent"`
}

type NetworkStatus struct {
	InterfaceID     string     `json:"interface_id"`
	Name            string     `json:"name"`
	MACAddress      string     `json:"mac_address,omitempty"`
	MTU             int        `json:"mtu"`
	LinkSpeedMbps   int        `json:"link_speed_mbps"`
	Addresses       []string   `json:"addresses"`
	BucketAt        *time.Time `json:"bucket_at,omitempty"`
	LinkUp          bool       `json:"link_up"`
	RXBytesTotal    int64      `json:"rx_bytes_total"`
	TXBytesTotal    int64      `json:"tx_bytes_total"`
	RXBitsPerSecond float64    `json:"rx_bits_per_second"`
	TXBitsPerSecond float64    `json:"tx_bits_per_second"`
}

type CPUHardware struct {
	PackageIndex    int     `json:"package_index"`
	Vendor          string  `json:"vendor,omitempty"`
	ModelName       string  `json:"model_name"`
	PhysicalCores   int     `json:"physical_cores"`
	LogicalThreads  int     `json:"logical_threads"`
	MaxFrequencyMHz float64 `json:"max_frequency_mhz"`
}

type MemoryHardware struct {
	SlotName     string `json:"slot_name,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	PartNumber   string `json:"part_number,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	MemoryType   string `json:"memory_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
	SpeedMTs     int    `json:"speed_mts"`
}

type BlockDeviceHardware struct {
	DeviceName   string `json:"device_name"`
	DeviceKind   string `json:"device_kind"`
	Vendor       string `json:"vendor,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	WWN          string `json:"wwn,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
	Rotational   *bool  `json:"rotational,omitempty"`
}

type NodeDetail struct {
	Node          NodeSummary           `json:"node"`
	CPUPackages   []CPUHardware         `json:"cpu_packages"`
	MemoryModules []MemoryHardware      `json:"memory_modules"`
	BlockDevices  []BlockDeviceHardware `json:"block_devices"`
	Filesystems   []FilesystemStatus    `json:"filesystems"`
	Network       []NetworkStatus       `json:"network"`
}

type HistoryPoint struct {
	BucketAt                time.Time `json:"bucket_at"`
	CPUUsagePercent         float64   `json:"cpu_usage_percent"`
	MemoryUsagePercent      float64   `json:"memory_usage_percent"`
	DiskUsagePercent        float64   `json:"disk_usage_percent"`
	DiskReadBytesPerSecond  float64   `json:"disk_read_bytes_per_second"`
	DiskWriteBytesPerSecond float64   `json:"disk_write_bytes_per_second"`
	NetworkRXBytesPerSecond float64   `json:"network_rx_bytes_per_second"`
	NetworkTXBytesPerSecond float64   `json:"network_tx_bytes_per_second"`
}

type NodeHistory struct {
	NodeID     string         `json:"node_id"`
	Range      string         `json:"range"`
	Resolution string         `json:"resolution"`
	Points     []HistoryPoint `json:"points"`
}

func (store *Store) ListNodes(ctx context.Context) ([]NodeSummary, error) {
	rows, err := store.pool.Query(ctx, nodeSummarySQL+` ORDER BY status.hostname`)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()
	result := make([]NodeSummary, 0)
	for rows.Next() {
		item, err := scanNodeSummary(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return result, nil
}

func (store *Store) GetNode(ctx context.Context, nodeID string) (NodeDetail, error) {
	row := store.pool.QueryRow(ctx, nodeSummarySQL+` WHERE status.node_id = $1::uuid`, nodeID)
	summary, err := scanNodeSummary(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeDetail{}, ErrNotFound
	}
	if err != nil {
		return NodeDetail{}, err
	}
	filesystems, err := store.listFilesystems(ctx, nodeID)
	if err != nil {
		return NodeDetail{}, err
	}
	network, err := store.listNetwork(ctx, nodeID)
	if err != nil {
		return NodeDetail{}, err
	}
	cpuPackages, err := store.listCPUPackages(ctx, nodeID)
	if err != nil {
		return NodeDetail{}, err
	}
	memoryModules, err := store.listMemoryModules(ctx, nodeID)
	if err != nil {
		return NodeDetail{}, err
	}
	blockDevices, err := store.listBlockDevices(ctx, nodeID)
	if err != nil {
		return NodeDetail{}, err
	}
	return NodeDetail{
		Node: summary, CPUPackages: cpuPackages, MemoryModules: memoryModules,
		BlockDevices: blockDevices, Filesystems: filesystems, Network: network,
	}, nil
}

const nodeSummarySQL = `
	SELECT
		status.node_id::text,
		status.agent_id::text,
		status.hostname,
		COALESCE(status.display_name, ''),
		status.os_name,
		COALESCE(status.os_version, ''),
		status.architecture,
		status.agent_version,
		status.labels,
		status.last_seen_at,
		status.status,
		COALESCE(primary_address.address, ''),
		status.seconds_since_last_seen,
		status.latest_bucket_at,
		COALESCE(status.cpu_usage_percent, 0)::double precision,
		COALESCE(status.load_1, 0)::double precision,
		COALESCE(status.load_5, 0)::double precision,
		COALESCE(status.load_15, 0)::double precision,
		COALESCE(status.memory_total_bytes, 0),
		COALESCE(status.memory_used_bytes, 0),
		COALESCE(status.memory_usage_percent, 0)::double precision,
		COALESCE(status.uptime_seconds, 0),
		hardware.cpu_package_count,
		hardware.cpu_physical_core_count,
		hardware.cpu_logical_thread_count,
		COALESCE(hardware.cpu_models, ARRAY[]::text[]),
		hardware.memory_module_count,
		hardware.memory_total_bytes,
		COALESCE(hardware.memory_models, ARRAY[]::text[]),
		hardware.disk_count,
		hardware.disk_total_bytes,
		COALESCE(hardware.disk_models, ARRAY[]::text[]),
		COALESCE(filesystem_usage.used_percent, 0)::double precision,
		COALESCE(current_metric.disk_read_bytes_per_second, 0)::double precision,
		COALESCE(current_metric.disk_write_bytes_per_second, 0)::double precision,
		COALESCE(network_usage.rx_bytes_per_second, 0)::double precision,
		COALESCE(network_usage.tx_bytes_per_second, 0)::double precision
	  FROM monitoring.v_node_status status
	  JOIN monitoring.v_node_hardware_summary hardware ON hardware.node_id = status.node_id
	  LEFT JOIN monitoring.node_current_metrics current_metric ON current_metric.node_id = status.node_id
	  LEFT JOIN LATERAL (
		SELECT host(address)::text AS address
		  FROM monitoring.network_addresses
		 WHERE node_id = status.node_id AND removed_at IS NULL
		 ORDER BY CASE address_scope WHEN 'global' THEN 0 WHEN 'private' THEN 1 WHEN 'link' THEN 2 ELSE 3 END,
		          family(address), address
		 LIMIT 1
	  ) primary_address ON true
	  LEFT JOIN LATERAL (
		SELECT max(filesystem_metric.used_percent) AS used_percent
		  FROM monitoring.filesystem_current_metrics filesystem_metric
		  JOIN monitoring.filesystems filesystem
		    ON filesystem.node_id = filesystem_metric.node_id
		   AND filesystem.id = filesystem_metric.filesystem_id
		 WHERE filesystem_metric.node_id = status.node_id
		   AND filesystem.removed_at IS NULL
		   AND NOT ('ro' = ANY(filesystem.mount_options))
	  ) filesystem_usage ON true
	  LEFT JOIN LATERAL (
		SELECT sum(rx_bits_per_second) / 8 AS rx_bytes_per_second,
		       sum(tx_bits_per_second) / 8 AS tx_bytes_per_second
		  FROM monitoring.network_current_metrics
		 WHERE node_id = status.node_id
	  ) network_usage ON true
`

type scanner interface {
	Scan(dest ...any) error
}

func scanNodeSummary(row scanner) (NodeSummary, error) {
	var item NodeSummary
	var labelsJSON []byte
	err := row.Scan(
		&item.NodeID, &item.AgentID, &item.Hostname, &item.DisplayName,
		&item.OSName, &item.OSVersion, &item.Architecture, &item.AgentVersion,
		&labelsJSON, &item.LastSeenAt, &item.Status, &item.PrimaryIP, &item.SecondsSinceLastSeen,
		&item.LatestBucketAt, &item.CPUUsagePercent, &item.Load1, &item.Load5, &item.Load15,
		&item.MemoryTotalBytes, &item.MemoryUsedBytes, &item.MemoryUsagePercent, &item.UptimeSeconds,
		&item.CPUPackageCount, &item.CPUPhysicalCoreCount, &item.CPULogicalThreadCount, &item.CPUModels,
		&item.MemoryModuleCount, &item.InventoryMemoryBytes, &item.MemoryModels,
		&item.DiskCount, &item.DiskTotalBytes, &item.DiskModels,
		&item.DiskUsagePercent, &item.DiskReadBytesPerSec, &item.DiskWriteBytesPerSec,
		&item.NetworkRXBytesPerSec, &item.NetworkTXBytesPerSec,
	)
	if err != nil {
		return NodeSummary{}, fmt.Errorf("scan node summary: %w", err)
	}
	if err := json.Unmarshal(labelsJSON, &item.Labels); err != nil {
		return NodeSummary{}, fmt.Errorf("decode node labels: %w", err)
	}
	normalizeNodeSummary(&item)
	return item, nil
}

func normalizeNodeSummary(item *NodeSummary) {
	if item.LatestBucketAt == nil && item.Status != "disabled" {
		item.Status = "pending"
	}
}

func (store *Store) listFilesystems(ctx context.Context, nodeID string) ([]FilesystemStatus, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT filesystem_id::text, device_name, filesystem_type, mount_point, bucket_at,
		       COALESCE(total_bytes, 0), COALESCE(used_bytes, 0), COALESCE(available_bytes, 0),
		       COALESCE(used_percent, 0)::double precision
		  FROM monitoring.v_filesystem_status
		 WHERE node_id = $1::uuid
		 ORDER BY mount_point
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query filesystem status: %w", err)
	}
	defer rows.Close()
	result := make([]FilesystemStatus, 0)
	for rows.Next() {
		var item FilesystemStatus
		if err := rows.Scan(&item.FilesystemID, &item.DeviceName, &item.FilesystemType, &item.MountPoint, &item.BucketAt, &item.TotalBytes, &item.UsedBytes, &item.AvailableBytes, &item.UsedPercent); err != nil {
			return nil, fmt.Errorf("scan filesystem status: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) listNetwork(ctx context.Context, nodeID string) ([]NetworkStatus, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT interface_id::text, interface_name, COALESCE(mac_address::text, ''),
		       COALESCE(mtu, 0), COALESCE(link_speed_mbps, 0), COALESCE(addresses, ARRAY[]::text[]),
		       bucket_at, COALESCE(link_up, false), COALESCE(rx_bytes_total, 0), COALESCE(tx_bytes_total, 0),
		       COALESCE(rx_bits_per_second, 0)::double precision,
		       COALESCE(tx_bits_per_second, 0)::double precision
		  FROM monitoring.v_network_status
		 WHERE node_id = $1::uuid
		 ORDER BY interface_name
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query network status: %w", err)
	}
	defer rows.Close()
	result := make([]NetworkStatus, 0)
	for rows.Next() {
		var item NetworkStatus
		if err := rows.Scan(&item.InterfaceID, &item.Name, &item.MACAddress, &item.MTU, &item.LinkSpeedMbps, &item.Addresses, &item.BucketAt, &item.LinkUp, &item.RXBytesTotal, &item.TXBytesTotal, &item.RXBitsPerSecond, &item.TXBitsPerSecond); err != nil {
			return nil, fmt.Errorf("scan network status: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) listCPUPackages(ctx context.Context, nodeID string) ([]CPUHardware, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT package_index, COALESCE(vendor, ''), model_name, physical_cores,
		       logical_threads, COALESCE(max_frequency_mhz, 0)::double precision
		  FROM monitoring.cpu_packages
		 WHERE node_id = $1::uuid AND removed_at IS NULL
		 ORDER BY package_index
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query CPU packages: %w", err)
	}
	defer rows.Close()
	result := make([]CPUHardware, 0)
	for rows.Next() {
		var item CPUHardware
		if err := rows.Scan(&item.PackageIndex, &item.Vendor, &item.ModelName, &item.PhysicalCores, &item.LogicalThreads, &item.MaxFrequencyMHz); err != nil {
			return nil, fmt.Errorf("scan CPU package: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) listMemoryModules(ctx context.Context, nodeID string) ([]MemoryHardware, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT COALESCE(slot_name, ''), COALESCE(manufacturer, ''), COALESCE(model_name, ''),
		       COALESCE(part_number, ''), COALESCE(serial_number, ''), COALESCE(memory_type, ''),
		       size_bytes, COALESCE(speed_mts, 0)
		  FROM monitoring.memory_modules
		 WHERE node_id = $1::uuid AND removed_at IS NULL
		 ORDER BY slot_name NULLS LAST, id
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query memory modules: %w", err)
	}
	defer rows.Close()
	result := make([]MemoryHardware, 0)
	for rows.Next() {
		var item MemoryHardware
		if err := rows.Scan(&item.SlotName, &item.Manufacturer, &item.ModelName, &item.PartNumber, &item.SerialNumber, &item.MemoryType, &item.SizeBytes, &item.SpeedMTs); err != nil {
			return nil, fmt.Errorf("scan memory module: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) listBlockDevices(ctx context.Context, nodeID string) ([]BlockDeviceHardware, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT device_name, device_kind, COALESCE(vendor, ''), COALESCE(model_name, ''),
		       COALESCE(serial_number, ''), COALESCE(wwn, ''), size_bytes, rotational
		  FROM monitoring.block_devices
		 WHERE node_id = $1::uuid AND removed_at IS NULL
		 ORDER BY device_name
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query block devices: %w", err)
	}
	defer rows.Close()
	result := make([]BlockDeviceHardware, 0)
	for rows.Next() {
		var item BlockDeviceHardware
		if err := rows.Scan(&item.DeviceName, &item.DeviceKind, &item.Vendor, &item.ModelName, &item.SerialNumber, &item.WWN, &item.SizeBytes, &item.Rotational); err != nil {
			return nil, fmt.Errorf("scan block device: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

var historyDurations = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
}

func ValidHistoryRange(value string) bool {
	_, ok := historyDurations[value]
	return ok
}

func (store *Store) GetNodeHistory(ctx context.Context, nodeID, window string) (NodeHistory, error) {
	duration, ok := historyDurations[window]
	if !ok {
		return NodeHistory{}, fmt.Errorf("invalid history range %q", window)
	}
	var exists bool
	if err := store.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM monitoring.nodes WHERE id = $1::uuid)`, nodeID).Scan(&exists); err != nil {
		return NodeHistory{}, fmt.Errorf("check history node: %w", err)
	}
	if !exists {
		return NodeHistory{}, ErrNotFound
	}

	start := time.Now().UTC().Add(-duration)
	resolution := "minute"
	query := rawHistorySQL
	if duration > 24*time.Hour {
		resolution = "hour"
		query = hourlyHistorySQL
	}
	rows, err := store.pool.Query(ctx, query, nodeID, start)
	if err != nil {
		return NodeHistory{}, fmt.Errorf("query node history: %w", err)
	}
	defer rows.Close()
	points := make([]HistoryPoint, 0)
	for rows.Next() {
		var point HistoryPoint
		if err := rows.Scan(
			&point.BucketAt, &point.CPUUsagePercent, &point.MemoryUsagePercent,
			&point.DiskUsagePercent, &point.DiskReadBytesPerSecond, &point.DiskWriteBytesPerSecond,
			&point.NetworkRXBytesPerSecond, &point.NetworkTXBytesPerSecond,
		); err != nil {
			return NodeHistory{}, fmt.Errorf("scan node history: %w", err)
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return NodeHistory{}, fmt.Errorf("iterate node history: %w", err)
	}
	return NodeHistory{NodeID: nodeID, Range: window, Resolution: resolution, Points: points}, nil
}

const rawHistorySQL = `
	SELECT sample.bucket_at,
	       sample.cpu_usage_percent::double precision,
	       CASE WHEN sample.memory_total_bytes = 0 THEN 0
	            ELSE sample.memory_used_bytes::double precision * 100 / sample.memory_total_bytes END,
	       COALESCE(filesystem.used_percent, 0)::double precision,
	       sample.disk_read_bytes_delta::double precision / sample.interval_seconds,
	       sample.disk_write_bytes_delta::double precision / sample.interval_seconds,
	       COALESCE(network.rx_bytes_delta, 0)::double precision / sample.interval_seconds,
	       COALESCE(network.tx_bytes_delta, 0)::double precision / sample.interval_seconds
	  FROM monitoring.node_metric_samples sample
	  LEFT JOIN LATERAL (
		SELECT max(filesystem_sample.used_percent) AS used_percent
		  FROM monitoring.filesystem_metric_samples filesystem_sample
		  JOIN monitoring.filesystems filesystem
		    ON filesystem.node_id = filesystem_sample.node_id
		   AND filesystem.id = filesystem_sample.filesystem_id
		 WHERE filesystem_sample.bucket_at = sample.bucket_at
		   AND filesystem_sample.node_id = sample.node_id
		   AND NOT ('ro' = ANY(filesystem.mount_options))
	  ) filesystem ON true
	  LEFT JOIN LATERAL (
		SELECT sum(rx_bytes_delta) AS rx_bytes_delta, sum(tx_bytes_delta) AS tx_bytes_delta
		  FROM monitoring.network_metric_samples network_sample
		 WHERE network_sample.bucket_at = sample.bucket_at
		   AND network_sample.node_id = sample.node_id
	  ) network ON true
	 WHERE sample.node_id = $1::uuid AND sample.bucket_at >= $2
	 ORDER BY sample.bucket_at
`

const hourlyHistorySQL = `
	SELECT sample.hour_at,
	       sample.cpu_usage_avg::double precision,
	       sample.memory_usage_avg::double precision,
	       COALESCE(filesystem.used_percent, 0)::double precision,
	       sample.disk_read_bytes_per_second_avg::double precision,
	       sample.disk_write_bytes_per_second_avg::double precision,
	       COALESCE(network.rx_bytes_per_second, 0)::double precision,
	       COALESCE(network.tx_bytes_per_second, 0)::double precision
	  FROM monitoring.node_metric_hourly sample
	  LEFT JOIN LATERAL (
		SELECT max(filesystem_sample.usage_avg) AS used_percent
		  FROM monitoring.filesystem_metric_hourly filesystem_sample
		  JOIN monitoring.filesystems filesystem
		    ON filesystem.node_id = filesystem_sample.node_id
		   AND filesystem.id = filesystem_sample.filesystem_id
		 WHERE filesystem_sample.hour_at = sample.hour_at
		   AND filesystem_sample.node_id = sample.node_id
		   AND NOT ('ro' = ANY(filesystem.mount_options))
	  ) filesystem ON true
	  LEFT JOIN LATERAL (
		SELECT sum(rx_bits_per_second_avg) / 8 AS rx_bytes_per_second,
		       sum(tx_bits_per_second_avg) / 8 AS tx_bytes_per_second
		  FROM monitoring.network_metric_hourly network_sample
		 WHERE network_sample.hour_at = sample.hour_at
		   AND network_sample.node_id = sample.node_id
	  ) network ON true
	 WHERE sample.node_id = $1::uuid AND sample.hour_at >= $2
	 ORDER BY sample.hour_at
`
