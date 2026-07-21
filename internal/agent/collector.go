package agent

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guohai/server-status/internal/report"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

var Version = "dev"

type Collector struct {
	mu             sync.Mutex
	previousNet    map[string]gnet.IOCountersStat
	previousDisk   map[string]disk.IOCountersStat
	lastCollection time.Time
}

func NewCollector() *Collector {
	return &Collector{
		previousNet:  make(map[string]gnet.IOCountersStat),
		previousDisk: make(map[string]disk.IOCountersStat),
	}
}

func (collector *Collector) Collect(ctx context.Context, config Config) (report.Report, error) {
	hostInfo, err := host.InfoWithContext(ctx)
	if err != nil {
		return report.Report{}, fmt.Errorf("collect host info: %w", err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return report.Report{}, fmt.Errorf("read hostname: %w", err)
	}
	cpuInventory, err := collectCPUInventory(ctx)
	if err != nil {
		return report.Report{}, err
	}
	usage, err := cpu.PercentWithContext(ctx, time.Second, false)
	if err != nil || len(usage) == 0 {
		return report.Report{}, fmt.Errorf("collect CPU usage: %w", err)
	}
	loadAverage, err := load.AvgWithContext(ctx)
	if err != nil {
		return report.Report{}, fmt.Errorf("collect load average: %w", err)
	}
	virtualMemory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return report.Report{}, fmt.Errorf("collect memory: %w", err)
	}
	swapMemory, err := mem.SwapMemoryWithContext(ctx)
	if err != nil {
		return report.Report{}, fmt.Errorf("collect swap: %w", err)
	}

	memoryModules, _ := collectMemoryModules(ctx)
	if len(memoryModules) == 0 && virtualMemory.Total > 0 {
		memoryModules = []report.MemoryModule{{
			Key:       "system-memory-aggregate",
			SlotName:  "aggregate",
			ModelName: "System Memory (DMI unavailable)",
			SizeBytes: virtualMemory.Total,
		}}
	}
	blockDevices, err := collectBlockDevices()
	if err != nil {
		return report.Report{}, err
	}
	diskMetrics, err := collector.collectDisk(ctx, blockDevices)
	if err != nil {
		return report.Report{}, err
	}
	filesystems, filesystemMetrics, err := collectFilesystems(ctx)
	if err != nil {
		return report.Report{}, err
	}
	networkInterfaces, networkMetrics, err := collector.collectNetwork(ctx)
	if err != nil {
		return report.Report{}, err
	}
	gpus, gpuMetrics := collectNVIDIAGPUs(ctx)

	now := time.Now().UTC()
	collector.mu.Lock()
	interval := config.Interval
	if !collector.lastCollection.IsZero() {
		interval = now.Sub(collector.lastCollection)
	}
	collector.lastCollection = now
	collector.mu.Unlock()
	intervalSeconds := int(interval.Round(time.Second) / time.Second)
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}
	if intervalSeconds > 3600 {
		intervalSeconds = 3600
	}

	payload := report.Report{
		Version:         report.Version,
		CollectedAt:     now,
		IntervalSeconds: intervalSeconds,
		Agent: report.AgentInfo{
			ID:            config.AgentID,
			Hostname:      hostname,
			OSName:        hostInfo.Platform,
			OSVersion:     hostInfo.PlatformVersion,
			KernelVersion: hostInfo.KernelVersion,
			Architecture:  runtime.GOARCH,
			AgentVersion:  Version,
			MachineType:   classifyMachineType(hostInfo.VirtualizationRole),
			PrimaryIP:     preferredBridgeIPv4(networkInterfaces, isBridgeInterface),
			Labels:        config.Labels,
		},
		Inventory: report.Inventory{
			CPUPackages:       cpuInventory,
			MemoryModules:     memoryModules,
			BlockDevices:      blockDevices,
			Filesystems:       filesystems,
			NetworkInterfaces: networkInterfaces,
			GPUs:              gpus,
		},
		Metrics: report.Metrics{
			CPU: report.CPUMetrics{
				UsagePercent: usage[0],
				Load1:        loadAverage.Load1,
				Load5:        loadAverage.Load5,
				Load15:       loadAverage.Load15,
			},
			Memory: report.MemoryMetrics{
				TotalBytes:     virtualMemory.Total,
				UsedBytes:      virtualMemory.Used,
				AvailableBytes: virtualMemory.Available,
				CachedBytes:    virtualMemory.Cached,
				BuffersBytes:   virtualMemory.Buffers,
				SwapTotalBytes: swapMemory.Total,
				SwapUsedBytes:  swapMemory.Used,
				UptimeSeconds:  hostInfo.Uptime,
			},
			Disk:        diskMetrics,
			Filesystems: filesystemMetrics,
			Network:     networkMetrics,
			GPUs:        gpuMetrics,
		},
	}
	report.NormalizeInventory(&payload.Inventory)
	payload.InventoryFingerprint, err = report.InventoryFingerprint(payload.Inventory)
	if err != nil {
		return report.Report{}, fmt.Errorf("fingerprint inventory: %w", err)
	}
	return payload, nil
}

