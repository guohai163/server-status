package report

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func (r *Report) Validate(now time.Time) error {
	if r.Version != Version {
		return fmt.Errorf("unsupported report version %d", r.Version)
	}
	if !uuidPattern.MatchString(r.Agent.ID) {
		return errors.New("agent.id must be an RFC 4122 UUID")
	}
	if strings.TrimSpace(r.Agent.Hostname) == "" || strings.TrimSpace(r.Agent.OSName) == "" || strings.TrimSpace(r.Agent.Architecture) == "" || strings.TrimSpace(r.Agent.AgentVersion) == "" {
		return errors.New("agent hostname, os_name, architecture, and agent_version are required")
	}
	switch r.Agent.MachineType {
	case "", "physical", "virtual":
	default:
		return errors.New("agent machine_type must be physical or virtual")
	}
	if r.Agent.PrimaryIP != "" && net.ParseIP(r.Agent.PrimaryIP) == nil {
		return errors.New("agent primary_ip must be an IP address")
	}
	if r.CollectedAt.IsZero() {
		return errors.New("collected_at is required")
	}
	if r.CollectedAt.After(now.Add(5 * time.Minute)) {
		return errors.New("collected_at is more than 5 minutes in the future")
	}
	if r.CollectedAt.Before(now.Add(-24 * time.Hour)) {
		return errors.New("collected_at is more than 24 hours old")
	}
	if r.IntervalSeconds < 1 || r.IntervalSeconds > 3600 {
		return errors.New("interval_seconds must be between 1 and 3600")
	}
	if r.Metrics.CPU.UsagePercent < 0 || r.Metrics.CPU.UsagePercent > 100 {
		return errors.New("cpu usage_percent must be between 0 and 100")
	}
	if r.Metrics.CPU.Load1 < 0 || r.Metrics.CPU.Load5 < 0 || r.Metrics.CPU.Load15 < 0 {
		return errors.New("CPU load values cannot be negative")
	}
	if err := validateInventory(r.Inventory); err != nil {
		return err
	}
	if err := validateMetrics(r.Inventory, r.Metrics); err != nil {
		return err
	}
	fingerprint, err := InventoryFingerprint(r.Inventory)
	if err != nil {
		return fmt.Errorf("fingerprint inventory: %w", err)
	}
	if r.InventoryFingerprint != fingerprint {
		return errors.New("inventory_fingerprint does not match inventory")
	}
	return nil
}

