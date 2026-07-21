package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

func normalizeInventory(inventory *Inventory) {
	sort.Sort(cpuPackageSorter(inventory.CPUPackages))
	sort.Sort(memoryModuleSorter(inventory.MemoryModules))
	sort.Sort(blockDeviceSorter(inventory.BlockDevices))
	sort.Sort(filesystemSorter(inventory.Filesystems))
	sort.Sort(networkInterfaceSorter(inventory.NetworkInterfaces))
	sort.Sort(gpuSorter(inventory.GPUs))
	for i := range inventory.Filesystems {
		sort.Strings(inventory.Filesystems[i].MountOptions)
	}
	for i := range inventory.NetworkInterfaces {
		sort.Sort(networkAddressSorter(inventory.NetworkInterfaces[i].Addresses))
	}
}

func inventoryFingerprint(inventory Inventory) (string, error) {
	normalizeInventory(&inventory)
	encoded, err := json.Marshal(inventory)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type cpuPackageSorter []CPUPackage

func (s cpuPackageSorter) Len() int           { return len(s) }
func (s cpuPackageSorter) Less(i, j int) bool { return s[i].Key < s[j].Key }
func (s cpuPackageSorter) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

type memoryModuleSorter []MemoryModule

func (s memoryModuleSorter) Len() int           { return len(s) }
func (s memoryModuleSorter) Less(i, j int) bool { return s[i].Key < s[j].Key }
func (s memoryModuleSorter) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

type blockDeviceSorter []BlockDevice

func (s blockDeviceSorter) Len() int           { return len(s) }
func (s blockDeviceSorter) Less(i, j int) bool { return s[i].Key < s[j].Key }
func (s blockDeviceSorter) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

type filesystemSorter []Filesystem

func (s filesystemSorter) Len() int           { return len(s) }
func (s filesystemSorter) Less(i, j int) bool { return s[i].Key < s[j].Key }
func (s filesystemSorter) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

type networkInterfaceSorter []NetworkInterface

func (s networkInterfaceSorter) Len() int           { return len(s) }
func (s networkInterfaceSorter) Less(i, j int) bool { return s[i].Key < s[j].Key }
func (s networkInterfaceSorter) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

type gpuSorter []GPU

func (s gpuSorter) Len() int           { return len(s) }
func (s gpuSorter) Less(i, j int) bool { return s[i].Key < s[j].Key }
func (s gpuSorter) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

type networkAddressSorter []NetworkAddress

func (s networkAddressSorter) Len() int           { return len(s) }
func (s networkAddressSorter) Less(i, j int) bool { return s[i].Address < s[j].Address }
func (s networkAddressSorter) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
