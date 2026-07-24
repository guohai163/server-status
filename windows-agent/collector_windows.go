//go:build windows
// +build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemTimes          = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx    = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetVersionExW           = kernel32.NewProc("GetVersionExW")
	procGetLogicalDriveStringsW = kernel32.NewProc("GetLogicalDriveStringsW")
	procGetDriveTypeW           = kernel32.NewProc("GetDriveTypeW")
	procGetDiskFreeSpaceExW     = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetVolumeInformationW   = kernel32.NewProc("GetVolumeInformationW")
	procGetTickCount            = kernel32.NewProc("GetTickCount")
	procGetTickCount64          = kernel32.NewProc("GetTickCount64")
	iphlpapi                    = syscall.NewLazyDLL("iphlpapi.dll")
	procGetIfTable              = iphlpapi.NewProc("GetIfTable")
)

type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func (value filetime) uint64() uint64 {
	return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
}

type memoryStatusEx struct {
	Length            uint32
	MemoryLoad        uint32
	TotalPhysical     uint64
	AvailablePhysical uint64
	TotalPageFile     uint64
	AvailablePageFile uint64
	TotalVirtual      uint64
	AvailableVirtual  uint64
	AvailableExtended uint64
}

type osVersionInfoEx struct {
	Size             uint32
	MajorVersion     uint32
	MinorVersion     uint32
	BuildNumber      uint32
	PlatformID       uint32
	CSDVersion       [128]uint16
	ServicePackMajor uint16
	ServicePackMinor uint16
	SuiteMask        uint16
	ProductType      byte
	Reserved         byte
}

type cpuTimes struct {
	idle   uint64
	kernel uint64
	user   uint64
}

type WindowsCollector struct {
	mu             sync.Mutex
	previousCPU    cpuTimes
	hasPreviousCPU bool
	network        map[int]networkCounterState
	lastCollection time.Time
}

func newWindowsCollector() *WindowsCollector {
	return &WindowsCollector{network: make(map[int]networkCounterState)}
}

func (collector *WindowsCollector) Collect(config Config) (Report, error) {
	now := time.Now().UTC()
	hostname, err := os.Hostname()
	if err != nil {
		return Report{}, fmt.Errorf("read hostname: %v", err)
	}
	memory, err := readMemoryStatus()
	if err != nil {
		return Report{}, err
	}
	cpuUsage, err := collector.readCPUUsage()
	if err != nil {
		return Report{}, err
	}
	filesystems, filesystemMetrics := collectVolumes()
	networkInterfaces, networkMetrics, primaryIP := collector.collectNetworkInterfaces()
	blockDevices := []BlockDevice{}
	storageHealth := []StorageHealth(nil)
	if smartctlPath := installedSmartctlPath(); smartctlPath != "" {
		smartContext, cancelSMART := context.WithTimeout(context.Background(), 25*time.Second)
		blockDevices, storageHealth = collectStorageHealth(smartContext, smartctlPath, blockDevices)
		cancelSMART()
	}
	logicalProcessors := runtime.NumCPU()
	if logicalProcessors < 1 {
		logicalProcessors = 1
	}
	cpuModel := strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER"))
	if cpuModel == "" {
		cpuModel = "Windows Processor"
	}

	collector.mu.Lock()
	intervalSeconds := config.IntervalSeconds
	if !collector.lastCollection.IsZero() {
		intervalSeconds = int(now.Sub(collector.lastCollection).Seconds() + 0.5)
	}
	collector.lastCollection = now
	collector.mu.Unlock()
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}
	if intervalSeconds > 3600 {
		intervalSeconds = 3600
	}

	pageFileTotal := uint64(0)
	if memory.TotalPageFile > memory.TotalPhysical {
		pageFileTotal = memory.TotalPageFile - memory.TotalPhysical
	}
	inventory := Inventory{
		CPUPackages: []CPUPackage{{
			Key: "cpu-package-0", PackageIndex: 0, ModelName: cpuModel,
			PhysicalCores: logicalProcessors, LogicalThreads: logicalProcessors,
		}},
		MemoryModules: []MemoryModule{{
			Key: "system-memory-aggregate", SlotName: "aggregate",
			ModelName: "System Memory (Windows aggregate)", SizeBytes: memory.TotalPhysical,
		}},
		BlockDevices:      blockDevices,
		Filesystems:       filesystems,
		NetworkInterfaces: networkInterfaces,
		GPUs:              []GPU{},
	}
	normalizeInventory(&inventory)
	fingerprint, err := inventoryFingerprint(inventory)
	if err != nil {
		return Report{}, fmt.Errorf("fingerprint inventory: %v", err)
	}
	osName, osVersion, kernelVersion := readWindowsVersion()
	payload := Report{
		Version: reportVersion, CollectedAt: now, IntervalSeconds: intervalSeconds,
		InventoryFingerprint: fingerprint,
		Agent: AgentInfo{
			ID: config.AgentID, Hostname: hostname, OSName: osName,
			OSVersion: osVersion, KernelVersion: kernelVersion,
			Architecture: runtime.GOARCH, AgentVersion: Version,
			PrimaryIP: primaryIP, Labels: config.Labels,
		},
		Inventory: inventory,
		Metrics: Metrics{
			CPU: CPUMetrics{UsagePercent: cpuUsage},
			Memory: MemoryMetrics{
				TotalBytes: memory.TotalPhysical, UsedBytes: memory.TotalPhysical - memory.AvailablePhysical,
				AvailableBytes: memory.AvailablePhysical, SwapTotalBytes: pageFileTotal,
				UptimeSeconds: readUptimeSeconds(),
			},
			Disk: DiskMetrics{}, Filesystems: filesystemMetrics,
			Network: networkMetrics, GPUs: []GPUMetrics{}, StorageHealth: storageHealth,
		},
	}
	return payload, nil
}

