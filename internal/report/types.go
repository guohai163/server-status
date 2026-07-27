package report

import "time"

const Version = 1

type Report struct {
	Version              int       `json:"version"`
	CollectedAt          time.Time `json:"collected_at"`
	IntervalSeconds      int       `json:"interval_seconds"`
	InventoryFingerprint string    `json:"inventory_fingerprint"`
	Agent                AgentInfo `json:"agent"`
	Inventory            Inventory `json:"inventory"`
	Metrics              Metrics   `json:"metrics"`
}

type ReportReceipt struct {
	Status      string       `json:"status"`
	BucketAt    time.Time    `json:"bucket_at"`
	AgentUpdate *AgentUpdate `json:"agent_update,omitempty"`
}

type AgentUpdate struct {
	Version string `json:"version"`
}

type AgentInfo struct {
	ID            string            `json:"id"`
	Hostname      string            `json:"hostname"`
	OSName        string            `json:"os_name"`
	OSVersion     string            `json:"os_version,omitempty"`
	KernelVersion string            `json:"kernel_version,omitempty"`
	Architecture  string            `json:"architecture"`
	AgentVersion  string            `json:"agent_version"`
	MachineType   string            `json:"machine_type,omitempty"`
	SystemModel   string            `json:"system_model,omitempty"`
	PrimaryIP     string            `json:"primary_ip,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}

type Inventory struct {
	CPUPackages       []CPUPackage       `json:"cpu_packages"`
	MemoryModules     []MemoryModule     `json:"memory_modules"`
	BlockDevices      []BlockDevice      `json:"block_devices"`
	Filesystems       []Filesystem       `json:"filesystems"`
	NetworkInterfaces []NetworkInterface `json:"network_interfaces"`
	GPUs              []GPU              `json:"gpus,omitempty"`
}

type GPU struct {
	Key              string `json:"key"`
	Index            int    `json:"index"`
	UUID             string `json:"uuid"`
	ModelName        string `json:"model_name"`
	MemoryTotalBytes uint64 `json:"memory_total_bytes"`
}

type CPUPackage struct {
	Key              string  `json:"key"`
	PackageIndex     int     `json:"package_index"`
	Vendor           string  `json:"vendor,omitempty"`
	ModelName        string  `json:"model_name"`
	PhysicalCores    int     `json:"physical_cores"`
	LogicalThreads   int     `json:"logical_threads"`
	PerformanceCores int     `json:"performance_cores,omitempty"`
	EfficiencyCores  int     `json:"efficiency_cores,omitempty"`
	MaxFrequencyMHz  float64 `json:"max_frequency_mhz,omitempty"`
}

type MemoryModule struct {
	Key          string `json:"key"`
	SlotName     string `json:"slot_name,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	PartNumber   string `json:"part_number,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	MemoryType   string `json:"memory_type,omitempty"`
	SizeBytes    uint64 `json:"size_bytes"`
	SpeedMTs     int    `json:"speed_mts,omitempty"`
}

