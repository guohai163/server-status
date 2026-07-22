package server

import (
	"bytes"
	"testing"
	"time"

	"github.com/guohai/server-status/internal/store"
	"github.com/xuri/excelize/v2"
)

func TestBuildNodeExportWorkbook(t *testing.T) {
	reportedAt := time.Date(2026, time.July, 22, 8, 30, 0, 0, time.UTC)
	rotational := true
	content, err := buildNodeExportWorkbook([]store.NodeDetail{{
		Node: store.NodeSummary{
			NodeID: "10000000-0000-4000-8000-000000000001", AgentID: "20000000-0000-4000-8000-000000000001", DisplayName: "生产节点", Hostname: "server-01", PrimaryIP: "10.0.0.1", Status: "online", MachineType: "physical",
			SystemModel: "ThinkSystem SR630 -[7X02CTO1WW]-", OSName: "Ubuntu", OSVersion: "24.04", Architecture: "amd64", Tags: []string{"production"},
			LastSeenAt: reportedAt, AgentVersion: "1.0.0", CPUModels: []string{"Intel Xeon"}, CPUPackageCount: 1, CPUPhysicalCoreCount: 8, CPULogicalThreadCount: 16,
			MemoryTotalBytes: 32 << 30, MemoryModuleCount: 2, DiskTotalBytes: 2 << 40, DiskCount: 2, CPUUsagePercent: 25.5, MemoryUsagePercent: 50.5,
			DiskUsagePercent: 42.5, Load1: 1.2, Load5: 1.1, Load15: 1.0, UptimeSeconds: 3600, NetworkRXBytesPerSec: 12, NetworkTXBytesPerSec: 34,
		},
		CPUPackages:   []store.CPUHardware{{PackageIndex: 0, Vendor: "Intel", ModelName: "Xeon Gold", PhysicalCores: 8, LogicalThreads: 16, MaxFrequencyMHz: 3200}},
		MemoryModules: []store.MemoryHardware{{SlotName: "DIMM A1", Manufacturer: "Samsung", ModelName: "M393", SizeBytes: 16 << 30, SpeedMTs: 3200}},
		BlockDevices:  []store.BlockDeviceHardware{{DeviceName: "/dev/sda", DeviceKind: "disk", ModelName: "PM9A3", SizeBytes: 1 << 40, Rotational: &rotational}},
		Filesystems:   []store.FilesystemStatus{{DeviceName: "/dev/sda1", FilesystemType: "ext4", MountPoint: "/", BucketAt: &reportedAt, TotalBytes: 1000, UsedBytes: 400, AvailableBytes: 600, UsedPercent: 40}},
		Network:       []store.NetworkStatus{{Name: "eth0", IsPrimary: true, MACAddress: "00:11:22:33:44:55", MTU: 1500, LinkSpeedMbps: 1000, Addresses: []string{"10.0.0.1/24"}, BucketAt: &reportedAt, LinkUp: true, RXBytesTotal: 10, TXBytesTotal: 20, RXBitsPerSecond: 30, TXBitsPerSecond: 40}},
		GPUs:          []store.GPUStatus{{Index: 0, UUID: "GPU-test", ModelName: "NVIDIA L4", BucketAt: &reportedAt, UtilizationPercent: 10, MemoryTotalBytes: 24 << 30, MemoryUsedBytes: 2 << 30, MemoryUsagePercent: 8.3}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 {
		t.Fatal("expected workbook content")
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("open export workbook: %v", err)
	}
	defer workbook.Close()
	if got, want := workbook.GetSheetList(), []string{"机器概览", "CPU", "内存", "磁盘", "文件系统", "网卡", "GPU"}; len(got) != len(want) {
		t.Fatalf("unexpected worksheets: %v", got)
	} else {
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("worksheet %d = %q, want %q", index, got[index], want[index])
			}
		}
	}
	if got, err := workbook.GetCellValue("机器概览", "C2"); err != nil || got != "生产节点" {
		t.Fatalf("summary name = %q, err=%v", got, err)
	}
	if got, err := workbook.GetCellValue("机器概览", "H2"); err != nil || got != "ThinkSystem SR630 -[7X02CTO1WW]-" {
		t.Fatalf("system model = %q, err=%v", got, err)
	}
	if got, err := workbook.GetCellValue("磁盘", "G2"); err != nil || got != "PM9A3" {
		t.Fatalf("disk model = %q, err=%v", got, err)
	}
}

func TestExcelTextPreventsFormulaExecution(t *testing.T) {
	if got := excelText("=1+1"); got != "'=1+1" {
		t.Fatalf("formula text = %q", got)
	}
	if got := excelText("server-01"); got != "server-01" {
		t.Fatalf("ordinary text = %q", got)
	}
}
