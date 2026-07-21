package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

func NormalizeInventory(inventory *Inventory) {
	sort.Slice(inventory.CPUPackages, func(i, j int) bool { return inventory.CPUPackages[i].Key < inventory.CPUPackages[j].Key })
	sort.Slice(inventory.MemoryModules, func(i, j int) bool { return inventory.MemoryModules[i].Key < inventory.MemoryModules[j].Key })
	sort.Slice(inventory.BlockDevices, func(i, j int) bool { return inventory.BlockDevices[i].Key < inventory.BlockDevices[j].Key })
	sort.Slice(inventory.Filesystems, func(i, j int) bool { return inventory.Filesystems[i].Key < inventory.Filesystems[j].Key })
	sort.Slice(inventory.NetworkInterfaces, func(i, j int) bool { return inventory.NetworkInterfaces[i].Key < inventory.NetworkInterfaces[j].Key })
	sort.Slice(inventory.GPUs, func(i, j int) bool { return inventory.GPUs[i].Key < inventory.GPUs[j].Key })
	for i := range inventory.Filesystems {
		sort.Strings(inventory.Filesystems[i].MountOptions)
	}
	for i := range inventory.NetworkInterfaces {
		sort.Slice(inventory.NetworkInterfaces[i].Addresses, func(a, b int) bool {
			return inventory.NetworkInterfaces[i].Addresses[a].Address < inventory.NetworkInterfaces[i].Addresses[b].Address
		})
	}
}

func InventoryFingerprint(inventory Inventory) (string, error) {
	NormalizeInventory(&inventory)
	encoded, err := json.Marshal(inventory)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