func readSystemTimes() (cpuTimes, error) {
	var idle, kernel, user filetime
	ok, _, callErr := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if ok == 0 {
		return cpuTimes{}, fmt.Errorf("GetSystemTimes failed: %v", callErr)
	}
	return cpuTimes{idle: idle.uint64(), kernel: kernel.uint64(), user: user.uint64()}, nil
}

func (collector *WindowsCollector) readCPUUsage() (float64, error) {
	current, err := readSystemTimes()
	if err != nil {
		return 0, err
	}
	collector.mu.Lock()
	if !collector.hasPreviousCPU {
		collector.previousCPU = current
		collector.hasPreviousCPU = true
		collector.mu.Unlock()
		time.Sleep(time.Second)
		current, err = readSystemTimes()
		if err != nil {
			return 0, err
		}
		collector.mu.Lock()
	}
	previous := collector.previousCPU
	collector.previousCPU = current
	collector.mu.Unlock()
	if current.kernel < previous.kernel || current.user < previous.user || current.idle < previous.idle {
		return 0, nil
	}
	total := (current.kernel - previous.kernel) + (current.user - previous.user)
	idle := current.idle - previous.idle
	if total == 0 || idle > total {
		return 0, nil
	}
	usage := float64(total-idle) * 100 / float64(total)
	if usage < 0 {
		return 0, nil
	}
	if usage > 100 {
		return 100, nil
	}
	return usage, nil
}

func readMemoryStatus() (memoryStatusEx, error) {
	status := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	ok, _, callErr := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return memoryStatusEx{}, fmt.Errorf("GlobalMemoryStatusEx failed: %v", callErr)
	}
	if status.TotalPhysical == 0 || status.AvailablePhysical > status.TotalPhysical {
		return memoryStatusEx{}, errors.New("GlobalMemoryStatusEx returned invalid physical memory values")
	}
	return status, nil
}

func readWindowsVersion() (string, string, string) {
	productName := readWindowsProductName()
	info := osVersionInfoEx{Size: uint32(unsafe.Sizeof(osVersionInfoEx{}))}
	ok, _, _ := procGetVersionExW.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if productName != "" {
			return productName, "unknown", "unknown"
		}
		return "Windows", "unknown", "unknown"
	}
	version := fmt.Sprintf("%d.%d", info.MajorVersion, info.MinorVersion)
	servicePack := syscall.UTF16ToString(info.CSDVersion[:])
	if servicePack != "" {
		version += " " + servicePack
	}
	return windowsProductName(productName, info.MajorVersion, info.MinorVersion), version, strconv.FormatUint(uint64(info.BuildNumber), 10)
}

func readWindowsProductName() string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetStringValue("ProductName")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func readUptimeSeconds() uint64 {
	if procGetTickCount64.Find() == nil {
		low, high, _ := procGetTickCount64.Call()
		if unsafe.Sizeof(uintptr(0)) == 4 {
			return (uint64(high)<<32 | uint64(low)) / 1000
		}
		return uint64(low) / 1000
	}
	milliseconds, _, _ := procGetTickCount.Call()
	return uint64(uint32(milliseconds)) / 1000
}

