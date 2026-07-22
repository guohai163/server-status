package agent

import (
	"errors"
	"testing"

	"github.com/guohai/server-status/internal/report"
	"github.com/shirou/gopsutil/v4/disk"
)

func TestParseDMIMemoryModules(t *testing.T) {
	input := `
Memory Device
	Size: 16 GB
	Locator: DIMM_A1
	Manufacturer: Example
	Part Number: MEM-16G
	Serial Number: ABC123
	Type: DDR4
	Configured Memory Speed: 3200 MT/s
Memory Device
	Size: No Module Installed
	Locator: DIMM_A2
`
	modules := parseDMIMemoryModules(input)
	if len(modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(modules))
	}
	if modules[0].Key != "DIMM_A1" || modules[0].SizeBytes != 16*1024*1024*1024 || modules[0].SpeedMTs != 3200 {
		t.Fatalf("unexpected module: %+v", modules[0])
	}
}

func TestCounterDeltaHandlesReset(t *testing.T) {
	if got := counterDelta(150, 100); got != 50 {
		t.Fatalf("expected delta 50, got %d", got)
	}
	if got := counterDelta(10, 100); got != 0 {
		t.Fatalf("counter reset should produce zero, got %d", got)
	}
}

func TestAddressScope(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1/8":    "host",
		"169.254.1.1/16": "link",
		"10.0.0.1/24":    "private",
		"8.8.8.8/32":     "global",
	}
	for address, expected := range tests {
		if got := addressScope(address); got != expected {
			t.Errorf("scope for %s: expected %s, got %s", address, expected, got)
		}
	}
}

func TestPreferredBridgeIPv4(t *testing.T) {
	interfaces := []report.NetworkInterface{
		{Name: "eno1", Addresses: []report.NetworkAddress{{Address: "10.100.119.241/24", Scope: "private"}}},
		{Name: "vmbr0", Addresses: []report.NetworkAddress{
			{Address: "2001:db8::1/64", Scope: "global"},
			{Address: "10.100.119.183/24", Scope: "private"},
		}},
	}
	isBridge := func(name string) bool { return name == "vmbr0" }
	if got := preferredBridgeIPv4(interfaces, isBridge); got != "10.100.119.183" {
		t.Fatalf("expected bridge IPv4, got %q", got)
	}
	if got := preferredBridgeIPv4(interfaces, func(string) bool { return false }); got != "" {
		t.Fatalf("expected no preferred address without a bridge, got %q", got)
	}
}

func TestClassifyMachineType(t *testing.T) {
	if got := classifyMachineType("guest"); got != "virtual" {
		t.Fatalf("guest classified as %q", got)
	}
	for _, role := range []string{"host", ""} {
		if got := classifyMachineType(role); got != "physical" {
			t.Fatalf("role %q classified as %q", role, got)
		}
	}
}

