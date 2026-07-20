package report

import (
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
