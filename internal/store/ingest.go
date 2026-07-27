package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sort"
	"time"

	"github.com/guohai/server-status/internal/report"
	"github.com/jackc/pgx/v5"
)

func (store *Store) Ingest(ctx context.Context, auth NodeAuth, payload report.Report) error {
	receivedAt := time.Now().UTC()
	bucketAt := payload.CollectedAt.UTC().Truncate(time.Minute)
	agentLabels := make(map[string]string, len(payload.Agent.Labels)+3)
	for key, value := range payload.Agent.Labels {
		agentLabels[key] = value
	}
	if payload.Agent.MachineType != "" {
		agentLabels[machineTypeLabelKey] = payload.Agent.MachineType
	}
	if payload.Agent.SystemModel != "" {
		agentLabels[systemModelLabelKey] = payload.Agent.SystemModel
	}
	if payload.Agent.PrimaryIP != "" {
		agentLabels[primaryIPLabelKey] = payload.Agent.PrimaryIP
	}
	labels, err := json.Marshal(agentLabels)
	if err != nil {
		return fmt.Errorf("encode agent labels: %w", err)
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin report transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentFingerprint string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(inventory_fingerprint, '')
		  FROM monitoring.nodes
		 WHERE id = $1::uuid
		 FOR UPDATE
	`, auth.NodeID).Scan(&currentFingerprint); err != nil {
		return fmt.Errorf("lock reporting node: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE monitoring.nodes
		   SET hostname = $2,
		       os_name = $3,
		       os_version = NULLIF($4, ''),
		       kernel_version = NULLIF($5, ''),
		       architecture = $6,
		       agent_version = $7,
		       labels = $8::jsonb,
		       inventory_fingerprint = $9,
		       last_seen_at = $10,
		       updated_at = $10
		 WHERE id = $1::uuid
	`, auth.NodeID, payload.Agent.Hostname, payload.Agent.OSName, payload.Agent.OSVersion,
		payload.Agent.KernelVersion, payload.Agent.Architecture, payload.Agent.AgentVersion,
		labels, payload.InventoryFingerprint, receivedAt); err != nil {
		return fmt.Errorf("update reporting node: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE monitoring.node_api_tokens SET last_used_at = $2 WHERE id = $1::uuid
	`, auth.TokenID, receivedAt); err != nil {
		return fmt.Errorf("update token usage: %w", err)
	}
	if currentFingerprint != payload.InventoryFingerprint {
		if err := syncInventory(ctx, tx, auth.NodeID, payload.Inventory, receivedAt); err != nil {
			return err
		}
	}
	filesystemIDs, err := activeFilesystemIDs(ctx, tx, auth.NodeID)
	if err != nil {
		return err
	}
	interfaceIDs, err := activeInterfaceIDs(ctx, tx, auth.NodeID)
	if err != nil {
		return err
	}
	gpuIDs, err := activeGPUIDs(ctx, tx, auth.NodeID)
	if err != nil {
		return err
	}
	blockDeviceIDs, err := activeBlockDeviceIDs(ctx, tx, auth.NodeID)
	if err != nil {
		return err
	}

	memory := payload.Metrics.Memory
	cpu := payload.Metrics.CPU
	disk := payload.Metrics.Disk
	if _, err := tx.Exec(ctx, `
		INSERT INTO monitoring.node_metric_samples (
			bucket_at, node_id, collected_at, received_at, interval_seconds,
			cpu_usage_percent, load_1, load_5, load_15,
			memory_total_bytes, memory_used_bytes, memory_available_bytes,
			memory_cached_bytes, memory_buffers_bytes,
			swap_total_bytes, swap_used_bytes, uptime_seconds,
			disk_read_bytes_total, disk_write_bytes_total,
			disk_read_bytes_delta, disk_write_bytes_delta,
			disk_read_ops_delta, disk_write_ops_delta
		) VALUES (
			$1, $2::uuid, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22, $23
		)
		ON CONFLICT (bucket_at, node_id) DO UPDATE SET
			collected_at = EXCLUDED.collected_at,
			received_at = EXCLUDED.received_at,
			interval_seconds = EXCLUDED.interval_seconds,
			cpu_usage_percent = EXCLUDED.cpu_usage_percent,
			load_1 = EXCLUDED.load_1,
			load_5 = EXCLUDED.load_5,
			load_15 = EXCLUDED.load_15,
			memory_total_bytes = EXCLUDED.memory_total_bytes,
			memory_used_bytes = EXCLUDED.memory_used_bytes,
			memory_available_bytes = EXCLUDED.memory_available_bytes,
			memory_cached_bytes = EXCLUDED.memory_cached_bytes,
			memory_buffers_bytes = EXCLUDED.memory_buffers_bytes,
			swap_total_bytes = EXCLUDED.swap_total_bytes,
			swap_used_bytes = EXCLUDED.swap_used_bytes,
			uptime_seconds = EXCLUDED.uptime_seconds,
			disk_read_bytes_total = EXCLUDED.disk_read_bytes_total,
			disk_write_bytes_total = EXCLUDED.disk_write_bytes_total,
			disk_read_bytes_delta = EXCLUDED.disk_read_bytes_delta,
			disk_write_bytes_delta = EXCLUDED.disk_write_bytes_delta,
			disk_read_ops_delta = EXCLUDED.disk_read_ops_delta,
			disk_write_ops_delta = EXCLUDED.disk_write_ops_delta
	`, bucketAt, auth.NodeID, payload.CollectedAt.UTC(), receivedAt, payload.IntervalSeconds,
		cpu.UsagePercent, cpu.Load1, cpu.Load5, cpu.Load15,
		i64(memory.TotalBytes), i64(memory.UsedBytes), i64(memory.AvailableBytes),
		i64(memory.CachedBytes), i64(memory.BuffersBytes),
		i64(memory.SwapTotalBytes), i64(memory.SwapUsedBytes), i64(memory.UptimeSeconds),
		i64(disk.ReadBytesTotal), i64(disk.WriteBytesTotal),
		i64(disk.ReadBytesDelta), i64(disk.WriteBytesDelta),
		i64(disk.ReadOpsDelta), i64(disk.WriteOpsDelta)); err != nil {
		return fmt.Errorf("upsert node sample: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitoring.filesystem_metric_samples WHERE bucket_at = $1 AND node_id = $2::uuid`, bucketAt, auth.NodeID); err != nil {
		return fmt.Errorf("replace filesystem samples: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitoring.network_metric_samples WHERE bucket_at = $1 AND node_id = $2::uuid`, bucketAt, auth.NodeID); err != nil {
		return fmt.Errorf("replace network samples: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitoring.gpu_metric_samples WHERE bucket_at = $1 AND node_id = $2::uuid`, bucketAt, auth.NodeID); err != nil {
		return fmt.Errorf("replace GPU samples: %w", err)
	}
	for _, metric := range payload.Metrics.Filesystems {
		filesystemID, ok := filesystemIDs[metric.FilesystemKey]
		if !ok {
			return fmt.Errorf("%w: filesystem %q", ErrInvalidResource, metric.FilesystemKey)
		}
		var totalInodes, usedInodes any
		if metric.TotalInodes != 0 {
			totalInodes = i64(metric.TotalInodes)
			usedInodes = i64(metric.UsedInodes)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO monitoring.filesystem_metric_samples (
				bucket_at, node_id, filesystem_id,
				total_bytes, used_bytes, available_bytes, total_inodes, used_inodes
			) VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)
		`, bucketAt, auth.NodeID, filesystemID, i64(metric.TotalBytes), i64(metric.UsedBytes),
			i64(metric.AvailableBytes), totalInodes, usedInodes); err != nil {
			return fmt.Errorf("insert filesystem sample %q: %w", metric.FilesystemKey, err)
		}
	}
	for _, metric := range payload.Metrics.Network {
		interfaceID, ok := interfaceIDs[metric.InterfaceKey]
		if !ok {
			return fmt.Errorf("%w: network interface %q", ErrInvalidResource, metric.InterfaceKey)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO monitoring.network_metric_samples (
				bucket_at, node_id, interface_id, link_up,
				rx_bytes_total, tx_bytes_total, rx_bytes_delta, tx_bytes_delta,
				rx_packets_delta, tx_packets_delta, rx_errors_delta, tx_errors_delta,
				rx_dropped_delta, tx_dropped_delta
			) VALUES (
				$1, $2::uuid, $3::uuid, $4,
				$5, $6, $7, $8, $9, $10, $11, $12, $13, $14
			)
		`, bucketAt, auth.NodeID, interfaceID, metric.LinkUp,
			i64(metric.RXBytesTotal), i64(metric.TXBytesTotal), i64(metric.RXBytesDelta), i64(metric.TXBytesDelta),
			i64(metric.RXPacketsDelta), i64(metric.TXPacketsDelta), i64(metric.RXErrorsDelta), i64(metric.TXErrorsDelta),
			i64(metric.RXDroppedDelta), i64(metric.TXDroppedDelta)); err != nil {
			return fmt.Errorf("insert network sample %q: %w", metric.InterfaceKey, err)
		}
	}
	for _, metric := range payload.Metrics.GPUs {
		gpu, ok := gpuIDs[metric.GPUKey]
		if !ok {
			return fmt.Errorf("%w: GPU %q", ErrInvalidResource, metric.GPUKey)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO monitoring.gpu_metric_samples (
				bucket_at, node_id, gpu_id, utilization_percent,
				memory_total_bytes, memory_used_bytes
			) VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6)
		`, bucketAt, auth.NodeID, gpu.ID, metric.UtilizationPercent,
			gpu.MemoryTotalBytes, i64(metric.MemoryUsedBytes)); err != nil {
			return fmt.Errorf("insert GPU sample %q: %w", metric.GPUKey, err)
		}
	}
	if err := replaceTemperatureMetrics(ctx, tx, auth.NodeID, bucketAt, payload.CollectedAt.UTC(), receivedAt, payload.Metrics.Temperatures); err != nil {
		return err
	}
	if err := replaceStorageHealthMetrics(ctx, tx, auth.NodeID, bucketAt, payload.CollectedAt.UTC(), receivedAt, blockDeviceIDs, payload.Metrics.StorageHealth); err != nil {
		return err
	}
	if err := replaceRAIDStatus(ctx, tx, auth.NodeID, bucketAt, payload.CollectedAt.UTC(), receivedAt, payload.Metrics.RAIDControllers); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit report: %w", err)
	}
	return nil
}

func replaceRAIDStatus(ctx context.Context, tx pgx.Tx, nodeID string, bucketAt, collectedAt, receivedAt time.Time, controllers []report.RAIDControllerStatus) error {
	replace, err := currentSnapshotCanBeReplaced(ctx, tx, raidStatusSnapshotFreshnessSQL, nodeID, bucketAt)
	if err != nil {
		return fmt.Errorf("check RAID status freshness: %w", err)
	}
	if !replace {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitoring.raid_status_current_snapshots WHERE node_id = $1::uuid`, nodeID); err != nil {
		return fmt.Errorf("replace RAID status snapshot: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO monitoring.raid_status_current_snapshots (
			node_id, bucket_at, collected_at, received_at
		) VALUES ($1::uuid, $2, $3, $4)
	`, nodeID, bucketAt, collectedAt, receivedAt); err != nil {
		return fmt.Errorf("insert RAID status snapshot: %w", err)
	}
	for _, controller := range controllers {
		if _, err := tx.Exec(ctx, `
			INSERT INTO monitoring.raid_controller_current_metrics (
				node_id, controller_id, model, state, physical_drive_count,
				drive_group_count, virtual_drive_count
			) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
		`, nodeID, controller.ControllerID, controller.Model, controller.State,
			controller.PhysicalDriveCount, controller.DriveGroupCount, controller.VirtualDriveCount); err != nil {
			return fmt.Errorf("insert RAID controller %d: %w", controller.ControllerID, err)
		}
		for _, drive := range controller.VirtualDrives {
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.raid_virtual_drive_current_metrics (
					node_id, controller_id, drive_group_id, virtual_drive_id,
					raid_level, state, access, consistency, cache, size_bytes, name
				) VALUES (
					$1::uuid, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''),
					NULLIF($9, ''), $10, NULLIF($11, '')
				)
			`, nodeID, controller.ControllerID, drive.DriveGroupID, drive.VirtualDriveID,
				drive.RAIDLevel, drive.State, drive.Access, drive.Consistency, drive.Cache,
				i64(drive.SizeBytes), drive.Name); err != nil {
				return fmt.Errorf("insert RAID virtual drive %d/%d: %w", drive.DriveGroupID, drive.VirtualDriveID, err)
			}
		}
		for _, drive := range controller.PhysicalDrives {
			enclosureID := -1
			if drive.EnclosureID != nil {
				enclosureID = *drive.EnclosureID
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.raid_physical_drive_current_metrics (
					node_id, controller_id, enclosure_id, slot, device_id, state,
					drive_group_id, size_bytes, interface, media_type, sector_size_bytes,
					model, spun, drive_type, rebuild_progress, rebuild_status, estimated_time_left
				) VALUES (
					$1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
					$12, NULLIF($13, ''), NULLIF($14, ''), $15, NULLIF($16, ''), NULLIF($17, '')
				)
			`, nodeID, controller.ControllerID, enclosureID, drive.Slot, drive.DeviceID,
				drive.State, drive.DriveGroupID, i64(drive.SizeBytes), drive.Interface,
				drive.MediaType, nullableI64Value(drive.SectorSizeBytes), drive.Model,
				drive.Spun, drive.DriveType, drive.RebuildProgress, drive.RebuildStatus,
				drive.EstimatedTimeLeft); err != nil {
				return fmt.Errorf("insert RAID physical drive in slot %d: %w", drive.Slot, err)
			}
		}
	}
	return nil
}

func nullableI64Value(value uint64) any {
	if value == 0 {
		return nil
	}
	return i64(value)
}

func replaceTemperatureMetrics(ctx context.Context, tx pgx.Tx, nodeID string, bucketAt, collectedAt, receivedAt time.Time, metrics []report.TemperatureMetrics) error {
	replace, err := currentSnapshotCanBeReplaced(ctx, tx, temperatureSnapshotFreshnessSQL, nodeID, bucketAt)
	if err != nil {
		return fmt.Errorf("check temperature metric freshness: %w", err)
	}
	if !replace {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitoring.temperature_current_metrics WHERE node_id = $1::uuid`, nodeID); err != nil {
		return fmt.Errorf("replace temperature metrics: %w", err)
	}
	for _, metric := range metrics {
		if _, err := tx.Exec(ctx, `
			INSERT INTO monitoring.temperature_current_metrics (
				node_id, sensor_key, component, label, bucket_at, collected_at, received_at,
				temperature_celsius, high_celsius, critical_celsius
			) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, nodeID, metric.Key, metric.Component, metric.Label, bucketAt, collectedAt, receivedAt,
			metric.TemperatureCelsius, metric.HighCelsius, metric.CriticalCelsius); err != nil {
			return fmt.Errorf("insert temperature metric %q: %w", metric.Key, err)
		}
	}
	return nil
}

func replaceStorageHealthMetrics(ctx context.Context, tx pgx.Tx, nodeID string, bucketAt, collectedAt, receivedAt time.Time, blockDeviceIDs map[string]string, metrics []report.StorageHealth) error {
	replace, err := currentSnapshotCanBeReplaced(ctx, tx, storageHealthSnapshotFreshnessSQL, nodeID, bucketAt)
	if err != nil {
		return fmt.Errorf("check storage health metric freshness: %w", err)
	}
	if !replace {
		return nil
	}
	previous, err := loadStorageHealthCounters(ctx, tx, nodeID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM monitoring.storage_health_current_metrics WHERE node_id = $1::uuid`, nodeID); err != nil {
		return fmt.Errorf("replace storage health metrics: %w", err)
	}
	for _, metric := range metrics {
		blockDeviceID, ok := blockDeviceIDs[metric.BlockDeviceKey]
		if !ok {
			return fmt.Errorf("%w: block device %q", ErrInvalidResource, metric.BlockDeviceKey)
		}
		prior := previous[blockDeviceID]
		sameBucket := !prior.BucketAt.IsZero() && prior.BucketAt.Equal(bucketAt)
		eccDelta := storageCounterDelta(metric.SCSIECCCorrected, prior.SCSIECCCorrected, prior.SCSIECCCorrectedDelta, sameBucket)
		nonMediumDelta := storageCounterDelta(metric.SCSINonMediumErrors, prior.SCSINonMediumErrors, prior.SCSINonMediumErrorsDelta, sameBucket)
		if nonZeroCounter(eccDelta) {
			addStorageWarning(&metric, "scsi_ecc_errors_increased")
		}
		if nonZeroCounter(nonMediumDelta) {
			addStorageWarning(&metric, "scsi_non_medium_errors_increased")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO monitoring.storage_health_current_metrics (
				node_id, block_device_id, bucket_at, collected_at, received_at,
				smart_available, smart_enabled, smart_status, risk_level, risk_reasons,
				temperature_celsius, power_on_hours, error_count,
				read_error_rate_normalized, read_error_rate_raw,
				reallocated_sectors, pending_sectors, uncorrectable_sectors,
				scsi_grown_defects, scsi_ecc_corrected, scsi_ecc_corrected_delta,
				scsi_non_medium_errors, scsi_non_medium_errors_delta, percentage_used
			) VALUES (
				$1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10,
				$11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
				$21, $22, $23, $24
			)
		`, nodeID, blockDeviceID, bucketAt, collectedAt, receivedAt,
			metric.SMARTAvailable, metric.SMARTEnabled, metric.SMARTStatus, metric.RiskLevel,
			nonNilStrings(metric.RiskReasons), metric.TemperatureCelsius,
			nullableI64(metric.PowerOnHours), nullableI64(metric.ErrorCount),
			nullableI64(metric.ReadErrorRateNormalized), nullableI64(metric.ReadErrorRateRaw),
			nullableI64(metric.ReallocatedSectors), nullableI64(metric.PendingSectors),
			nullableI64(metric.UncorrectableSectors), nullableI64(metric.SCSIGrownDefects),
			nullableI64(metric.SCSIECCCorrected), nullableI64(eccDelta),
			nullableI64(metric.SCSINonMediumErrors), nullableI64(nonMediumDelta),
			metric.PercentageUsed); err != nil {
			return fmt.Errorf("insert storage health metric %q: %w", metric.BlockDeviceKey, err)
		}
	}
	return nil
}

type storageHealthCounters struct {
	BucketAt                 time.Time
	SCSIECCCorrected         *int64
	SCSIECCCorrectedDelta    *int64
	SCSINonMediumErrors      *int64
	SCSINonMediumErrorsDelta *int64
}

func loadStorageHealthCounters(ctx context.Context, tx pgx.Tx, nodeID string) (map[string]storageHealthCounters, error) {
	rows, err := tx.Query(ctx, `
		SELECT block_device_id::text, bucket_at,
		       scsi_ecc_corrected, scsi_ecc_corrected_delta,
		       scsi_non_medium_errors, scsi_non_medium_errors_delta
		  FROM monitoring.storage_health_current_metrics
		 WHERE node_id = $1::uuid
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query previous storage health counters: %w", err)
	}
	defer rows.Close()
	result := make(map[string]storageHealthCounters)
	for rows.Next() {
		var blockDeviceID string
		var counters storageHealthCounters
		if err := rows.Scan(
			&blockDeviceID, &counters.BucketAt,
			&counters.SCSIECCCorrected, &counters.SCSIECCCorrectedDelta,
			&counters.SCSINonMediumErrors, &counters.SCSINonMediumErrorsDelta,
		); err != nil {
			return nil, fmt.Errorf("scan previous storage health counters: %w", err)
		}
		result[blockDeviceID] = counters
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate previous storage health counters: %w", err)
	}
	return result, nil
}

func storageCounterDelta(current *uint64, previous, previousDelta *int64, sameBucket bool) *uint64 {
	if current == nil || previous == nil || *previous < 0 {
		return nil
	}
	base := uint64(0)
	if sameBucket && previousDelta != nil && *previousDelta >= 0 {
		base = uint64(*previousDelta)
	}
	previousValue := uint64(*previous)
	if *current < previousValue {
		return &base
	}
	increase := *current - previousValue
	if increase > math.MaxInt64-base {
		return nil
	}
	delta := base + increase
	return &delta
}

func addStorageWarning(metric *report.StorageHealth, reason string) {
	for _, existing := range metric.RiskReasons {
		if existing == reason {
			return
		}
	}
	metric.RiskReasons = append(metric.RiskReasons, reason)
	sort.Strings(metric.RiskReasons)
	if metric.RiskLevel != "critical" {
		metric.RiskLevel = "warning"
	}
}

func nonZeroCounter(value *uint64) bool {
	return value != nil && *value > 0
}

const temperatureSnapshotFreshnessSQL = `
	SELECT NOT EXISTS (
		SELECT 1 FROM monitoring.temperature_current_metrics
		WHERE node_id = $1::uuid AND bucket_at > $2
	)`

const storageHealthSnapshotFreshnessSQL = `
	SELECT NOT EXISTS (
		SELECT 1 FROM monitoring.storage_health_current_metrics
		WHERE node_id = $1::uuid AND bucket_at > $2
	)`

const raidStatusSnapshotFreshnessSQL = `
	SELECT NOT EXISTS (
		SELECT 1 FROM monitoring.raid_status_current_snapshots
		WHERE node_id = $1::uuid AND bucket_at > $2
	)`

func currentSnapshotCanBeReplaced(ctx context.Context, tx pgx.Tx, query, nodeID string, bucketAt time.Time) (bool, error) {
	var replace bool
	if err := tx.QueryRow(ctx, query, nodeID, bucketAt).Scan(&replace); err != nil {
		return false, err
	}
	return replace, nil
}

func syncInventory(ctx context.Context, tx pgx.Tx, nodeID string, inventory report.Inventory, observedAt time.Time) error {
	if err := syncCPUPackages(ctx, tx, nodeID, inventory.CPUPackages, observedAt); err != nil {
		return err
	}
	if err := syncMemoryModules(ctx, tx, nodeID, inventory.MemoryModules, observedAt); err != nil {
		return err
	}
	if err := syncBlockDevices(ctx, tx, nodeID, inventory.BlockDevices, observedAt); err != nil {
		return err
	}
	if err := syncFilesystems(ctx, tx, nodeID, inventory.Filesystems, observedAt); err != nil {
		return err
	}
	if err := syncNetworkInterfaces(ctx, tx, nodeID, inventory.NetworkInterfaces, observedAt); err != nil {
		return err
	}
	if err := syncGPUs(ctx, tx, nodeID, inventory.GPUs, observedAt); err != nil {
		return err
	}
	return nil
}

func syncGPUs(ctx context.Context, tx pgx.Tx, nodeID string, items []report.GPU, at time.Time) error {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
		result, err := tx.Exec(ctx, `
			UPDATE monitoring.gpu_devices SET
				device_index = $3, gpu_uuid = $4, model_name = $5,
				memory_total_bytes = $6, last_seen_at = $7
			WHERE node_id = $1::uuid AND hardware_key = $2 AND removed_at IS NULL
		`, nodeID, item.Key, item.Index, item.UUID, item.ModelName, i64(item.MemoryTotalBytes), at)
		if err != nil {
			return fmt.Errorf("update GPU %q: %w", item.Key, err)
		}
		if result.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.gpu_devices (
					node_id, hardware_key, device_index, gpu_uuid, model_name,
					memory_total_bytes, first_seen_at, last_seen_at
				) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $7)
			`, nodeID, item.Key, item.Index, item.UUID, item.ModelName, i64(item.MemoryTotalBytes), at); err != nil {
				return fmt.Errorf("insert GPU %q: %w", item.Key, err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE monitoring.gpu_devices SET removed_at = $3
		WHERE node_id = $1::uuid AND removed_at IS NULL AND NOT (hardware_key = ANY($2::text[]))
	`, nodeID, keys, at); err != nil {
		return fmt.Errorf("remove missing GPUs: %w", err)
	}
	return nil
}

func syncCPUPackages(ctx context.Context, tx pgx.Tx, nodeID string, items []report.CPUPackage, at time.Time) error {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
		result, err := tx.Exec(ctx, `
			UPDATE monitoring.cpu_packages SET
				package_index = $3, vendor = NULLIF($4, ''), model_name = $5,
				physical_cores = $6, logical_threads = $7,
				performance_cores = $8, efficiency_cores = $9,
				max_frequency_mhz = NULLIF($10, 0), last_seen_at = $11
			WHERE node_id = $1::uuid AND hardware_key = $2 AND removed_at IS NULL
		`, nodeID, item.Key, item.PackageIndex, item.Vendor, item.ModelName, item.PhysicalCores,
			item.LogicalThreads, item.PerformanceCores, item.EfficiencyCores, item.MaxFrequencyMHz, at)
		if err != nil {
			return fmt.Errorf("update CPU package %q: %w", item.Key, err)
		}
		if result.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.cpu_packages (
					node_id, hardware_key, package_index, vendor, model_name,
					physical_cores, logical_threads, performance_cores, efficiency_cores,
					max_frequency_mhz, first_seen_at, last_seen_at
				) VALUES ($1::uuid, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, NULLIF($10, 0), $11, $11)
			`, nodeID, item.Key, item.PackageIndex, item.Vendor, item.ModelName, item.PhysicalCores,
				item.LogicalThreads, item.PerformanceCores, item.EfficiencyCores, item.MaxFrequencyMHz, at); err != nil {
				return fmt.Errorf("insert CPU package %q: %w", item.Key, err)
			}
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE monitoring.cpu_packages SET removed_at = $3
		WHERE node_id = $1::uuid AND removed_at IS NULL AND NOT (hardware_key = ANY($2::text[]))
	`, nodeID, keys, at)
	if err != nil {
		return fmt.Errorf("remove missing CPU packages: %w", err)
	}
	return nil
}

func syncMemoryModules(ctx context.Context, tx pgx.Tx, nodeID string, items []report.MemoryModule, at time.Time) error {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
		result, err := tx.Exec(ctx, `
			UPDATE monitoring.memory_modules SET
				slot_name = NULLIF($3, ''), manufacturer = NULLIF($4, ''), model_name = NULLIF($5, ''),
				part_number = NULLIF($6, ''), serial_number = NULLIF($7, ''), memory_type = NULLIF($8, ''),
				size_bytes = $9, speed_mts = NULLIF($10, 0), last_seen_at = $11
			WHERE node_id = $1::uuid AND hardware_key = $2 AND removed_at IS NULL
		`, nodeID, item.Key, item.SlotName, item.Manufacturer, item.ModelName, item.PartNumber,
			item.SerialNumber, item.MemoryType, i64(item.SizeBytes), item.SpeedMTs, at)
		if err != nil {
			return fmt.Errorf("update memory module %q: %w", item.Key, err)
		}
		if result.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.memory_modules (
					node_id, hardware_key, slot_name, manufacturer, model_name, part_number,
					serial_number, memory_type, size_bytes, speed_mts, first_seen_at, last_seen_at
				) VALUES (
					$1::uuid, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''),
					NULLIF($7, ''), NULLIF($8, ''), $9, NULLIF($10, 0), $11, $11
				)
			`, nodeID, item.Key, item.SlotName, item.Manufacturer, item.ModelName, item.PartNumber,
				item.SerialNumber, item.MemoryType, i64(item.SizeBytes), item.SpeedMTs, at); err != nil {
				return fmt.Errorf("insert memory module %q: %w", item.Key, err)
			}
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE monitoring.memory_modules SET removed_at = $3
		WHERE node_id = $1::uuid AND removed_at IS NULL AND NOT (hardware_key = ANY($2::text[]))
	`, nodeID, keys, at)
	if err != nil {
		return fmt.Errorf("remove missing memory modules: %w", err)
	}
	return nil
}

