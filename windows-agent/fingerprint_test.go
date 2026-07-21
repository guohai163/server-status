package main

import "testing"

func TestInventoryFingerprintMatchesProtocolVector(t *testing.T) {
	inventory := Inventory{
		CPUPackages:   []CPUPackage{{Key: "cpu-package-0", PackageIndex: 0, ModelName: "Test CPU", PhysicalCores: 2, LogicalThreads: 2}},
		MemoryModules: []MemoryModule{{Key: "system-memory-aggregate", SizeBytes: 4096}},
		BlockDevices:  []BlockDevice{}, Filesystems: []Filesystem{}, NetworkInterfaces: []NetworkInterface{}, GPUs: []GPU{},
	}
	got, err := inventoryFingerprint(inventory)
	if err != nil {
		t.Fatal(err)
	}
	const want = "ba5121ec861b47d90369e1f1eb1305e95ef1401677ee92d3716bc37748839af7"
	if got != want {
		t.Fatalf("inventory fingerprint changed: got %s, want %s", got, want)
	}
}