func validateInventory(inventory Inventory) error {
	if len(inventory.CPUPackages) > 1024 || len(inventory.MemoryModules) > 4096 || len(inventory.BlockDevices) > 4096 || len(inventory.Filesystems) > 4096 || len(inventory.NetworkInterfaces) > 4096 || len(inventory.GPUs) > 1024 {
		return errors.New("inventory contains too many resources")
	}
	if err := uniqueKeys("CPU package", len(inventory.CPUPackages), func(i int) string { return inventory.CPUPackages[i].Key }); err != nil {
		return err
	}
	for _, item := range inventory.CPUPackages {
		if strings.TrimSpace(item.ModelName) == "" || item.PhysicalCores < 1 || item.LogicalThreads < item.PhysicalCores || item.PackageIndex < 0 || item.MaxFrequencyMHz < 0 {
			return fmt.Errorf("invalid CPU package %q", item.Key)
		}
	}
	if err := uniqueKeys("memory module", len(inventory.MemoryModules), func(i int) string { return inventory.MemoryModules[i].Key }); err != nil {
		return err
	}
	for _, item := range inventory.MemoryModules {
		if item.SizeBytes == 0 || item.SizeBytes > math.MaxInt64 || item.SpeedMTs < 0 {
			return fmt.Errorf("invalid memory module %q", item.Key)
		}
	}
	if err := uniqueKeys("block device", len(inventory.BlockDevices), func(i int) string { return inventory.BlockDevices[i].Key }); err != nil {
		return err
	}
	for _, item := range inventory.BlockDevices {
		if strings.TrimSpace(item.DeviceName) == "" || item.SizeBytes > math.MaxInt64 {
			return fmt.Errorf("invalid block device %q", item.Key)
		}
		switch item.DeviceKind {
		case "disk", "raid", "multipath", "virtual":
		default:
			return fmt.Errorf("invalid block device kind %q", item.DeviceKind)
		}
	}
	if err := uniqueKeys("filesystem", len(inventory.Filesystems), func(i int) string { return inventory.Filesystems[i].Key }); err != nil {
		return err
	}
	for _, item := range inventory.Filesystems {
		if strings.TrimSpace(item.DeviceName) == "" || strings.TrimSpace(item.FilesystemType) == "" || strings.TrimSpace(item.MountPoint) == "" {
			return fmt.Errorf("invalid filesystem %q", item.Key)
		}
	}
	if err := uniqueKeys("network interface", len(inventory.NetworkInterfaces), func(i int) string { return inventory.NetworkInterfaces[i].Key }); err != nil {
		return err
	}
	for _, item := range inventory.NetworkInterfaces {
		if strings.TrimSpace(item.Name) == "" || item.MTU < 0 || item.LinkSpeedMbps < 0 {
			return fmt.Errorf("invalid network interface %q", item.Key)
		}
		seenAddresses := make(map[string]struct{}, len(item.Addresses))
		for _, address := range item.Addresses {
			if _, _, err := net.ParseCIDR(address.Address); err != nil {
				return fmt.Errorf("invalid address %q on interface %q", address.Address, item.Key)
			}
			if _, ok := seenAddresses[address.Address]; ok {
				return fmt.Errorf("duplicate address %q on interface %q", address.Address, item.Key)
			}
			seenAddresses[address.Address] = struct{}{}
			switch address.Scope {
			case "host", "link", "private", "global", "multicast", "unknown":
			default:
				return fmt.Errorf("invalid address scope %q", address.Scope)
			}
		}
	}
	if err := uniqueKeys("GPU", len(inventory.GPUs), func(i int) string { return inventory.GPUs[i].Key }); err != nil {
		return err
	}
	for _, item := range inventory.GPUs {
		if item.Index < 0 || strings.TrimSpace(item.UUID) == "" || strings.TrimSpace(item.ModelName) == "" || item.MemoryTotalBytes == 0 || item.MemoryTotalBytes > math.MaxInt64 {
			return fmt.Errorf("invalid GPU %q", item.Key)
		}
	}
	return nil
}