func syncBlockDevices(ctx context.Context, tx pgx.Tx, nodeID string, items []report.BlockDevice, at time.Time) error {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
		result, err := tx.Exec(ctx, `
			UPDATE monitoring.block_devices SET
				device_name = $3, device_kind = $4, vendor = NULLIF($5, ''), model_name = NULLIF($6, ''),
				serial_number = NULLIF($7, ''), wwn = NULLIF($8, ''), size_bytes = $9,
				rotational = $10, protocol = NULLIF($11, ''), smart_device_type = NULLIF($12, ''),
				raid_passthrough = $13, last_seen_at = $14
			WHERE node_id = $1::uuid AND hardware_key = $2 AND removed_at IS NULL
		`, nodeID, item.Key, item.DeviceName, item.DeviceKind, item.Vendor, item.ModelName,
			item.SerialNumber, item.WWN, i64(item.SizeBytes), item.Rotational, item.Protocol,
			item.SMARTDeviceType, item.RAIDPassthrough, at)
		if err != nil {
			return fmt.Errorf("update block device %q: %w", item.Key, err)
		}
		if result.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.block_devices (
					node_id, hardware_key, device_name, device_kind, vendor, model_name,
					serial_number, wwn, size_bytes, rotational, protocol, smart_device_type,
					raid_passthrough, first_seen_at, last_seen_at
				) VALUES (
					$1::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''),
					NULLIF($7, ''), NULLIF($8, ''), $9, $10, NULLIF($11, ''), NULLIF($12, ''),
					$13, $14, $14
				)
			`, nodeID, item.Key, item.DeviceName, item.DeviceKind, item.Vendor, item.ModelName,
				item.SerialNumber, item.WWN, i64(item.SizeBytes), item.Rotational, item.Protocol,
				item.SMARTDeviceType, item.RAIDPassthrough, at); err != nil {
				return fmt.Errorf("insert block device %q: %w", item.Key, err)
			}
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE monitoring.block_devices SET removed_at = $3
		WHERE node_id = $1::uuid AND removed_at IS NULL AND NOT (hardware_key = ANY($2::text[]))
	`, nodeID, keys, at)
	if err != nil {
		return fmt.Errorf("remove missing block devices: %w", err)
	}
	return nil
}