func collectNVIDIAGPUs(ctx context.Context) ([]report.GPU, []report.GPUMetrics) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, nil
	}
	commandContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandContext, path,
		"--query-gpu=uuid,name,index,utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, nil
	}
	return parseNVIDIASMI(string(output))
}

func parseNVIDIASMI(output string) ([]report.GPU, []report.GPUMetrics) {
	reader := csv.NewReader(strings.NewReader(output))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, nil
	}
	type gpuSample struct {
		inventory report.GPU
		metric    report.GPUMetrics
	}
	samples := make([]gpuSample, 0, len(rows))
	for _, row := range rows {
		if len(row) != 6 {
			continue
		}
		for index := range row {
			row[index] = strings.TrimSpace(row[index])
		}
		gpuIndex, indexErr := strconv.Atoi(row[2])
		utilization, utilizationErr := strconv.ParseFloat(row[3], 64)
		memoryUsedMiB, usedErr := strconv.ParseUint(row[4], 10, 64)
		memoryTotalMiB, totalErr := strconv.ParseUint(row[5], 10, 64)
		if row[0] == "" || row[1] == "" || indexErr != nil || utilizationErr != nil || usedErr != nil || totalErr != nil || memoryTotalMiB == 0 {
			continue
		}
		const mebibyte = uint64(1024 * 1024)
		samples = append(samples, gpuSample{
			inventory: report.GPU{Key: row[0], UUID: row[0], ModelName: row[1], Index: gpuIndex, MemoryTotalBytes: memoryTotalMiB * mebibyte},
			metric:    report.GPUMetrics{GPUKey: row[0], UtilizationPercent: utilization, MemoryUsedBytes: memoryUsedMiB * mebibyte},
		})
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].inventory.Index < samples[j].inventory.Index })
	inventory := make([]report.GPU, 0, len(samples))
	metrics := make([]report.GPUMetrics, 0, len(samples))
	for _, sample := range samples {
		inventory = append(inventory, sample.inventory)
		metrics = append(metrics, sample.metric)
	}
	return inventory, metrics
}

func classifyMachineType(virtualizationRole string) string {
	if strings.EqualFold(strings.TrimSpace(virtualizationRole), "guest") {
		return "virtual"
	}
	return "physical"
}

func (collector *Collector) collectDisk(ctx context.Context, devices []report.BlockDevice) (report.DiskMetrics, error) {
	names := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.DeviceKind == "disk" {
			names = append(names, filepath.Base(device.DeviceName))
		}
	}
	if len(names) == 0 {
		return report.DiskMetrics{}, nil
	}
	counters, err := disk.IOCountersWithContext(ctx, names...)
	if err != nil {
		return report.DiskMetrics{}, fmt.Errorf("collect disk counters: %w", err)
	}
	var metric report.DiskMetrics
	collector.mu.Lock()
	defer collector.mu.Unlock()
	metric = aggregateDiskCounters(collector.previousDisk, names, counters)
	return metric, nil
}

func aggregateDiskCounters(previousByName map[string]disk.IOCountersStat, names []string, counters map[string]disk.IOCountersStat) report.DiskMetrics {
	var metric report.DiskMetrics
	for _, name := range names {
		current, ok := counters[name]
		if !ok {
			continue
		}
		metric.ReadBytesTotal += current.ReadBytes
		metric.WriteBytesTotal += current.WriteBytes
		if previous, exists := previousByName[name]; exists {
			metric.ReadBytesDelta += counterDelta(current.ReadBytes, previous.ReadBytes)
			metric.WriteBytesDelta += counterDelta(current.WriteBytes, previous.WriteBytes)
			metric.ReadOpsDelta += counterDelta(current.ReadCount, previous.ReadCount)
			metric.WriteOpsDelta += counterDelta(current.WriteCount, previous.WriteCount)
		}
		previousByName[name] = current
	}
	return metric
}

