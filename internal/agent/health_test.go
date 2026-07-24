package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSMARTScanIncludesRAIDPassthroughDevices(t *testing.T) {
	devices := parseSMARTScan([]byte(`{
  "devices": [
    {"name":"/dev/sda","type":"sat","protocol":"ATA"},
    {"name":"/dev/bus/0","type":"megaraid,0","protocol":"SCSI"},
    {"name":"/dev/bus/0","type":"megaraid,1","protocol":"SCSI"}
  ]
}`))
	if len(devices) != 3 || devices[1].Type != "megaraid,0" || devices[2].Type != "megaraid,1" {
		t.Fatalf("unexpected SMART scan: %+v", devices)
	}
}

func TestSMARTATAHealthRiskAndCounters(t *testing.T) {
	document, err := parseSMARTDocument([]byte(`{
  "smartctl":{"exit_status":64},
  "device":{"name":"/dev/sda","type":"sat","protocol":"ATA"},
  "model_name":"Example SSD","serial_number":"ATA-1",
  "smart_support":{"available":true,"enabled":true},
  "smart_status":{"passed":true},
  "temperature":{"current":41},
  "power_on_time":{"hours":12345},
  "ata_smart_error_log":{"summary":{"count":2}},
  "ata_smart_attributes":{"table":[
    {"id":1,"name":"Raw_Read_Error_Rate","value":100,"raw":{"value":7}},
    {"id":5,"name":"Reallocated_Sector_Ct","value":99,"raw":{"value":3}},
    {"id":197,"name":"Current_Pending_Sector","value":100,"raw":{"value":0}},
    {"id":198,"name":"Offline_Uncorrectable","value":100,"raw":{"value":0}}
  ]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	metric := storageHealthFromSMART("ATA-1", document)
	if metric.RiskLevel != "warning" || metric.PowerOnHours == nil || *metric.PowerOnHours != 12345 {
		t.Fatalf("unexpected ATA health: %+v", metric)
	}
	if metric.ReadErrorRateNormalized == nil || *metric.ReadErrorRateNormalized != 100 || metric.ReadErrorRateRaw == nil || *metric.ReadErrorRateRaw != 7 {
		t.Fatalf("unexpected ATA read error rate: %+v", metric)
	}
	if metric.ReallocatedSectors == nil || *metric.ReallocatedSectors != 3 || metric.ErrorCount == nil || *metric.ErrorCount != 2 {
		t.Fatalf("unexpected ATA counters: %+v", metric)
	}
}

func TestSMARTNVMeCriticalHealth(t *testing.T) {
	document, err := parseSMARTDocument([]byte(`{
  "smartctl":{"exit_status":0},
  "device":{"name":"/dev/nvme0","type":"nvme","protocol":"NVMe"},
  "model_name":"Example NVMe","serial_number":"NVME-1",
  "smart_support":{"available":true,"enabled":true},
  "smart_status":{"passed":false},
  "nvme_smart_health_information_log":{
    "critical_warning":1,"temperature":57,"percentage_used":101,
    "media_errors":4,"num_err_log_entries":8,"power_on_hours":2200
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	metric := storageHealthFromSMART("NVME-1", document)
	if metric.RiskLevel != "critical" || metric.SMARTStatus != "failed" {
		t.Fatalf("unexpected NVMe risk: %+v", metric)
	}
	if metric.ErrorCount == nil || *metric.ErrorCount != 4 || metric.PercentageUsed == nil || *metric.PercentageUsed != 101 {
		t.Fatalf("unexpected NVMe counters: %+v", metric)
	}
}

func TestMergeSMARTAddsRAIDMember(t *testing.T) {
	document, err := parseSMARTDocument([]byte(`{
  "smartctl":{"exit_status":0},
  "device":{"name":"/dev/bus/0","type":"megaraid,4","protocol":"SCSI"},
  "scsi_vendor":"SEAGATE","scsi_product":"ST1200MM0009","serial_number":"RAID-DISK-4",
  "user_capacity":{"bytes":1200243695616},
  "smart_support":{"available":true,"enabled":true},"smart_status":{"passed":true}
}`))
	if err != nil {
		t.Fatal(err)
	}
	devices, health := mergeSMARTSamples(nil, []smartSample{{device: document.Device, doc: document, ok: true}})
	if len(devices) != 1 || len(health) != 1 || !devices[0].RAIDPassthrough || devices[0].SMARTDeviceType != "megaraid,4" {
		t.Fatalf("unexpected RAID SMART merge: devices=%+v health=%+v", devices, health)
	}
}

func TestCollectHWMONTemperatures(t *testing.T) {
	root := t.TempDir()
	chip := filepath.Join(root, "hwmon0")
	if err := os.Mkdir(chip, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"name":        "coretemp\n",
		"temp1_label": "Package id 0\n",
		"temp1_input": "53500\n",
		"temp1_max":   "90000\n",
		"temp1_crit":  "105000\n",
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(chip, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	metrics := collectHWMONTemperatures(root)
	if len(metrics) != 1 || metrics[0].Component != "cpu" || metrics[0].TemperatureCelsius != 53.5 {
		t.Fatalf("unexpected hwmon metrics: %+v", metrics)
	}
	if metrics[0].HighCelsius == nil || *metrics[0].HighCelsius != 90 || metrics[0].CriticalCelsius == nil || *metrics[0].CriticalCelsius != 105 {
		t.Fatalf("unexpected hwmon thresholds: %+v", metrics[0])
	}
}
