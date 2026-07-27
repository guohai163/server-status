package agent

import (
	"math"
	"testing"
)

const storCLIStatusFixture = `warning before JSON
{
  "Controllers": [{
    "Command Status": {
      "Controller": 0,
      "Status": "Success"
    },
    "Response Data": {
      "Basics": {
        "Controller": 0,
        "Model": "PERC H330 Adapter"
      },
      "Status": {
        "Controller Status": "Needs Attention"
      },
      "Physical Drives": 2,
      "Drive Groups": 1,
      "Virtual Drives": 1,
      "VD LIST": [{
        "DG/VD": "0/0",
        "TYPE": "RAID1",
        "State": "Dgrd",
        "Access": "RW",
        "Consist": "No",
        "Cache": "NRWTD",
        "Size": "1.818 TB",
        "Name": "GenericR1_0"
      }],
      "PD LIST": [{
        "EID:Slt": "32:0",
        "DID": 0,
        "State": "Rbld",
        "DG": 0,
        "Size": "1.819 TB",
        "Intf": "SATA",
        "Med": "HDD",
        "SeSz": "512B",
        "Model": "Replacement SATA Disk",
        "Sp": "U",
        "Type": "-"
      }, {
        "EID:Slt": "32:1",
        "DID": 1,
        "State": "Onln",
        "DG": 0,
        "Size": "1.818 TB",
        "Intf": "SATA",
        "Med": "HDD",
        "SeSz": "512B",
        "Model": "ST2000DM001-1ER164",
        "Sp": "U",
        "Type": "-"
      }]
    }
  }]
}`

func TestParseStorCLIStatus(t *testing.T) {
	controllers, err := parseStorCLIStatus([]byte(storCLIStatusFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(controllers) != 1 {
		t.Fatalf("got %d controllers, want 1", len(controllers))
	}
	controller := controllers[0]
	if controller.ControllerID != 0 || controller.Model != "PERC H330 Adapter" || controller.State != "Needs Attention" {
		t.Fatalf("unexpected controller: %+v", controller)
	}
	if controller.DriveGroupCount != 1 || len(controller.VirtualDrives) != 1 || len(controller.PhysicalDrives) != 2 {
		t.Fatalf("unexpected RAID topology: %+v", controller)
	}
	virtualDrive := controller.VirtualDrives[0]
	if virtualDrive.RAIDLevel != "RAID1" || virtualDrive.State != "Dgrd" || virtualDrive.SizeBytes == 0 {
		t.Fatalf("unexpected virtual drive: %+v", virtualDrive)
	}
	physicalDrive := controller.PhysicalDrives[0]
	if physicalDrive.EnclosureID == nil || *physicalDrive.EnclosureID != 32 || physicalDrive.Slot != 0 || physicalDrive.State != "Rbld" || physicalDrive.Interface != "SATA" || physicalDrive.SectorSizeBytes != 512 {
		t.Fatalf("unexpected physical drive: %+v", physicalDrive)
	}
	if !hasRebuildingDrive(controllers) {
		t.Fatal("rebuilding drive was not detected")
	}
}

func TestApplyStorCLIRebuildStatus(t *testing.T) {
	controllers, err := parseStorCLIStatus([]byte(storCLIStatusFixture))
	if err != nil {
		t.Fatal(err)
	}
	rebuild := `{
  "Controllers": [{
    "Response Data": {
      "Drive Rebuild Status": [{
        "Drive-ID": "/c0/e32/s0",
        "Progress%": "42.5%",
        "Status": "In progress",
        "Estimated Time Left": "3 Hours 12 Minutes"
      }]
    }
  }]
}`
	applyStorCLIRebuildStatus(controllers, []byte(rebuild))
	drive := controllers[0].PhysicalDrives[0]
	if drive.RebuildProgress == nil || math.Abs(*drive.RebuildProgress-42.5) > 0.001 || drive.RebuildStatus != "In progress" || drive.EstimatedTimeLeft != "3 Hours 12 Minutes" {
		t.Fatalf("unexpected rebuild status: %+v", drive)
	}
}

func TestParseStorCLISize(t *testing.T) {
	for input, expected := range map[string]uint64{
		"512B":     512,
		"1.000 KB": 1024,
		"1.000 TB": 1 << 40,
	} {
		actual, err := parseStorCLISize(input)
		if err != nil || actual != expected {
			t.Errorf("parseStorCLISize(%q) = %d, %v; want %d", input, actual, err, expected)
		}
	}
}
