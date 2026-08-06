package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

const testLibvirtDomainXML = `<domain type="kvm">
  <name>web-01</name>
  <uuid>4f08a52f-2f2e-4cb9-b12b-2692f8cdd463</uuid>
  <memory unit="KiB">8388608</memory>
  <currentMemory unit="KiB">4194304</currentMemory>
  <vcpu placement="static" current="2">4</vcpu>
  <os><type arch="x86_64" machine="pc-q35-8.2">hvm</type></os>
  <metadata><libosinfo:libosinfo xmlns:libosinfo="http://libosinfo.org/xmlns/libvirt/domain/1.0"><libosinfo:os id="http://ubuntu.com/ubuntu/22.04"/></libosinfo:libosinfo></metadata>
  <devices>
    <disk type="file" device="disk"><target dev="vda" bus="virtio"/></disk>
    <disk type="file" device="cdrom"><target dev="sda" bus="sata"/></disk>
  </devices>
</domain>`

func TestParseLibvirtDomainXML(t *testing.T) {
	item, targets, err := parseLibvirtDomainXML([]byte(testLibvirtDomainXML))
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "web-01" || item.Key != item.UUID || item.VCPUs != 2 || item.MemoryBytes != 4<<30 ||
		item.OSType != "hvm" || item.Architecture != "x86_64" || item.MachineType != "pc-q35-8.2" {
		t.Fatalf("unexpected domain: %+v", item)
	}
	if !reflect.DeepEqual(targets, []string{"vda"}) {
		t.Fatalf("unexpected disk targets: %#v", targets)
	}
	if got := parseLibvirtMetadataOS([]byte(testLibvirtDomainXML)); got != "22.04" {
		t.Fatalf("unexpected metadata OS %q", got)
	}
}

func TestCollectKVMVirtualMachinesWithVirsh(t *testing.T) {
	run := func(_ context.Context, arguments ...string) ([]byte, error) {
		switch arguments[0] {
		case "list":
			return []byte("4f08a52f-2f2e-4cb9-b12b-2692f8cdd463\n"), nil
		case "dumpxml":
			return []byte(testLibvirtDomainXML), nil
		case "domstate":
			return []byte("running\n"), nil
		case "domblkinfo":
			return []byte("Capacity:       53687091200\nAllocation:     10737418240\n"), nil
		case "domguestinfo":
			return []byte("os.id          : ubuntu\nos.pretty-name : Ubuntu 22.04.4 LTS\n"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	items, collected := collectKVMVirtualMachinesWithVirsh(t.Context(), run)
	if !collected || len(items) != 1 {
		t.Fatalf("unexpected collection result: collected=%t items=%+v", collected, items)
	}
	if items[0].State != "running" || items[0].DiskBytes != 50<<30 || items[0].OSName != "Ubuntu 22.04.4 LTS" {
		t.Fatalf("unexpected virtual machine: %+v", items[0])
	}
}

func TestKVMCollectionFailureIsNotAuthoritative(t *testing.T) {
	items, collected := collectKVMVirtualMachinesWithVirsh(t.Context(), func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("permission denied")
	})
	if collected || items != nil {
		t.Fatalf("failed collection must not clear stored inventory: collected=%t items=%+v", collected, items)
	}
}

func TestEmptyKVMCollectionIsAuthoritative(t *testing.T) {
	items, collected := collectKVMVirtualMachinesWithVirsh(t.Context(), func(_ context.Context, arguments ...string) ([]byte, error) {
		if len(arguments) == 3 && arguments[0] == "list" {
			return []byte("\n"), nil
		}
		return nil, errors.New("unexpected command")
	})
	if !collected || len(items) != 0 {
		t.Fatalf("successful empty collection must remove missing domains: collected=%t items=%+v", collected, items)
	}
}

func TestParseLibvirtGuestOS(t *testing.T) {
	if got := parseLibvirtGuestOS("os.id : ubuntu\nos.name : Ubuntu\nos.pretty-name : Ubuntu 24.04 LTS\n"); got != "Ubuntu 24.04 LTS" {
		t.Fatalf("unexpected guest OS %q", got)
	}
	if capacity, ok := parseLibvirtBlockCapacity("Capacity: 21474836480\n"); !ok || capacity != 20<<30 {
		t.Fatalf("unexpected block capacity: %d, %t", capacity, ok)
	}
}
