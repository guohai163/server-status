package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/guohai/server-status/internal/report"
	"github.com/jackc/pgx/v5"
)

func (store *Store) Ingest(ctx context.Context, auth NodeAuth, payload report.Report) error {
	receivedAt := time.Now().UTC()
	bucketAt := payload.CollectedAt.UTC().Truncate(time.Minute)
	agentLabels := make(map[string]string, len(payload.Agent.Labels)+2)
	for key, value := range payload.Agent.Labels {
		agentLabels[key] = value
	}
	if payload.Agent.MachineType != "" {
		agentLabels[machineTypeLabelKey] = payload.Agent.MachineType
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
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit report: %w", err)
	}
	return nil
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
				max_frequency_mhz = NULLIF($8, 0), last_seen_at = $9
			WHERE node_id = $1::uuid AND hardware_key = $2 AND removed_at IS NULL
		`, nodeID, item.Key, item.PackageIndex, item.Vendor, item.ModelName, item.PhysicalCores,
			item.LogicalThreads, item.MaxFrequencyMHz, at)
		if err != nil {
			return fmt.Errorf("update CPU package %q: %w", item.Key, err)
		}
		if result.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.cpu_packages (
					node_id, hardware_key, package_index, vendor, model_name,
					physical_cores, logical_threads, max_frequency_mhz, first_seen_at, last_seen_at
				) VALUES ($1::uuid, $2, $3, NULLIF($4, ''), $5, $6, $7, NULLIF($8, 0), $9, $9)
			`, nodeID, item.Key, item.PackageIndex, item.Vendor, item.ModelName, item.PhysicalCores,
				item.LogicalThreads, item.MaxFrequencyMHz, at); err != nil {
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
				rotational = $10, last_seen_at = $11
			WHERE node_id = $1::uuid AND hardware_key = $2 AND removed_at IS NULL
		`, nodeID, item.Key, item.DeviceName, item.DeviceKind, item.Vendor, item.ModelName,
			item.SerialNumber, item.WWN, i64(item.SizeBytes), item.Rotational, at)
		if err != nil {
			return fmt.Errorf("update block device %q: %w", item.Key, err)
		}
		if result.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO monitoring.block_devices (
					node_id, hardware_key, device_name, device_kind, vendor, model_name,
					serial_number, wwn, size_bytes, rotational, first_seen_at, last_seen_at
				) VALUES (
					$1::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''),
					NULLIF($7, ''), NULLIF($8, ''), $9, $10, $11, $11
				)
			`, nodeID, item.Key, item.DeviceName, item.DeviceKind, item.Vendor, item.ModelName,
				item.SerialNumber, item.WWN, i64(item.SizeBytes), item.Rotational, at); err != nil {
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
