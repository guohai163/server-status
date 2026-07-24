package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guohai/server-status/internal/report"
)

type smartScan struct {
	Devices []smartDevice `json:"devices"`
}

type smartDevice struct {
	Name     string `json:"name"`
	InfoName string `json:"info_name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
}

type smartctlDocument struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
	} `json:"smartctl"`
	Device        smartDevice `json:"device"`
	ModelFamily   string      `json:"model_family"`
	ModelName     string      `json:"model_name"`
	SerialNumber  string      `json:"serial_number"`
	Firmware      string      `json:"firmware_version"`
	SCSIVendor    string      `json:"scsi_vendor"`
	SCSIProduct   string      `json:"scsi_product"`
	SCSIModelName string      `json:"scsi_model_name"`
	LogicalUnitID string      `json:"logical_unit_id"`
	RotationRate  *uint64     `json:"rotation_rate"`
	UserCapacity  struct {
		Bytes json.Number `json:"bytes"`
	} `json:"user_capacity"`
	SMARTSupport struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
	} `json:"smart_support"`
	SMARTStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature struct {
		Current   *float64 `json:"current"`
		DriveTrip *float64 `json:"drive_trip"`
	} `json:"temperature"`
	PowerOnTime struct {
		Hours json.Number `json:"hours"`
	} `json:"power_on_time"`
	ATAAttributes struct {
		Table []smartATAAttribute `json:"table"`
	} `json:"ata_smart_attributes"`
	ATAErrorLog struct {
		Summary struct {
			Count json.Number `json:"count"`
		} `json:"summary"`
	} `json:"ata_smart_error_log"`
	NVMeHealth struct {
		CriticalWarning json.Number `json:"critical_warning"`
		Temperature     *float64    `json:"temperature"`
		PercentageUsed  *float64    `json:"percentage_used"`
		MediaErrors     json.Number `json:"media_errors"`
		ErrorLogEntries json.Number `json:"num_err_log_entries"`
		PowerOnHours    json.Number `json:"power_on_hours"`
	} `json:"nvme_smart_health_information_log"`
	SCSIErrorLog struct {
		Read   smartSCSIErrorCounter `json:"read"`
		Write  smartSCSIErrorCounter `json:"write"`
		Verify smartSCSIErrorCounter `json:"verify"`
	} `json:"scsi_error_counter_log"`
}

type smartATAAttribute struct {
	ID         int         `json:"id"`
	Name       string      `json:"name"`
	Value      json.Number `json:"value"`
	Worst      json.Number `json:"worst"`
	Threshold  json.Number `json:"thresh"`
	WhenFailed string      `json:"when_failed"`
	Raw        struct {
		Value json.Number `json:"value"`
	} `json:"raw"`
}

type smartSCSIErrorCounter struct {
	TotalUncorrected json.Number `json:"total_uncorrected_errors"`
}

type smartSample struct {
	device smartDevice
	doc    smartctlDocument
	ok     bool
}

func collectStorageHealth(ctx context.Context, smartctlPath string, inventory []report.BlockDevice) ([]report.BlockDevice, []report.StorageHealth) {
	scanContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	output, _ := exec.CommandContext(scanContext, smartctlPath, "--scan-open", "--json").Output()
	cancel()
	devices := parseSMARTScan(output)
	if len(devices) == 0 {
		return inventory, nil
	}

	samples := make([]smartSample, len(devices))
	semaphore := make(chan struct{}, 4)
	var group sync.WaitGroup
	for index, device := range devices {
		group.Add(1)
		go func() {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			commandContext, commandCancel := context.WithTimeout(ctx, 12*time.Second)
			defer commandCancel()
			args := []string{"--all", "--json", "--nocheck=standby"}
			if device.Type != "" {
				args = append(args, "--device="+device.Type)
			}
			args = append(args, device.Name)
			deviceOutput, _ := exec.CommandContext(commandContext, smartctlPath, args...).Output()
			document, err := parseSMARTDocument(deviceOutput)
			if err == nil {
				samples[index] = smartSample{device: device, doc: document, ok: true}
			}
		}()
	}
	group.Wait()
	return mergeSMARTSamples(inventory, samples)
}

func parseSMARTScan(data []byte) []smartDevice {
	var scan smartScan
	if json.Unmarshal(data, &scan) != nil {
		return nil
	}
	result := make([]smartDevice, 0, len(scan.Devices))
	seen := make(map[string]struct{}, len(scan.Devices))
	for _, device := range scan.Devices {
		device.Name = strings.TrimSpace(device.Name)
		device.Type = strings.TrimSpace(device.Type)
		device.Protocol = strings.TrimSpace(device.Protocol)
		if !strings.HasPrefix(device.Name, "/dev/") || len(device.Name) > 4096 || len(device.Type) > 256 {
			continue
		}
		key := device.Name + "\x00" + device.Type
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, device)
		if len(result) >= 4096 {
			break
		}
	}
	return result
}

func parseSMARTDocument(data []byte) (smartctlDocument, error) {
	var document smartctlDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return smartctlDocument{}, err
	}
	if document.Device.Name == "" && document.SerialNumber == "" && document.ModelName == "" && document.SCSIModelName == "" {
		return smartctlDocument{}, errors.New("SMART document does not identify a device")
	}
	return document, nil
}

func mergeSMARTSamples(inventory []report.BlockDevice, samples []smartSample) ([]report.BlockDevice, []report.StorageHealth) {
	devices := append([]report.BlockDevice(nil), inventory...)
	health := make([]report.StorageHealth, 0, len(samples))
	seenHealth := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		if !sample.ok {
			continue
		}
		device := blockDeviceFromSMART(sample.device, sample.doc)
		index := matchBlockDevice(devices, device)
		if index >= 0 {
			mergeBlockDevice(&devices[index], device)
			device = devices[index]
		} else {
			devices = append(devices, device)
		}
		if _, exists := seenHealth[device.Key]; exists {
			continue
		}
		seenHealth[device.Key] = struct{}{}
		health = append(health, storageHealthFromSMART(device.Key, sample.doc))
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Key < devices[j].Key })
	sort.Slice(health, func(i, j int) bool { return health[i].BlockDeviceKey < health[j].BlockDeviceKey })
	return devices, health
}

func blockDeviceFromSMART(scanned smartDevice, document smartctlDocument) report.BlockDevice {
	deviceInfo := document.Device
	if deviceInfo.Name == "" {
		deviceInfo = scanned
	}
	model := firstNonEmpty(document.ModelName, document.SCSIModelName, strings.TrimSpace(document.SCSIVendor+" "+document.SCSIProduct))
	vendor := document.SCSIVendor
	deviceType := firstNonEmpty(deviceInfo.Type, scanned.Type)
	protocol := firstNonEmpty(deviceInfo.Protocol, scanned.Protocol)
	wwn := document.LogicalUnitID
	key := firstNonEmpty(document.SerialNumber, wwn, deviceInfo.Name+"|"+deviceType)
	device := report.BlockDevice{
		Key: key, DeviceName: firstNonEmpty(deviceInfo.Name, scanned.Name), DeviceKind: "disk",
		Vendor: vendor, ModelName: model, SerialNumber: document.SerialNumber, WWN: wwn,
		Protocol: protocol, SMARTDeviceType: deviceType, RAIDPassthrough: isRAIDSMARTType(deviceType),
	}
	if size := smartUint(document.UserCapacity.Bytes); size != nil {
		device.SizeBytes = *size
	}
	if document.RotationRate != nil {
		rotational := *document.RotationRate > 0
		device.Rotational = &rotational
	}
	return device
}

func matchBlockDevice(devices []report.BlockDevice, candidate report.BlockDevice) int {
	for index, device := range devices {
		if candidate.SerialNumber != "" && candidate.SerialNumber == device.SerialNumber ||
			candidate.WWN != "" && candidate.WWN == device.WWN ||
			candidate.DeviceName != "" && candidate.DeviceName == device.DeviceName && !candidate.RAIDPassthrough {
			return index
		}
	}
	return -1
}

func mergeBlockDevice(target *report.BlockDevice, source report.BlockDevice) {
	if source.Vendor != "" {
		target.Vendor = source.Vendor
	}
	if source.ModelName != "" {
		target.ModelName = source.ModelName
	}
	if source.SerialNumber != "" {
		target.SerialNumber = source.SerialNumber
	}
	if source.WWN != "" {
		target.WWN = source.WWN
	}
	if source.SizeBytes > 0 {
		target.SizeBytes = source.SizeBytes
	}
	if source.Rotational != nil {
		target.Rotational = source.Rotational
	}
	target.Protocol = source.Protocol
	target.SMARTDeviceType = source.SMARTDeviceType
	target.RAIDPassthrough = source.RAIDPassthrough
}

func isRAIDSMARTType(deviceType string) bool {
	deviceType = strings.ToLower(strings.TrimSpace(deviceType))
	for _, prefix := range []string{"megaraid,", "aacraid,", "areca,", "3ware,", "hpt,", "cciss,", "sssraid,"} {
		if strings.HasPrefix(deviceType, prefix) || strings.Contains(deviceType, "+"+prefix) {
			return true
		}
	}
	return false
}

func storageHealthFromSMART(blockDeviceKey string, document smartctlDocument) report.StorageHealth {
	metric := report.StorageHealth{
		BlockDeviceKey: blockDeviceKey,
		SMARTAvailable: document.SMARTSupport.Available,
		SMARTEnabled:   document.SMARTSupport.Enabled,
		SMARTStatus:    "unknown",
		RiskLevel:      "unknown",
	}
	if document.SMARTStatus != nil {
		if document.SMARTStatus.Passed {
			metric.SMARTStatus = "passed"
		} else {
			metric.SMARTStatus = "failed"
		}
	}
	metric.TemperatureCelsius = document.Temperature.Current
	metric.PowerOnHours = smartUint(document.PowerOnTime.Hours)
	metric.ErrorCount = smartUint(document.ATAErrorLog.Summary.Count)

	var nvmeCritical uint64
	if value := smartUint(document.NVMeHealth.CriticalWarning); value != nil {
		nvmeCritical = *value
	}
	if document.NVMeHealth.Temperature != nil {
		metric.TemperatureCelsius = document.NVMeHealth.Temperature
	}
	if value := smartUint(document.NVMeHealth.PowerOnHours); value != nil {
		metric.PowerOnHours = value
	}
	if value := smartUint(document.NVMeHealth.MediaErrors); value != nil {
		metric.ErrorCount = value
	}
	metric.PercentageUsed = document.NVMeHealth.PercentageUsed

	var scsiErrors uint64
	var hasSCSIErrors bool
	for _, counter := range []json.Number{
		document.SCSIErrorLog.Read.TotalUncorrected,
		document.SCSIErrorLog.Write.TotalUncorrected,
		document.SCSIErrorLog.Verify.TotalUncorrected,
	} {
		if value := smartUint(counter); value != nil {
			hasSCSIErrors = true
			if math.MaxUint64-scsiErrors >= *value {
				scsiErrors += *value
			}
		}
	}
	if hasSCSIErrors {
		metric.ErrorCount = uint64Pointer(scsiErrors)
	}

	for _, attribute := range document.ATAAttributes.Table {
		raw := smartUint(attribute.Raw.Value)
		switch attribute.ID {
		case 1:
			metric.ReadErrorRateNormalized = smartUint(attribute.Value)
			metric.ReadErrorRateRaw = raw
		case 5:
			metric.ReallocatedSectors = raw
		case 197:
			metric.PendingSectors = raw
		case 198:
			metric.UncorrectableSectors = raw
		}
	}

	critical := false
	warning := false
	addReason := func(condition bool, criticalReason bool, reason string) {
		if !condition {
			return
		}
		metric.RiskReasons = append(metric.RiskReasons, reason)
		if criticalReason {
			critical = true
		} else {
			warning = true
		}
	}
	addReason(metric.SMARTStatus == "failed" || document.Smartctl.ExitStatus&0x08 != 0, true, "smart_failed")
	addReason(document.Smartctl.ExitStatus&0x10 != 0, true, "prefail_attribute")
	addReason(nvmeCritical != 0, true, "nvme_critical_warning")
	addReason(nonZero(metric.PendingSectors), true, "pending_sectors")
	addReason(nonZero(metric.UncorrectableSectors), true, "uncorrectable_sectors")
	addReason(nonZero(metric.ReallocatedSectors), false, "reallocated_sectors")
	addReason(nonZero(metric.ErrorCount), false, "device_errors")
	addReason(document.Smartctl.ExitStatus&0x20 != 0, false, "age_attribute")
	addReason(document.Smartctl.ExitStatus&0x40 != 0, false, "device_error_log")
	addReason(document.Smartctl.ExitStatus&0x80 != 0, false, "self_test_errors")
	if metric.PercentageUsed != nil {
		addReason(*metric.PercentageUsed >= 100, true, "wear_exhausted")
		addReason(*metric.PercentageUsed >= 90 && *metric.PercentageUsed < 100, false, "wear_high")
	}
	sort.Strings(metric.RiskReasons)
	switch {
	case critical:
		metric.RiskLevel = "critical"
	case warning:
		metric.RiskLevel = "warning"
	case metric.SMARTStatus == "passed":
		metric.RiskLevel = "healthy"
	}
	return metric
}

func smartUint(value json.Number) *uint64 {
	text := strings.TrimSpace(string(value))
	if text == "" {
		return nil
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil || parsed > math.MaxInt64 {
		return nil
	}
	return &parsed
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}

func nonZero(value *uint64) bool {
	return value != nil && *value > 0
}

func collectHWMONTemperatures(root string) []report.TemperatureMetrics {
	chips, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	result := make([]report.TemperatureMetrics, 0)
	for _, chip := range chips {
		base := filepath.Join(root, chip.Name())
		chipName := readTrimmed(filepath.Join(base, "name"))
		inputs, _ := filepath.Glob(filepath.Join(base, "temp*_input"))
		sort.Strings(inputs)
		for _, input := range inputs {
			stem := strings.TrimSuffix(filepath.Base(input), "_input")
			temperature, ok := readMillidegrees(input)
			if !ok {
				continue
			}
			label := readTrimmed(filepath.Join(base, stem+"_label"))
			if label == "" {
				label = strings.TrimSpace(chipName + " " + strings.TrimPrefix(stem, "temp"))
			}
			metric := report.TemperatureMetrics{
				Key: chip.Name() + ":" + stem, Component: classifyTemperatureComponent(chipName, label),
				Label: label, TemperatureCelsius: temperature,
			}
			if value, valid := readMillidegrees(filepath.Join(base, stem+"_max")); valid {
				metric.HighCelsius = &value
			}
			if value, valid := readMillidegrees(filepath.Join(base, stem+"_crit")); valid {
				metric.CriticalCelsius = &value
			}
			result = append(result, metric)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func readMillidegrees(path string) (float64, bool) {
	value, err := strconv.ParseFloat(readTrimmed(path), 64)
	if err != nil {
		return 0, false
	}
	value /= 1000
	if math.IsNaN(value) || math.IsInf(value, 0) || value < -273.15 || value > 1000 {
		return 0, false
	}
	return value, true
}

func classifyTemperatureComponent(chipName, label string) string {
	value := strings.ToLower(chipName + " " + label)
	for _, token := range []string{"coretemp", "k10temp", "zenpower", "cpu_thermal", "package id", "tctl", "tdie", "cpu temp", "cpu temperature"} {
		if strings.Contains(value, token) {
			return "cpu"
		}
	}
	for _, token := range []string{"amdgpu", "radeon", "nouveau", "gpu"} {
		if strings.Contains(value, token) {
			return "gpu"
		}
	}
	for _, token := range []string{"nvme", "drivetemp", "hdd", "ssd"} {
		if strings.Contains(value, token) {
			return "storage"
		}
	}
	for _, token := range []string{"acpitz", "nct", "it87", "pch", "system", "motherboard", "mainboard", "systin"} {
		if strings.Contains(value, token) {
			return "motherboard"
		}
	}
	return "other"
}
