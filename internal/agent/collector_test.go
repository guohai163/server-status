package agent

import (
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