func validateMetrics(inventory Inventory, metrics Metrics) error {
	if err := validateUnsignedBigints(
		metrics.Memory.TotalBytes, metrics.Memory.UsedBytes, metrics.Memory.AvailableBytes,
		metrics.Memory.CachedBytes, metrics.Memory.BuffersBytes, metrics.Memory.SwapTotalBytes,
		metrics.Memory.SwapUsedBytes, metrics.Memory.UptimeSeconds,
	); err != nil {
		return fmt.Errorf("memory metrics: %w", err)
	}
	if metrics.Memory.UsedBytes > metrics.Memory.TotalBytes || metrics.Memory.AvailableBytes > metrics.Memory.TotalBytes || metrics.Memory.CachedBytes > metrics.Memory.TotalBytes || metrics.Memory.BuffersBytes > metrics.Memory.TotalBytes || metrics.Memory.SwapUsedBytes > metrics.Memory.SwapTotalBytes {
		return errors.New("memory metrics exceed their totals")
	}
	if err := validateUnsignedBigints(
		metrics.Disk.ReadBytesTotal, metrics.Disk.WriteBytesTotal,
		metrics.Disk.ReadBytesDelta, metrics.Disk.WriteBytesDelta,
		metrics.Disk.ReadOpsDelta, metrics.Disk.WriteOpsDelta,
	); err != nil {
		return fmt.Errorf("disk metrics: %w", err)
	}
	filesystemKeys := make(map[string]struct{}, len(inventory.Filesystems))
	for _, item := range inventory.Filesystems {
		filesystemKeys[item.Key] = struct{}{}
	}
	if err := uniqueKeys("filesystem metric", len(metrics.Filesystems), func(i int) string { return metrics.Filesystems[i].FilesystemKey }); err != nil {
		return err
	}
	for _, item := range metrics.Filesystems {
		if _, ok := filesystemKeys[item.FilesystemKey]; !ok {
			return fmt.Errorf("filesystem metric references unknown key %q", item.FilesystemKey)
		}
		if err := validateUnsignedBigints(item.TotalBytes, item.UsedBytes, item.AvailableBytes, item.TotalInodes, item.UsedInodes); err != nil {
			return fmt.Errorf("filesystem metric %q: %w", item.FilesystemKey, err)
		}
		if item.UsedBytes > item.TotalBytes || item.AvailableBytes > item.TotalBytes || item.UsedInodes > item.TotalInodes && item.TotalInodes != 0 {
			return fmt.Errorf("filesystem metric %q exceeds its total", item.FilesystemKey)
		}
	}
	interfaceKeys := make(map[string]struct{}, len(inventory.NetworkInterfaces))
	for _, item := range inventory.NetworkInterfaces {
		interfaceKeys[item.Key] = struct{}{}
	}
	if err := uniqueKeys("network metric", len(metrics.Network), func(i int) string { return metrics.Network[i].InterfaceKey }); err != nil {
		return err
	}
	for _, item := range metrics.Network {
		if _, ok := interfaceKeys[item.InterfaceKey]; !ok {
			return fmt.Errorf("network metric references unknown key %q", item.InterfaceKey)
		}
		if err := validateUnsignedBigints(
			item.RXBytesTotal, item.TXBytesTotal, item.RXBytesDelta, item.TXBytesDelta,
			item.RXPacketsDelta, item.TXPacketsDelta, item.RXErrorsDelta, item.TXErrorsDelta,
			item.RXDroppedDelta, item.TXDroppedDelta,
		); err != nil {
			return fmt.Errorf("network metric %q: %w", item.InterfaceKey, err)
		}
	}
	gpuMemoryByKey := make(map[string]uint64, len(inventory.GPUs))
	for _, item := range inventory.GPUs {
		gpuMemoryByKey[item.Key] = item.MemoryTotalBytes
	}
	if err := uniqueKeys("GPU metric", len(metrics.GPUs), func(i int) string { return metrics.GPUs[i].GPUKey }); err != nil {
		return err
	}
	for _, item := range metrics.GPUs {
		memoryTotal, ok := gpuMemoryByKey[item.GPUKey]
		if !ok {
			return fmt.Errorf("GPU metric references unknown key %q", item.GPUKey)
		}
		if item.UtilizationPercent < 0 || item.UtilizationPercent > 100 {
			return fmt.Errorf("GPU metric %q utilization_percent must be between 0 and 100", item.GPUKey)
		}
		if err := validateUnsignedBigints(item.MemoryUsedBytes); err != nil {
			return fmt.Errorf("GPU metric %q: %w", item.GPUKey, err)
		}
		if item.MemoryUsedBytes > memoryTotal {
			return fmt.Errorf("GPU metric %q memory usage exceeds its total", item.GPUKey)
		}
	}
	return nil
}

func uniqueKeys(kind string, count int, key func(int) string) error {
	seen := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		value := strings.TrimSpace(key(i))
		if value == "" {
			return fmt.Errorf("%s key cannot be empty", kind)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s key %q", kind, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateUnsignedBigints(values ...uint64) error {
	for _, value := range values {
		if value > math.MaxInt64 {
			return errors.New("value exceeds PostgreSQL bigint")
		}
	}
	return nil
}

func ValidFingerprint(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256Size
}

func ValidUUID(value string) bool {
	return uuidPattern.MatchString(value)
}

const sha256Size = 32
