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
	SecondsSinceLastSeen  int64             `json:"seconds_since_last_seen"`
	LatestBucketAt        *time.Time        `json:"latest_bucket_at,omitempty"`
	CPUUsagePercent       float64           `json:"cpu_usage_percent"`
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

type NodeDetail struct {
	Node        NodeSummary        `json:"node"`
	Filesystems []FilesystemStatus `json:"filesystems"`
	Network     []NetworkStatus    `json:"network"`
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
	return NodeDetail{Node: summary, Filesystems: filesystems, Network: network}, nil
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
		status.seconds_since_last_seen,
		status.latest_bucket_at,
		COALESCE(status.cpu_usage_percent, 0)::double precision,
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
		COALESCE(hardware.disk_models, ARRAY[]::text[])
	  FROM monitoring.v_node_status status
	  JOIN monitoring.v_node_hardware_summary hardware ON hardware.node_id = status.node_id
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
		&labelsJSON, &item.LastSeenAt, &item.Status, &item.SecondsSinceLastSeen,
		&item.LatestBucketAt, &item.CPUUsagePercent,
		&item.MemoryTotalBytes, &item.MemoryUsedBytes, &item.MemoryUsagePercent, &item.UptimeSeconds,
		&item.CPUPackageCount, &item.CPUPhysicalCoreCount, &item.CPULogicalThreadCount, &item.CPUModels,
		&item.MemoryModuleCount, &item.InventoryMemoryBytes, &item.MemoryModels,
		&item.DiskCount, &item.DiskTotalBytes, &item.DiskModels,
	)
	if err != nil {
		return NodeSummary{}, fmt.Errorf("scan node summary: %w", err)
	}
	if err := json.Unmarshal(labelsJSON, &item.Labels); err != nil {
		return NodeSummary{}, fmt.Errorf("decode node labels: %w", err)
	}
	return item, nil
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