func collectCPUInventory(ctx context.Context) ([]report.CPUPackage, error) {
	info, err := cpu.InfoWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect CPU inventory: %w", err)
	}
	type aggregate struct {
		vendor, model string
		cores         int
		threads       int
		mhz           float64
	}
	groups := make(map[string]*aggregate)
	for _, item := range info {
		key := strings.TrimSpace(item.PhysicalID)
		if key == "" {
			key = "0"
		}
		group := groups[key]
		if group == nil {
			group = &aggregate{vendor: item.VendorID, model: item.ModelName}
			groups[key] = group
		}
		group.threads++
		if int(item.Cores) > group.cores {
			group.cores = int(item.Cores)
		}
		if item.Mhz > group.mhz {
			group.mhz = item.Mhz
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]report.CPUPackage, 0, len(keys))
	for index, key := range keys {
		group := groups[key]
		if group.cores < 1 {
			group.cores = group.threads
		}
		if group.threads < group.cores {
			group.threads = group.cores
		}
		model := strings.TrimSpace(group.model)
		if model == "" {
			model = "unknown"
		}
		result = append(result, report.CPUPackage{
			Key:             "cpu-package-" + key,
			PackageIndex:    index,
			Vendor:          strings.TrimSpace(group.vendor),
			ModelName:       model,
			PhysicalCores:   group.cores,
			LogicalThreads:  group.threads,
			MaxFrequencyMHz: group.mhz,
		})
	}
	if len(result) == 0 {
		return nil, errors.New("no CPU packages found")
	}
	return result, nil
}

func collectMemoryModules(ctx context.Context) ([]report.MemoryModule, error) {
	path, err := exec.LookPath("dmidecode")
	if err != nil {
		return []report.MemoryModule{}, nil
	}
	commandContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandContext, path, "--type", "17").Output()
	if err != nil {
		return []report.MemoryModule{}, nil
	}
	return parseDMIMemoryModules(string(output)), nil
}

func parseDMIMemoryModules(output string) []report.MemoryModule {
	sections := strings.Split(output, "Memory Device")
	result := make([]report.MemoryModule, 0)
	for _, section := range sections[1:] {
		fields := make(map[string]string)
		scanner := bufio.NewScanner(strings.NewReader(section))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			key, value, ok := strings.Cut(line, ":")
			if ok {
				fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		size := parseMemorySize(fields["Size"])
		if size == 0 {
			continue
		}
		slot := fields["Locator"]
		key := slot
		if key == "" {
			key = fields["Bank Locator"]
		}
		if key == "" {
			key = fmt.Sprintf("memory-%d", len(result))
		}
		speed := parseLeadingInt(fields["Configured Memory Speed"])
		if speed == 0 {
			speed = parseLeadingInt(fields["Speed"])
		}
		result = append(result, report.MemoryModule{
			Key:          key,
			SlotName:     slot,
			Manufacturer: cleanDMIValue(fields["Manufacturer"]),
			ModelName:    cleanDMIValue(fields["Part Number"]),
			PartNumber:   cleanDMIValue(fields["Part Number"]),
			SerialNumber: cleanDMIValue(fields["Serial Number"]),
			MemoryType:   cleanDMIValue(fields["Type"]),
			SizeBytes:    size,
			SpeedMTs:     speed,
		})
	}
	return result
}

func parseMemorySize(value string) uint64 {
	parts := strings.Fields(value)
	if len(parts) < 2 {
		return 0
	}
	amount, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(parts[1]) {
	case "KB":
		return amount * 1024
	case "MB":
		return amount * 1024 * 1024
	case "GB":
		return amount * 1024 * 1024 * 1024
	case "TB":
		return amount * 1024 * 1024 * 1024 * 1024
	default:
		return 0
	}
}

func parseLeadingInt(value string) int {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return 0
	}
	parsed, _ := strconv.Atoi(parts[0])
	return parsed
}

func cleanDMIValue(value string) string {
	switch strings.TrimSpace(value) {
	case "", "Unknown", "Not Specified", "No Module Installed":
		return ""
	default:
		return strings.TrimSpace(value)
	}
}

