package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInventoryFingerprintIsOrderIndependent(t *testing.T) {
	first := Inventory{
		Filesystems: []Filesystem{
			{Key: "b", DeviceName: "/dev/b", FilesystemType: "xfs", MountPoint: "/b", MountOptions: []string{"rw", "noatime"}},
			{Key: "a", DeviceName: "/dev/a", FilesystemType: "ext4", MountPoint: "/a"},
		},
		NetworkInterfaces: []NetworkInterface{{
			Key: "eth0", Name: "eth0",
			Addresses: []NetworkAddress{{Address: "2001:db8::1/64", Scope: "global"}, {Address: "10.0.0.1/24", Scope: "private"}},
		}},
		GPUs: []GPU{{Key: "GPU-b", UUID: "GPU-b", ModelName: "GPU B", Index: 1, MemoryTotalBytes: 2000}, {Key: "GPU-a", UUID: "GPU-a", ModelName: "GPU A", Index: 0, MemoryTotalBytes: 1000}},
	}
	second := Inventory{
		Filesystems: []Filesystem{
			{Key: "a", DeviceName: "/dev/a", FilesystemType: "ext4", MountPoint: "/a"},
			{Key: "b", DeviceName: "/dev/b", FilesystemType: "xfs", MountPoint: "/b", MountOptions: []string{"noatime", "rw"}},
		},
		NetworkInterfaces: []NetworkInterface{{
			Key: "eth0", Name: "eth0",
			Addresses: []NetworkAddress{{Address: "10.0.0.1/24", Scope: "private"}, {Address: "2001:db8::1/64", Scope: "global"}},
		}},
		GPUs: []GPU{{Key: "GPU-a", UUID: "GPU-a", ModelName: "GPU A", Index: 0, MemoryTotalBytes: 1000}, {Key: "GPU-b", UUID: "GPU-b", ModelName: "GPU B", Index: 1, MemoryTotalBytes: 2000}},
	}
	firstDigest, err := InventoryFingerprint(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := InventoryFingerprint(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("fingerprints differ: %s != %s", firstDigest, secondDigest)
	}
}

func TestEmptyGPUInventoryRemainsWireCompatible(t *testing.T) {
	encoded, err := json.Marshal(Inventory{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "gpus") {
		t.Fatalf("empty GPU inventory must be omitted from legacy fingerprints: %s", encoded)
	}
}

func TestReportValidation(t *testing.T) {
	now := time.Now().UTC()
	payload := validTestReport(t, now)
	if err := payload.Validate(now); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	payload.Metrics.CPU.UsagePercent = 101
	if err := payload.Validate(now); err == nil {
		t.Fatal("CPU usage above 100 was accepted")
	}
}

func TestReportValidationAcceptsHeterogeneousCPUCoreTopology(t *testing.T) {
	now := time.Now().UTC()
	payload := validTestReport(t, now)
	payload.Inventory.CPUPackages[0].PerformanceCores = 3
	payload.Inventory.CPUPackages[0].EfficiencyCores = 1
	payload.InventoryFingerprint, _ = InventoryFingerprint(payload.Inventory)
	if err := payload.Validate(now); err != nil {
		t.Fatalf("valid heterogeneous CPU topology rejected: %v", err)
	}

	payload.Inventory.CPUPackages[0].EfficiencyCores = 2
	payload.InventoryFingerprint, _ = InventoryFingerprint(payload.Inventory)
	if err := payload.Validate(now); err == nil {
		t.Fatal("CPU topology that exceeds physical core count was accepted")
	}
}

func TestReportValidationRejectsUnknownResource(t *testing.T) {
	now := time.Now().UTC()
	payload := validTestReport(t, now)
	payload.Metrics.Filesystems[0].FilesystemKey = "missing"
	if err := payload.Validate(now); err == nil {
		t.Fatal("unknown filesystem metric key was accepted")
	}
}

func TestReportValidationRejectsDiskCounterOutsideBigint(t *testing.T) {
	now := time.Now().UTC()
	payload := validTestReport(t, now)
	payload.Metrics.Disk.ReadBytesTotal = ^uint64(0)
	if err := payload.Validate(now); err == nil {
		t.Fatal("disk counter outside PostgreSQL bigint was accepted")
	}
}

func TestReportValidationRejectsInvalidMachineType(t *testing.T) {
	now := time.Now().UTC()
	payload := validTestReport(t, now)
	payload.Agent.MachineType = "bare-metal"
	if err := payload.Validate(now); err == nil {
		t.Fatal("invalid machine type was accepted")
	}
}

func TestReportValidationRejectsInvalidPrimaryIP(t *testing.T) {
	now := time.Now().UTC()
	payload := validTestReport(t, now)
	payload.Agent.PrimaryIP = "10.0.0.1/24"
	if err := payload.Validate(now); err == nil {
		t.Fatal("primary IP with a prefix was accepted")
	}
}

func TestReportValidationAcceptsMultipleGPUs(t *testing.T) {
	now := time.Now().UTC()
	payload := validTestReport(t, now)
	payload.Inventory.GPUs = []GPU{
		{Key: "GPU-a", UUID: "GPU-a", ModelName: "NVIDIA RTX 3060", Index: 0, MemoryTotalBytes: 12 << 30},
		{Key: "GPU-b", UUID: "GPU-b", ModelName: "NVIDIA RTX 4090", Index: 1, MemoryTotalBytes: 24 << 30},
	}
	payload.Metrics.GPUs = []GPUMetrics{
		{GPUKey: "GPU-a", UtilizationPercent: 25, MemoryUsedBytes: 3 << 30},
		{GPUKey: "GPU-b", UtilizationPercent: 80, MemoryUsedBytes: 20 << 30},
	}
	payload.InventoryFingerprint, _ = InventoryFingerprint(payload.Inventory)
	if err := payload.Validate(now); err != nil {
		t.Fatalf("valid multi-GPU report rejected: %v", err)
	}
	payload.Metrics.GPUs[1].MemoryUsedBytes = 25 << 30
	if err := payload.Validate(now); err == nil {
		t.Fatal("GPU memory usage above total was accepted")
	}
}

func validTestReport(t *testing.T, now time.Time) Report {
	t.Helper()
	inventory := Inventory{
		CPUPackages: []CPUPackage{{Key: "cpu-0", ModelName: "Test CPU", PhysicalCores: 4, LogicalThreads: 8}},
		Filesystems: []Filesystem{{Key: "root", DeviceName: "/dev/sda1", FilesystemType: "ext4", MountPoint: "/"}},
		NetworkInterfaces: []NetworkInterface{{
			Key: "eth0", Name: "eth0", MACAddress: "02:00:00:00:00:01", MTU: 1500,
			Addresses: []NetworkAddress{{Address: "10.0.0.1/24", Scope: "private"}},
		}},
	}
	fingerprint, err := InventoryFingerprint(inventory)
	if err != nil {
		t.Fatal(err)
	}
	return Report{
		Version: Version, CollectedAt: now, IntervalSeconds: 60, InventoryFingerprint: fingerprint,
		Agent:     AgentInfo{ID: "20000000-0000-4000-8000-000000000001", Hostname: "test", OSName: "linux", Architecture: "amd64", AgentVersion: "test"},
		Inventory: inventory,
		Metrics: Metrics{
			CPU:         CPUMetrics{UsagePercent: 25},
			Memory:      MemoryMetrics{TotalBytes: 1000, UsedBytes: 500, AvailableBytes: 400},
			Filesystems: []FilesystemMetrics{{FilesystemKey: "root", TotalBytes: 1000, UsedBytes: 500, AvailableBytes: 400}},
			Network:     []NetworkMetrics{{InterfaceKey: "eth0", LinkUp: true}},
		},
	}
}