type BlockDevice struct {
	Key             string `json:"key"`
	DeviceName      string `json:"device_name"`
	DeviceKind      string `json:"device_kind"`
	Vendor          string `json:"vendor,omitempty"`
	ModelName       string `json:"model_name,omitempty"`
	SerialNumber    string `json:"serial_number,omitempty"`
	WWN             string `json:"wwn,omitempty"`
	SizeBytes       uint64 `json:"size_bytes"`
	Rotational      *bool  `json:"rotational,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	SMARTDeviceType string `json:"smart_device_type,omitempty"`
	RAIDPassthrough bool   `json:"raid_passthrough,omitempty"`
}

type Filesystem struct {
	Key            string   `json:"key"`
	UUID           string   `json:"uuid,omitempty"`
	DeviceName     string   `json:"device_name"`
	FilesystemType string   `json:"filesystem_type"`
	MountPoint     string   `json:"mount_point"`
	MountOptions   []string `json:"mount_options,omitempty"`
}

type NetworkInterface struct {
	Key           string           `json:"key"`
	Name          string           `json:"name"`
	MACAddress    string           `json:"mac_address,omitempty"`
	MTU           int              `json:"mtu,omitempty"`
	LinkSpeedMbps int              `json:"link_speed_mbps,omitempty"`
	Addresses     []NetworkAddress `json:"addresses,omitempty"`
}

type NetworkAddress struct {
	Address string `json:"address"`
	Scope   string `json:"scope"`
}

type Metrics struct {
	CPU             CPUMetrics             `json:"cpu"`
	Memory          MemoryMetrics          `json:"memory"`
	Disk            DiskMetrics            `json:"disk"`
	Filesystems     []FilesystemMetrics    `json:"filesystems"`
	Network         []NetworkMetrics       `json:"network"`
	GPUs            []GPUMetrics           `json:"gpus,omitempty"`
	Temperatures    []TemperatureMetrics   `json:"temperatures,omitempty"`
	StorageHealth   []StorageHealth        `json:"storage_health,omitempty"`
	RAIDControllers []RAIDControllerStatus `json:"raid_controllers,omitempty"`
}

type RAIDControllerStatus struct {
	ControllerID       int                       `json:"controller_id"`
	Model              string                    `json:"model"`
	State              string                    `json:"state"`
	PhysicalDriveCount int                       `json:"physical_drive_count"`
	DriveGroupCount    int                       `json:"drive_group_count"`
	VirtualDriveCount  int                       `json:"virtual_drive_count"`
	VirtualDrives      []RAIDVirtualDriveStatus  `json:"virtual_drives"`
	PhysicalDrives     []RAIDPhysicalDriveStatus `json:"physical_drives"`
}

type RAIDVirtualDriveStatus struct {
	DriveGroupID   int    `json:"drive_group_id"`
	VirtualDriveID int    `json:"virtual_drive_id"`
	RAIDLevel      string `json:"raid_level"`
	State          string `json:"state"`
	Access         string `json:"access,omitempty"`
	Consistency    string `json:"consistency,omitempty"`
	Cache          string `json:"cache,omitempty"`
	SizeBytes      uint64 `json:"size_bytes"`
	Name           string `json:"name,omitempty"`
}

type RAIDPhysicalDriveStatus struct {
	EnclosureID       *int     `json:"enclosure_id,omitempty"`
	Slot              int      `json:"slot"`
	DeviceID          int      `json:"device_id"`
	State             string   `json:"state"`
	DriveGroupID      *int     `json:"drive_group_id,omitempty"`
	SizeBytes         uint64   `json:"size_bytes"`
	Interface         string   `json:"interface"`
	MediaType         string   `json:"media_type"`
	SectorSizeBytes   uint64   `json:"sector_size_bytes,omitempty"`
	Model             string   `json:"model"`
	Spun              string   `json:"spun,omitempty"`
	DriveType         string   `json:"drive_type,omitempty"`
	RebuildProgress   *float64 `json:"rebuild_progress,omitempty"`
	RebuildStatus     string   `json:"rebuild_status,omitempty"`
	EstimatedTimeLeft string   `json:"estimated_time_left,omitempty"`
}

type TemperatureMetrics struct {
	Key                string   `json:"key"`
	Component          string   `json:"component"`
	Label              string   `json:"label"`
	TemperatureCelsius float64  `json:"temperature_celsius"`
	HighCelsius        *float64 `json:"high_celsius,omitempty"`
	CriticalCelsius    *float64 `json:"critical_celsius,omitempty"`
}

type StorageHealth struct {
	BlockDeviceKey          string   `json:"block_device_key"`
	SMARTAvailable          bool     `json:"smart_available"`
	SMARTEnabled            bool     `json:"smart_enabled"`
	SMARTStatus             string   `json:"smart_status"`
	RiskLevel               string   `json:"risk_level"`
	RiskReasons             []string `json:"risk_reasons,omitempty"`
	TemperatureCelsius      *float64 `json:"temperature_celsius,omitempty"`
	PowerOnHours            *uint64  `json:"power_on_hours,omitempty"`
	ErrorCount              *uint64  `json:"error_count,omitempty"`
	ReadErrorRateNormalized *uint64  `json:"read_error_rate_normalized,omitempty"`
	ReadErrorRateRaw        *uint64  `json:"read_error_rate_raw,omitempty"`
	ReallocatedSectors      *uint64  `json:"reallocated_sectors,omitempty"`
	PendingSectors          *uint64  `json:"pending_sectors,omitempty"`
	UncorrectableSectors    *uint64  `json:"uncorrectable_sectors,omitempty"`
	SCSIGrownDefects        *uint64  `json:"scsi_grown_defects,omitempty"`
	SCSIECCCorrected        *uint64  `json:"scsi_ecc_corrected,omitempty"`
	SCSINonMediumErrors     *uint64  `json:"scsi_non_medium_errors,omitempty"`
	PercentageUsed          *float64 `json:"percentage_used,omitempty"`
}

type GPUMetrics struct {
	GPUKey             string   `json:"gpu_key"`
	UtilizationPercent float64  `json:"utilization_percent"`
	MemoryUsedBytes    uint64   `json:"memory_used_bytes"`
	TemperatureCelsius *float64 `json:"temperature_celsius,omitempty"`
}

type CPUMetrics struct {
	UsagePercent float64 `json:"usage_percent"`
	Load1        float64 `json:"load_1"`
	Load5        float64 `json:"load_5"`
	Load15       float64 `json:"load_15"`
}

type MemoryMetrics struct {
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	CachedBytes    uint64 `json:"cached_bytes"`
	BuffersBytes   uint64 `json:"buffers_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	SwapUsedBytes  uint64 `json:"swap_used_bytes"`
	UptimeSeconds  uint64 `json:"uptime_seconds"`
}

type DiskMetrics struct {
	ReadBytesTotal  uint64 `json:"read_bytes_total"`
	WriteBytesTotal uint64 `json:"write_bytes_total"`
	ReadBytesDelta  uint64 `json:"read_bytes_delta"`
	WriteBytesDelta uint64 `json:"write_bytes_delta"`
	ReadOpsDelta    uint64 `json:"read_ops_delta"`
	WriteOpsDelta   uint64 `json:"write_ops_delta"`
}

type FilesystemMetrics struct {
	FilesystemKey  string `json:"filesystem_key"`
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	TotalInodes    uint64 `json:"total_inodes,omitempty"`
	UsedInodes     uint64 `json:"used_inodes,omitempty"`
}

type NetworkMetrics struct {
	InterfaceKey   string `json:"interface_key"`
	LinkUp         bool   `json:"link_up"`
	RXBytesTotal   uint64 `json:"rx_bytes_total"`
	TXBytesTotal   uint64 `json:"tx_bytes_total"`
	RXBytesDelta   uint64 `json:"rx_bytes_delta"`
	TXBytesDelta   uint64 `json:"tx_bytes_delta"`
	RXPacketsDelta uint64 `json:"rx_packets_delta"`
	TXPacketsDelta uint64 `json:"tx_packets_delta"`
	RXErrorsDelta  uint64 `json:"rx_errors_delta"`
	TXErrorsDelta  uint64 `json:"tx_errors_delta"`
	RXDroppedDelta uint64 `json:"rx_dropped_delta"`
	TXDroppedDelta uint64 `json:"tx_dropped_delta"`
}