func syncFilesystems(ctx context.Context, tx pgx.Tx, nodeID string, items []report.Filesystem, at time.Time) error {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
		mountOptions := nonNilStrings(item.MountOptions)
		result, err := tx.Exec(ctx, `
			UPDATE monitoring.filesystems SET
				filesystem_uuid = NULLIF($3, ''), device_name = $4, filesystem_type = $5,
				mount_point = $6, mount_options = $7, last_seen_at = $8
			WHERE node_id = $1::uuid AND filesystem_key = $2 AND removed_at IS NULL
		`, nodeID, item.Key, item.UUID, item.DeviceName, item.FilesystemType, item.MountPoint, mountOptions, at)
		if err != nil {
			return fmt.Errorf("update filesystem %q: %w", item.Key, err)
		}
		if result.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.filesystems (
					node_id, filesystem_key, filesystem_uuid, device_name, filesystem_type,
					mount_point, mount_options, first_seen_at, last_seen_at
				) VALUES ($1::uuid, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $8)
			`, nodeID, item.Key, item.UUID, item.DeviceName, item.FilesystemType, item.MountPoint, mountOptions, at); err != nil {
				return fmt.Errorf("insert filesystem %q: %w", item.Key, err)
			}
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE monitoring.filesystems SET removed_at = $3
		WHERE node_id = $1::uuid AND removed_at IS NULL AND NOT (filesystem_key = ANY($2::text[]))
	`, nodeID, keys, at)
	if err != nil {
		return fmt.Errorf("remove missing filesystems: %w", err)
	}
	return nil
}