func collectVolumes() ([]Filesystem, []FilesystemMetrics) {
	buffer := make([]uint16, 512)
	length, _, _ := procGetLogicalDriveStringsW.Call(uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0])))
	if length == 0 || int(length) >= len(buffer) {
		return []Filesystem{}, []FilesystemMetrics{}
	}
	filesystems := make([]Filesystem, 0)
	metrics := make([]FilesystemMetrics, 0)
	for _, root := range splitUTF16Strings(buffer[:length]) {
		rootPointer, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		driveType, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(rootPointer)))
		if driveType == 0 || driveType == 1 || driveType == 5 {
			continue
		}
		var available, total, free uint64
		ok, _, _ := procGetDiskFreeSpaceExW.Call(
			uintptr(unsafe.Pointer(rootPointer)), uintptr(unsafe.Pointer(&available)),
			uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
		if ok == 0 || total == 0 || free > total {
			continue
		}
		filesystemType, serial := volumeInformation(rootPointer)
		if filesystemType == "" {
			filesystemType = "unknown"
		}
		key := fmt.Sprintf("%08x|%s", serial, strings.ToUpper(root))
		filesystems = append(filesystems, Filesystem{
			Key: key, UUID: fmt.Sprintf("%08x", serial), DeviceName: root,
			FilesystemType: filesystemType, MountPoint: root, MountOptions: []string{},
		})
		metrics = append(metrics, FilesystemMetrics{
			FilesystemKey: key, TotalBytes: total, UsedBytes: total - free, AvailableBytes: available,
		})
	}
	return filesystems, metrics
}

func volumeInformation(root *uint16) (string, uint32) {
	filesystemName := make([]uint16, 64)
	var serial, maxComponentLength, flags uint32
	ok, _, _ := procGetVolumeInformationW.Call(
		uintptr(unsafe.Pointer(root)), 0, 0, uintptr(unsafe.Pointer(&serial)),
		uintptr(unsafe.Pointer(&maxComponentLength)), uintptr(unsafe.Pointer(&flags)),
		uintptr(unsafe.Pointer(&filesystemName[0])), uintptr(len(filesystemName)))
	if ok == 0 {
		return "", 0
	}
	return syscall.UTF16ToString(filesystemName), serial
}

func splitUTF16Strings(values []uint16) []string {
	result := make([]string, 0)
	start := 0
	for index, value := range values {
		if value == 0 {
			if index > start {
				result = append(result, syscall.UTF16ToString(values[start:index]))
			}
			start = index + 1
		}
	}
	if start < len(values) {
		result = append(result, syscall.UTF16ToString(values[start:]))
	}
	return result
}

type mibIfRow struct {
	Name             [256]uint16
	Index            uint32
	Type             uint32
	MTU              uint32
	Speed            uint32
	PhysicalAddrLen  uint32
	PhysicalAddr     [8]byte
	AdminStatus      uint32
	OperStatus       uint32
	LastChange       uint32
	InOctets         uint32
	InUcastPackets   uint32
	InNUcastPackets  uint32
	InDiscards       uint32
	InErrors         uint32
	InUnknown        uint32
	OutOctets        uint32
	OutUcastPackets  uint32
	OutNUcastPackets uint32
	OutDiscards      uint32
	OutErrors        uint32
	OutQueueLength   uint32
	DescriptionLen   uint32
	Description      [256]byte
}

type networkCounterState struct {
	initialized             bool
	inOctets, outOctets     uint32
	inPackets, outPackets   uint32
	inErrors, outErrors     uint32
	inDiscards, outDiscards uint32
	totalIn, totalOut       uint64
}

func readInterfaceRows() map[int]mibIfRow {
	var size uint32
	procGetIfTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if size < 4 {
		return make(map[int]mibIfRow)
	}
	buffer := make([]byte, size)
	result, _, _ := procGetIfTable.Call(
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0)
	if result != 0 || len(buffer) < 4 {
		return make(map[int]mibIfRow)
	}
	count := *(*uint32)(unsafe.Pointer(&buffer[0]))
	rowSize := int(unsafe.Sizeof(mibIfRow{}))
	rows := make(map[int]mibIfRow)
	for index := 0; index < int(count); index++ {
		offset := 4 + index*rowSize
		if offset+rowSize > len(buffer) {
			break
		}
		row := *(*mibIfRow)(unsafe.Pointer(&buffer[offset]))
		rows[int(row.Index)] = row
	}
	return rows
}

