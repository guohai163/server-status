package main

import (
	"testing"

	central "github.com/guohai/server-status/internal/report"
)

func TestInventoryFingerprintMatchesCentralProtocol(t *testing.T) {
	windowsInventory := Inventory{
		CPUPackages:   []CPUPackage{{Key: "cpu-package-0", PackageIndex: 0, ModelName: "Test CPU", PhysicalCores: 2, LogicalThreads: 2}},
		MemoryModules: []MemoryModule{{Key: "system-memory-aggregate", SizeBytes: 4096}},
		BlockDevices:  []BlockDevice{}, Filesystems: []Filesystem{}, NetworkInterfaces: []NetworkInterface{}, GPUs: []GPU{},
	}
	centralInventory := central.Inventory{
		CPUPackages:   []central.CPUPackage{{Key: "cpu-package-0", PackageIndex: 0, ModelName: "Test CPU", PhysicalCores: 2, LogicalThreads: 2}},
		MemoryModules: []central.MemoryModule{{Key: "system-memory-aggregate", SizeBytes: 4096}},
		BlockDevices:  []central.BlockDevice{}, Filesystems: []central.Filesystem{}, NetworkInterfaces: []central.NetworkInterface{}, GPUs: []central.GPU{},
	}
	got, err := inventoryFingerprint(windowsInventory)
	if err != nil {
		t.Fatal(err)
	}
	want, err := central.InventoryFingerprint(centralInventory)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Windows fingerprint %s does not match central fingerprint %s", got, want)
	}
}