func syncNetworkInterfaces(ctx context.Context, tx pgx.Tx, nodeID string, items []report.NetworkInterface, at time.Time) error {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
		macAddress := postgresMACAddress(item.MACAddress)
		var interfaceID string
		err := tx.QueryRow(ctx, `
			UPDATE monitoring.network_interfaces SET
				interface_name = $3, mac_address = NULLIF($4, '')::macaddr,
				mtu = NULLIF($5, 0), link_speed_mbps = NULLIF($6, 0), last_seen_at = $7
			WHERE node_id = $1::uuid AND interface_key = $2 AND removed_at IS NULL
			RETURNING id::text
		`, nodeID, item.Key, item.Name, macAddress, item.MTU, item.LinkSpeedMbps, at).Scan(&interfaceID)
		if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `
				INSERT INTO monitoring.network_interfaces (
					node_id, interface_key, interface_name, mac_address, mtu,
					link_speed_mbps, first_seen_at, last_seen_at
				) VALUES ($1::uuid, $2, $3, NULLIF($4, '')::macaddr, NULLIF($5, 0), NULLIF($6, 0), $7, $7)
				RETURNING id::text
			`, nodeID, item.Key, item.Name, macAddress, item.MTU, item.LinkSpeedMbps, at).Scan(&interfaceID)
		}
		if err != nil {
			return fmt.Errorf("upsert network interface %q: %w", item.Key, err)
		}
		addressIDs := make([]string, 0, len(item.Addresses))
		for _, address := range item.Addresses {
			var addressID string
			err := tx.QueryRow(ctx, `
				UPDATE monitoring.network_addresses SET address_scope = $4, last_seen_at = $5
				WHERE node_id = $1::uuid AND interface_id = $2::uuid
				  AND address = $3::inet AND removed_at IS NULL
				RETURNING id::text
			`, nodeID, interfaceID, address.Address, address.Scope, at).Scan(&addressID)
			if err == pgx.ErrNoRows {
				err = tx.QueryRow(ctx, `
					INSERT INTO monitoring.network_addresses (
						node_id, interface_id, address, address_scope, first_seen_at, last_seen_at
					) VALUES ($1::uuid, $2::uuid, $3::inet, $4, $5, $5)
					RETURNING id::text
				`, nodeID, interfaceID, address.Address, address.Scope, at).Scan(&addressID)
			}
			if err != nil {
				return fmt.Errorf("upsert address %q: %w", address.Address, err)
			}
			addressIDs = append(addressIDs, addressID)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE monitoring.network_addresses SET removed_at = $4
			WHERE node_id = $1::uuid AND interface_id = $2::uuid AND removed_at IS NULL
			  AND NOT (id::text = ANY($3::text[]))
		`, nodeID, interfaceID, addressIDs, at); err != nil {
			return fmt.Errorf("remove missing addresses on %q: %w", item.Key, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE monitoring.network_interfaces SET removed_at = $3
		WHERE node_id = $1::uuid AND removed_at IS NULL AND NOT (interface_key = ANY($2::text[]))
	`, nodeID, keys, at); err != nil {
		return fmt.Errorf("remove missing network interfaces: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE monitoring.network_addresses address SET removed_at = $2
		WHERE address.node_id = $1::uuid AND address.removed_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM monitoring.network_interfaces interface
			WHERE interface.id = address.interface_id AND interface.removed_at IS NULL
		  )
	`, nodeID, at); err != nil {
		return fmt.Errorf("remove addresses on missing interfaces: %w", err)
	}
	return nil
}

func activeFilesystemIDs(ctx context.Context, tx pgx.Tx, nodeID string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT filesystem_key, id::text FROM monitoring.filesystems
		WHERE node_id = $1::uuid AND removed_at IS NULL
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query active filesystems: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var key, resourceID string
		if err := rows.Scan(&key, &resourceID); err != nil {
			return nil, fmt.Errorf("scan active filesystem: %w", err)
		}
		result[key] = resourceID
	}
	return result, rows.Err()
}

func activeInterfaceIDs(ctx context.Context, tx pgx.Tx, nodeID string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT interface_key, id::text FROM monitoring.network_interfaces
		WHERE node_id = $1::uuid AND removed_at IS NULL
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query active interfaces: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var key, resourceID string
		if err := rows.Scan(&key, &resourceID); err != nil {
			return nil, fmt.Errorf("scan active interface: %w", err)
		}
		result[key] = resourceID
	}
	return result, rows.Err()
}

func activeBlockDeviceIDs(ctx context.Context, tx pgx.Tx, nodeID string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT hardware_key, id::text FROM monitoring.block_devices
		WHERE node_id = $1::uuid AND removed_at IS NULL
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query active block devices: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var key, resourceID string
		if err := rows.Scan(&key, &resourceID); err != nil {
			return nil, fmt.Errorf("scan active block device: %w", err)
		}
		result[key] = resourceID
	}
	return result, rows.Err()
}

type activeGPU struct {
	ID               string
	MemoryTotalBytes int64
}

func activeGPUIDs(ctx context.Context, tx pgx.Tx, nodeID string) (map[string]activeGPU, error) {
	rows, err := tx.Query(ctx, `
		SELECT hardware_key, id::text, memory_total_bytes FROM monitoring.gpu_devices
		WHERE node_id = $1::uuid AND removed_at IS NULL
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query active GPUs: %w", err)
	}
	defer rows.Close()
	result := make(map[string]activeGPU)
	for rows.Next() {
		var key string
		var gpu activeGPU
		if err := rows.Scan(&key, &gpu.ID, &gpu.MemoryTotalBytes); err != nil {
			return nil, fmt.Errorf("scan active GPU: %w", err)
		}
		result[key] = gpu
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active GPUs: %w", err)
	}
	return result, nil
}

func i64(value uint64) int64 {
	return int64(value)
}

func nullableI64(value *uint64) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func postgresMACAddress(value string) string {
	address, err := net.ParseMAC(value)
	if err != nil || len(address) != 6 {
		return ""
	}
	return address.String()
}