func (collector *WindowsCollector) collectNetworkInterfaces() ([]NetworkInterface, []NetworkMetrics, string) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return []NetworkInterface{}, []NetworkMetrics{}, ""
	}
	inventory := make([]NetworkInterface, 0, len(interfaces))
	metrics := make([]NetworkMetrics, 0, len(interfaces))
	primaryIP := ""
	rows := readInterfaceRows()
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for _, item := range interfaces {
		if item.Flags&net.FlagLoopback != 0 {
			continue
		}
		mac := ""
		if len(item.HardwareAddr) == 6 {
			mac = strings.ToLower(item.HardwareAddr.String())
		}
		key := item.Name
		if mac != "" {
			key += "|" + mac
		}
		addresses := make([]NetworkAddress, 0)
		rawAddresses, _ := item.Addrs()
		for _, raw := range rawAddresses {
			address := raw.String()
			ip, _, parseErr := net.ParseCIDR(address)
			if parseErr != nil {
				continue
			}
			addresses = append(addresses, NetworkAddress{Address: address, Scope: addressScope(ip)})
			if primaryIP == "" && ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				primaryIP = ip.String()
			}
		}
		row, hasRow := rows[item.Index]
		linkSpeed := 0
		if hasRow {
			linkSpeed = int(row.Speed / 1000000)
		}
		inventory = append(inventory, NetworkInterface{
			Key: key, Name: item.Name, MACAddress: mac, MTU: item.MTU,
			LinkSpeedMbps: linkSpeed, Addresses: addresses,
		})
		metric := NetworkMetrics{InterfaceKey: key, LinkUp: item.Flags&net.FlagUp != 0}
		if hasRow {
			state := collector.network[item.Index]
			if state.initialized {
				metric.RXBytesDelta = counterDelta32(row.InOctets, state.inOctets)
				metric.TXBytesDelta = counterDelta32(row.OutOctets, state.outOctets)
				metric.RXPacketsDelta = counterDelta32(row.InUcastPackets+row.InNUcastPackets, state.inPackets)
				metric.TXPacketsDelta = counterDelta32(row.OutUcastPackets+row.OutNUcastPackets, state.outPackets)
				metric.RXErrorsDelta = counterDelta32(row.InErrors, state.inErrors)
				metric.TXErrorsDelta = counterDelta32(row.OutErrors, state.outErrors)
				metric.RXDroppedDelta = counterDelta32(row.InDiscards, state.inDiscards)
				metric.TXDroppedDelta = counterDelta32(row.OutDiscards, state.outDiscards)
				state.totalIn += metric.RXBytesDelta
				state.totalOut += metric.TXBytesDelta
			} else {
				state.initialized = true
				state.totalIn = uint64(row.InOctets)
				state.totalOut = uint64(row.OutOctets)
			}
			state.inOctets, state.outOctets = row.InOctets, row.OutOctets
			state.inPackets = row.InUcastPackets + row.InNUcastPackets
			state.outPackets = row.OutUcastPackets + row.OutNUcastPackets
			state.inErrors, state.outErrors = row.InErrors, row.OutErrors
			state.inDiscards, state.outDiscards = row.InDiscards, row.OutDiscards
			collector.network[item.Index] = state
			metric.RXBytesTotal, metric.TXBytesTotal = state.totalIn, state.totalOut
			metric.LinkUp = row.OperStatus == 5
		}
		metrics = append(metrics, metric)
	}
	return inventory, metrics, primaryIP
}

func counterDelta32(current, previous uint32) uint64 {
	if current >= previous {
		return uint64(current - previous)
	}
	return uint64(current) + (uint64(1) << 32) - uint64(previous)
}

func addressScope(ip net.IP) string {
	if ip.IsLoopback() {
		return "host"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return "link"
	}
	if isPrivateIP(ip) {
		return "private"
	}
	if ip.IsMulticast() {
		return "multicast"
	}
	return "global"
}

func isPrivateIP(ip net.IP) bool {
	value := ip.To4()
	if value != nil {
		return value[0] == 10 || (value[0] == 172 && value[1] >= 16 && value[1] <= 31) || (value[0] == 192 && value[1] == 168)
	}
	return len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc
}
