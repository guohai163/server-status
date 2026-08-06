package agent

import (
	"context"
	"encoding/xml"
	"errors"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guohai/server-status/internal/report"
)

type virshCommand func(context.Context, ...string) ([]byte, error)

type libvirtDomainXML struct {
	Name          string         `xml:"name"`
	UUID          string         `xml:"uuid"`
	Memory        libvirtSize    `xml:"memory"`
	CurrentMemory libvirtSize    `xml:"currentMemory"`
	VCPU          libvirtVCPU    `xml:"vcpu"`
	OS            libvirtOS      `xml:"os"`
	Devices       libvirtDevices `xml:"devices"`
}

type libvirtSize struct {
	Unit  string `xml:"unit,attr"`
	Value uint64 `xml:",chardata"`
}

type libvirtVCPU struct {
	Current int `xml:"current,attr"`
	Value   int `xml:",chardata"`
}

type libvirtOS struct {
	Type libvirtOSType `xml:"type"`
}

type libvirtOSType struct {
	Architecture string `xml:"arch,attr"`
	MachineType  string `xml:"machine,attr"`
	Value        string `xml:",chardata"`
}

type libvirtDevices struct {
	Disks []libvirtDisk `xml:"disk"`
}

type libvirtDisk struct {
	Device string            `xml:"device,attr"`
	Target libvirtDiskTarget `xml:"target"`
}

type libvirtDiskTarget struct {
	Device string `xml:"dev,attr"`
}

func collectKVMVirtualMachines(ctx context.Context) ([]report.KVMVirtualMachine, bool) {
	if runtime.GOOS != "linux" {
		return nil, false
	}
	path, err := exec.LookPath("virsh")
	if err != nil {
		return nil, false
	}
	collectionContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	run := func(ctx context.Context, arguments ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, path, arguments...)
		command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
		return command.Output()
	}
	return collectKVMVirtualMachinesWithVirsh(collectionContext, run)
}

func collectKVMVirtualMachinesWithVirsh(ctx context.Context, run virshCommand) ([]report.KVMVirtualMachine, bool) {
	output, err := run(ctx, "list", "--all", "--uuid")
	if err != nil {
		return nil, false
	}
	identifiers := strings.Fields(string(output))
	items := make([]report.KVMVirtualMachine, 0, len(identifiers))
	for _, identifier := range identifiers {
		xmlOutput, err := run(ctx, "dumpxml", identifier)
		if err != nil {
			return nil, false
		}
		item, diskTargets, err := parseLibvirtDomainXML(xmlOutput)
		if err != nil {
			return nil, false
		}
		stateOutput, err := run(ctx, "domstate", identifier)
		if err != nil || strings.TrimSpace(string(stateOutput)) == "" {
			return nil, false
		}
		item.State = strings.TrimSpace(string(stateOutput))

		for _, target := range diskTargets {
			blockOutput, err := run(ctx, "domblkinfo", identifier, target)
			if err != nil {
				return nil, false
			}
			capacity, ok := parseLibvirtBlockCapacity(string(blockOutput))
			if !ok || capacity > math.MaxUint64-item.DiskBytes {
				return nil, false
			}
			item.DiskBytes += capacity
		}

		if guestOutput, guestErr := run(ctx, "domguestinfo", identifier, "--os"); guestErr == nil {
			item.OSName = parseLibvirtGuestOS(string(guestOutput))
		}
		if item.OSName == "" {
			item.OSName = parseLibvirtMetadataOS(xmlOutput)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items, true
}

func parseLibvirtDomainXML(data []byte) (report.KVMVirtualMachine, []string, error) {
	var domain libvirtDomainXML
	if err := xml.Unmarshal(data, &domain); err != nil {
		return report.KVMVirtualMachine{}, nil, err
	}
	domain.Name = strings.TrimSpace(domain.Name)
	domain.UUID = strings.TrimSpace(domain.UUID)
	if domain.Name == "" || domain.UUID == "" {
		return report.KVMVirtualMachine{}, nil, errors.New("libvirt domain has no name or UUID")
	}
	memory := domain.CurrentMemory
	if memory.Value == 0 {
		memory = domain.Memory
	}
	memoryBytes, ok := libvirtSizeBytes(memory)
	if !ok || memoryBytes == 0 {
		return report.KVMVirtualMachine{}, nil, errors.New("libvirt domain has invalid memory")
	}
	vcpus := domain.VCPU.Current
	if vcpus == 0 {
		vcpus = domain.VCPU.Value
	}
	if vcpus < 1 {
		return report.KVMVirtualMachine{}, nil, errors.New("libvirt domain has invalid vCPU count")
	}
	targets := make([]string, 0, len(domain.Devices.Disks))
	for _, disk := range domain.Devices.Disks {
		if disk.Device == "disk" && strings.TrimSpace(disk.Target.Device) != "" {
			targets = append(targets, strings.TrimSpace(disk.Target.Device))
		}
	}
	return report.KVMVirtualMachine{
		Key: domain.UUID, UUID: domain.UUID, Name: domain.Name, VCPUs: vcpus,
		MemoryBytes: memoryBytes, OSType: strings.TrimSpace(domain.OS.Type.Value),
		Architecture: strings.TrimSpace(domain.OS.Type.Architecture), MachineType: strings.TrimSpace(domain.OS.Type.MachineType),
	}, targets, nil
}

func libvirtSizeBytes(size libvirtSize) (uint64, bool) {
	multiplier := uint64(1)
	switch strings.ToLower(strings.TrimSpace(size.Unit)) {
	case "", "b", "bytes":
	case "k", "kb":
		multiplier = 1000
	case "kib":
		multiplier = 1 << 10
	case "m", "mb":
		multiplier = 1000 * 1000
	case "mib":
		multiplier = 1 << 20
	case "g", "gb":
		multiplier = 1000 * 1000 * 1000
	case "gib":
		multiplier = 1 << 30
	case "t", "tb":
		multiplier = 1000 * 1000 * 1000 * 1000
	case "tib":
		multiplier = 1 << 40
	default:
		return 0, false
	}
	if size.Value > math.MaxUint64/multiplier {
		return 0, false
	}
	return size.Value * multiplier, true
}

func parseLibvirtBlockCapacity(output string) (uint64, bool) {
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "Capacity" {
			continue
		}
		capacity, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		return capacity, err == nil
	}
	return 0, false
}

func parseLibvirtGuestOS(output string) string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(line, ":")
		if found {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"os.pretty-name", "os.name", "os.id"} {
		if values[key] != "" {
			return values[key]
		}
	}
	return ""
}

func parseLibvirtMetadataOS(data []byte) string {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "os" {
			continue
		}
		for _, attribute := range start.Attr {
			if attribute.Name.Local == "id" {
				value := strings.TrimRight(strings.TrimSpace(attribute.Value), "/")
				if index := strings.LastIndex(value, "/"); index >= 0 {
					value = value[index+1:]
				}
				return value
			}
		}
	}
}