func collectBlockDevices() ([]report.BlockDevice, error) {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, fmt.Errorf("read /sys/block: %w", err)
	}
	result := make([]report.BlockDevice, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "fd") || strings.HasPrefix(name, "sr") {
			continue
		}
		base := filepath.Join("/sys/block", name)
		sizeSectors, _ := strconv.ParseUint(readTrimmed(filepath.Join(base, "size")), 10, 64)
		rotationalValue := readTrimmed(filepath.Join(base, "queue", "rotational"))
		var rotational *bool
		if rotationalValue == "0" || rotationalValue == "1" {
			value := rotationalValue == "1"
			rotational = &value
		}
		serial := readTrimmed(filepath.Join(base, "device", "serial"))
		wwn := readTrimmed(filepath.Join(base, "device", "wwid"))
		vendor := readTrimmed(filepath.Join(base, "device", "vendor"))
		model := readTrimmed(filepath.Join(base, "device", "model"))
		if model == "" {
			model = fallbackBlockModel(name, vendor)
		}
		key := firstNonEmpty(serial, wwn, name)
		result = append(result, report.BlockDevice{
			Key:          key,
			DeviceName:   "/dev/" + name,
			DeviceKind:   blockDeviceKind(name),
			Vendor:       vendor,
			ModelName:    model,
			SerialNumber: serial,
			WWN:          wwn,
			SizeBytes:    sizeSectors * 512,
			Rotational:   rotational,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func fallbackBlockModel(name, vendor string) string {
	vendor = strings.TrimSpace(vendor)
	if strings.EqualFold(vendor, "0x1af4") {
		return "Virtio Block Device (" + name + ")"
	}
	if vendor != "" {
		return vendor + " Block Device (" + name + ")"
	}
	return "Block Device (" + name + ")"
}

func blockDeviceKind(name string) string {
	switch {
	case strings.HasPrefix(name, "md"):
		return "raid"
	case strings.HasPrefix(name, "dm-"):
		return "virtual"
	case strings.HasPrefix(name, "mpath"):
		return "multipath"
	default:
		return "disk"
	}
}

func collectFilesystems(ctx context.Context) ([]report.Filesystem, []report.FilesystemMetrics, error) {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil, nil, fmt.Errorf("collect filesystem inventory: %w", err)
	}
	uuidByDevice := filesystemUUIDs()
	inventory := make([]report.Filesystem, 0, len(partitions))
	metrics := make([]report.FilesystemMetrics, 0, len(partitions))
	seen := make(map[string]struct{})
	for _, partition := range partitions {
		if _, ok := seen[partition.Mountpoint]; ok {
			continue
		}
		usage, err := disk.UsageWithContext(ctx, partition.Mountpoint)
		if err != nil {
			continue
		}
		seen[partition.Mountpoint] = struct{}{}
		device := canonicalDevice(partition.Device)
		uuid := uuidByDevice[device]
		key := uuid
		if key == "" {
			key = partition.Device + "|" + partition.Mountpoint
		}
		inventory = append(inventory, report.Filesystem{
			Key:            key,
			UUID:           uuid,
			DeviceName:     partition.Device,
			FilesystemType: partition.Fstype,
			MountPoint:     partition.Mountpoint,
			MountOptions:   append([]string(nil), partition.Opts...),
		})
		metrics = append(metrics, report.FilesystemMetrics{
			FilesystemKey:  key,
			TotalBytes:     usage.Total,
			UsedBytes:      usage.Used,
			AvailableBytes: usage.Free,
			TotalInodes:    usage.InodesTotal,
			UsedInodes:     usage.InodesUsed,
		})
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Key < inventory[j].Key })
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].FilesystemKey < metrics[j].FilesystemKey })
	return inventory, metrics, nil
}

func filesystemUUIDs() map[string]string {
	result := make(map[string]string)
	paths, _ := filepath.Glob("/dev/disk/by-uuid/*")
	for _, path := range paths {
		device, err := filepath.EvalSymlinks(path)
		if err == nil {
			result[device] = filepath.Base(path)
		}
	}
	return result
}

func canonicalDevice(device string) string {
	resolved, err := filepath.EvalSymlinks(device)
	if err != nil {
		return device
	}
	return resolved
}