func TestFormatSystemModel(t *testing.T) {
	tests := []struct {
		name, productName, productSKU, want string
	}{
		{name: "product and SKU", productName: "ThinkSystem SR630", productSKU: "7X02CTO1WW", want: "ThinkSystem SR630 -[7X02CTO1WW]-"},
		{name: "product only", productName: "PowerEdge R650", want: "PowerEdge R650"},
		{name: "unavailable product", productName: "Not Specified", productSKU: "7X02CTO1WW", want: ""},
		{name: "unavailable SKU", productName: "ThinkSystem SR630", productSKU: "None", want: "ThinkSystem SR630"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatSystemModel(test.productName, test.productSKU); got != test.want {
				t.Fatalf("formatSystemModel() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBlockDeviceKind(t *testing.T) {
	if blockDeviceKind("md0") != "raid" || blockDeviceKind("dm-0") != "virtual" || blockDeviceKind("nvme0n1") != "disk" {
		t.Fatal("block device kind classification is incorrect")
	}
}

func TestFallbackBlockModel(t *testing.T) {
	if got := fallbackBlockModel("vda", "0x1af4"); got != "Virtio Block Device (vda)" {
		t.Fatalf("unexpected virtio fallback model: %s", got)
	}
	if got := fallbackBlockModel("sda", ""); got != "Block Device (sda)" {
		t.Fatalf("unexpected generic fallback model: %s", got)
	}
}

func TestDiskCounterDeltaAggregation(t *testing.T) {
	collector := NewCollector()
	collector.previousDisk["sda"] = disk.IOCountersStat{ReadBytes: 100, WriteBytes: 200, ReadCount: 4, WriteCount: 8}
	metric := aggregateDiskCounters(collector.previousDisk, []string{"sda"}, map[string]disk.IOCountersStat{
		"sda": {ReadBytes: 160, WriteBytes: 290, ReadCount: 7, WriteCount: 10},
	})
	if metric.ReadBytesDelta != 60 || metric.WriteBytesDelta != 90 || metric.ReadOpsDelta != 3 || metric.WriteOpsDelta != 2 {
		t.Fatalf("unexpected disk deltas: %+v", metric)
	}
}

func TestCollectFilesystemStatsDeduplicatesUUID(t *testing.T) {
	partitions := []disk.PartitionStat{
		{Device: "/dev/sdb1", Mountpoint: "/data0", Fstype: "ext4"},
		{Device: "/dev/sdb1", Mountpoint: "/log0", Fstype: "ext4"},
		{Device: "/dev/sdb1", Mountpoint: "/backup0", Fstype: "ext4"},
	}
	usageCalls := 0
	inventory, metrics, err := collectFilesystemStats(partitions, map[string]string{
		"/dev/sdb1": "f5dbe7ae-4f59-4731-aa7a-6ab4595dab77",
	}, func(string) (*disk.UsageStat, error) {
		usageCalls++
		return &disk.UsageStat{Total: 1000, Used: 400, Free: 600}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 1 || len(metrics) != 1 || usageCalls != 1 {
		t.Fatalf("expected one filesystem sample, got inventory=%d metrics=%d usage_calls=%d", len(inventory), len(metrics), usageCalls)
	}
	if inventory[0].Key != "f5dbe7ae-4f59-4731-aa7a-6ab4595dab77" || inventory[0].MountPoint != "/data0" {
		t.Fatalf("unexpected filesystem: %+v", inventory[0])
	}
}

func TestCollectFilesystemStatsRetriesDuplicateUUIDAfterUsageFailure(t *testing.T) {
	partitions := []disk.PartitionStat{
		{Device: "/dev/sdb1", Mountpoint: "/data0", Fstype: "ext4"},
		{Device: "/dev/sdb1", Mountpoint: "/log0", Fstype: "ext4"},
	}
	inventory, metrics, err := collectFilesystemStats(partitions, map[string]string{
		"/dev/sdb1": "filesystem-uuid",
	}, func(mountpoint string) (*disk.UsageStat, error) {
		if mountpoint == "/data0" {
			return nil, errors.New("unavailable")
		}
		return &disk.UsageStat{Total: 1000, Used: 400, Free: 600}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 1 || len(metrics) != 1 || inventory[0].MountPoint != "/log0" {
		t.Fatalf("expected fallback mountpoint, got inventory=%+v metrics=%+v", inventory, metrics)
	}
}

func TestParseNVIDIASMIMultipleGPUs(t *testing.T) {
	inventory, metrics := parseNVIDIASMI(`GPU-b, NVIDIA RTX 4090, 1, 88, 16384, 24564
GPU-a, NVIDIA GeForce RTX 3060, 0, 37, 2048, 12288
`)
	if len(inventory) != 2 || len(metrics) != 2 {
		t.Fatalf("expected two GPUs, got inventory=%d metrics=%d", len(inventory), len(metrics))
	}
	if inventory[0].UUID != "GPU-a" || inventory[0].Index != 0 || inventory[0].MemoryTotalBytes != 12288*1024*1024 {
		t.Fatalf("unexpected first GPU: %+v", inventory[0])
	}
	if metrics[1].GPUKey != "GPU-b" || metrics[1].UtilizationPercent != 88 || metrics[1].MemoryUsedBytes != 16384*1024*1024 {
		t.Fatalf("unexpected second GPU metrics: %+v", metrics[1])
	}
}

func TestParseNVIDIASMISkipsUnavailableRows(t *testing.T) {
	inventory, metrics := parseNVIDIASMI("malformed,row\nGPU-a, NVIDIA GPU, 0, [N/A], 0, 8192\nGPU-b, NVIDIA GPU, 1, 50, 1024, 8192\n")
	if len(inventory) != 1 || len(metrics) != 1 || inventory[0].UUID != "GPU-b" {
		t.Fatalf("expected malformed and unavailable rows to be skipped: inventory=%+v metrics=%+v", inventory, metrics)
	}
}