func (collector *Collector) collectNetwork(ctx context.Context) ([]report.NetworkInterface, []report.NetworkMetrics, error) {
	interfaces, err := gnet.InterfacesWithContext(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("collect network interfaces: %w", err)
	}
	counters, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil, nil, fmt.Errorf("collect network counters: %w", err)
	}
	counterByName := make(map[string]gnet.IOCountersStat, len(counters))
	for _, counter := range counters {
		counterByName[counter.Name] = counter
	}
	inventory := make([]report.NetworkInterface, 0, len(interfaces))
	metrics := make([]report.NetworkMetrics, 0, len(interfaces))
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for _, item := range interfaces {
		if containsString(item.Flags, "loopback") {
			continue
		}
		key := item.Name
		if item.HardwareAddr != "" {
			key += "|" + strings.ToLower(item.HardwareAddr)
		}
		addresses := make([]report.NetworkAddress, 0, len(item.Addrs))
		for _, address := range item.Addrs {
			addresses = append(addresses, report.NetworkAddress{Address: address.Addr, Scope: addressScope(address.Addr)})
		}
		inventory = append(inventory, report.NetworkInterface{
			Key:           key,
			Name:          item.Name,
			MACAddress:    item.HardwareAddr,
			MTU:           item.MTU,
			LinkSpeedMbps: readLinkSpeed(item.Name),
			Addresses:     addresses,
		})
		current := counterByName[item.Name]
		previous, hasPrevious := collector.previousNet[key]
		metric := report.NetworkMetrics{
			InterfaceKey: key,
			LinkUp:       containsString(item.Flags, "up"),
			RXBytesTotal: current.BytesRecv,
			TXBytesTotal: current.BytesSent,
		}
		if hasPrevious {
			metric.RXBytesDelta = counterDelta(current.BytesRecv, previous.BytesRecv)
			metric.TXBytesDelta = counterDelta(current.BytesSent, previous.BytesSent)
			metric.RXPacketsDelta = counterDelta(current.PacketsRecv, previous.PacketsRecv)
			metric.TXPacketsDelta = counterDelta(current.PacketsSent, previous.PacketsSent)
			metric.RXErrorsDelta = counterDelta(current.Errin, previous.Errin)
			metric.TXErrorsDelta = counterDelta(current.Errout, previous.Errout)
			metric.RXDroppedDelta = counterDelta(current.Dropin, previous.Dropin)
			metric.TXDroppedDelta = counterDelta(current.Dropout, previous.Dropout)
		}
		collector.previousNet[key] = current
		metrics = append(metrics, metric)
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Key < inventory[j].Key })
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].InterfaceKey < metrics[j].InterfaceKey })
	return inventory, metrics, nil
}

func addressScope(value string) string {
	ipValue, _, err := net.ParseCIDR(value)
	if err != nil {
		return "unknown"
	}
	switch {
	case ipValue.IsLoopback():
		return "host"
	case ipValue.IsLinkLocalUnicast(), ipValue.IsLinkLocalMulticast():
		return "link"
	case ipValue.IsPrivate():
		return "private"
	case ipValue.IsMulticast():
		return "multicast"
	case ipValue.IsGlobalUnicast():
		return "global"
	default:
		return "unknown"
	}
}

func isBridgeInterface(name string) bool {
	info, err := os.Stat(filepath.Join("/sys/class/net", name, "bridge"))
	return err == nil && info.IsDir()
}

func preferredBridgeIPv4(interfaces []report.NetworkInterface, isBridge func(string) bool) string {
	type candidate struct {
		address netip.Addr
		scope   int
	}
	candidates := make([]candidate, 0)
	for _, networkInterface := range interfaces {
		if !isBridge(networkInterface.Name) {
			continue
		}
		for _, networkAddress := range networkInterface.Addresses {
			prefix, err := netip.ParsePrefix(networkAddress.Address)
			if err != nil {
				continue
			}
			address := prefix.Addr().Unmap()
			if !address.Is4() {
				continue
			}
			scope := 3
			switch networkAddress.Scope {
			case "global":
				scope = 0
			case "private":
				scope = 1
			case "link":
				scope = 2
			}
			candidates = append(candidates, candidate{address: address, scope: scope})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].scope != candidates[j].scope {
			return candidates[i].scope < candidates[j].scope
		}
		return candidates[i].address.Compare(candidates[j].address) < 0
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].address.String()
}

func readLinkSpeed(name string) int {
	value, err := strconv.Atoi(readTrimmed(filepath.Join("/sys/class/net", name, "speed")))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func counterDelta(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func readTrimmed(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
