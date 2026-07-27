package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guohai/server-status/internal/report"
)

var storCLIRebuildDrivePattern = regexp.MustCompile(`^/c([0-9]+)(?:/e([0-9]+))?/s([0-9]+)$`)

func collectRAIDStatus(ctx context.Context) []report.RAIDControllerStatus {
	if runtime.GOOS != "linux" {
		return nil
	}
	cliPath := findRAIDCLI()
	if cliPath == "" {
		return nil
	}
	commandContext, cancel := context.WithTimeout(ctx, 12*time.Second)
	output, err := exec.CommandContext(commandContext, cliPath, "/call", "show", "all", "J").Output()
	cancel()
	if err != nil {
		return nil
	}
	controllers, err := parseStorCLIStatus(output)
	if err != nil || len(controllers) == 0 || !hasRebuildingDrive(controllers) {
		return controllers
	}

	rebuildContext, cancelRebuild := context.WithTimeout(ctx, 8*time.Second)
	rebuildOutput, rebuildErr := exec.CommandContext(rebuildContext, cliPath, "/call/eall/sall", "show", "rebuild", "J").Output()
	cancelRebuild()
	if rebuildErr == nil {
		applyStorCLIRebuildStatus(controllers, rebuildOutput)
	}
	return controllers
}

func findRAIDCLI() string {
	for _, name := range []string{"storcli64", "storcli", "perccli64", "perccli"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	for _, path := range []string{
		"/usr/local/bin/storcli64",
		"/opt/MegaRAID/storcli/storcli64",
		"/opt/MegaRAID/perccli/perccli64",
	} {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return filepath.Clean(path)
		}
	}
	return ""
}

func parseStorCLIStatus(data []byte) ([]report.RAIDControllerStatus, error) {
	root, err := decodeStorCLIJSON(data)
	if err != nil {
		return nil, err
	}
	entries, ok := root["Controllers"].([]any)
	if !ok {
		return nil, fmt.Errorf("storcli response does not contain controllers")
	}
	controllers := make([]report.RAIDControllerStatus, 0, len(entries))
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		commandStatus := objectValue(object, "Command Status")
		responseData := objectValue(object, "Response Data")
		controllerID, ok := intValue(commandStatus["Controller"])
		if !ok {
			controllerID, ok = findIntField(responseData, "Controller")
		}
		if !ok || controllerID < 0 {
			continue
		}
		controller := report.RAIDControllerStatus{ControllerID: controllerID}
		basics := objectValue(responseData, "Basics")
		controller.Model = firstText(basics["Model"], responseData["Product Name"])
		if controller.Model == "" {
			controller.Model = "RAID Controller"
		}
		status := objectValue(responseData, "Status")
		controller.State = firstText(status["Controller Status"], status["Status"])
		controller.VirtualDrives = parseStorCLIVirtualDrives(responseData)
		controller.PhysicalDrives = parseStorCLIPhysicalDrives(responseData)
		controller.PhysicalDriveCount = countValue(responseData["Physical Drives"], len(controller.PhysicalDrives))
		controller.DriveGroupCount = countValue(responseData["Drive Groups"], distinctDriveGroupCount(controller))
		controller.VirtualDriveCount = countValue(responseData["Virtual Drives"], len(controller.VirtualDrives))
		if controller.State == "" {
			controller.State = inferredRAIDControllerState(controller.VirtualDrives)
		}
		controllers = append(controllers, controller)
	}
	sort.Slice(controllers, func(i, j int) bool { return controllers[i].ControllerID < controllers[j].ControllerID })
	return controllers, nil
}

func parseStorCLIVirtualDrives(responseData map[string]any) []report.RAIDVirtualDriveStatus {
	rows := arrayValue(responseData, "VD LIST")
	result := make([]report.RAIDVirtualDriveStatus, 0, len(rows))
	for _, row := range rows {
		identifier := textValue(row["DG/VD"])
		parts := strings.Split(identifier, "/")
		if len(parts) != 2 {
			continue
		}
		driveGroupID, groupErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		virtualDriveID, driveErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		sizeBytes, sizeErr := parseStorCLISize(textValue(row["Size"]))
		if groupErr != nil || driveErr != nil || sizeErr != nil {
			continue
		}
		result = append(result, report.RAIDVirtualDriveStatus{
			DriveGroupID: driveGroupID, VirtualDriveID: virtualDriveID,
			RAIDLevel: textValue(row["TYPE"]), State: textValue(row["State"]),
			Access: textValue(row["Access"]), Consistency: textValue(row["Consist"]),
			Cache: textValue(row["Cache"]), SizeBytes: sizeBytes, Name: textValue(row["Name"]),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DriveGroupID == result[j].DriveGroupID {
			return result[i].VirtualDriveID < result[j].VirtualDriveID
		}
		return result[i].DriveGroupID < result[j].DriveGroupID
	})
	return result
}

func parseStorCLIPhysicalDrives(responseData map[string]any) []report.RAIDPhysicalDriveStatus {
	rows := arrayValue(responseData, "PD LIST")
	result := make([]report.RAIDPhysicalDriveStatus, 0, len(rows))
	for _, row := range rows {
		enclosureID, slot, ok := parseStorCLISlot(textValue(row["EID:Slt"]))
		deviceID, deviceOK := intValue(row["DID"])
		sizeBytes, sizeErr := parseStorCLISize(textValue(row["Size"]))
		if !ok || !deviceOK || sizeErr != nil {
			continue
		}
		var driveGroupID *int
		if group, groupOK := intValue(row["DG"]); groupOK && group >= 0 {
			driveGroupID = &group
		}
		sectorSize, _ := parseStorCLISize(textValue(row["SeSz"]))
		result = append(result, report.RAIDPhysicalDriveStatus{
			EnclosureID: enclosureID, Slot: slot, DeviceID: deviceID,
			State: textValue(row["State"]), DriveGroupID: driveGroupID,
			SizeBytes: sizeBytes, Interface: textValue(row["Intf"]), MediaType: textValue(row["Med"]),
			SectorSizeBytes: sectorSize, Model: textValue(row["Model"]), Spun: textValue(row["Sp"]), DriveType: textValue(row["Type"]),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := -1, -1
		if result[i].EnclosureID != nil {
			left = *result[i].EnclosureID
		}
		if result[j].EnclosureID != nil {
			right = *result[j].EnclosureID
		}
		if left == right {
			return result[i].Slot < result[j].Slot
		}
		return left < right
	})
	return result
}

func applyStorCLIRebuildStatus(controllers []report.RAIDControllerStatus, data []byte) {
	root, err := decodeStorCLIJSON(data)
	if err != nil {
		return
	}
	records := make([]map[string]any, 0)
	collectStorCLIRebuildRecords(root, &records)
	for _, record := range records {
		matches := storCLIRebuildDrivePattern.FindStringSubmatch(textValue(record["Drive-ID"]))
		if matches == nil {
			continue
		}
		controllerID, _ := strconv.Atoi(matches[1])
		slot, _ := strconv.Atoi(matches[3])
		var enclosureID *int
		if matches[2] != "" {
			value, _ := strconv.Atoi(matches[2])
			enclosureID = &value
		}
		for controllerIndex := range controllers {
			if controllers[controllerIndex].ControllerID != controllerID {
				continue
			}
			for driveIndex := range controllers[controllerIndex].PhysicalDrives {
				drive := &controllers[controllerIndex].PhysicalDrives[driveIndex]
				if drive.Slot != slot || enclosureID != nil && (drive.EnclosureID == nil || *drive.EnclosureID != *enclosureID) {
					continue
				}
				drive.RebuildStatus = textValue(record["Status"])
				drive.EstimatedTimeLeft = textValue(record["Estimated Time Left"])
				if progress, ok := percentValue(record["Progress%"]); ok {
					drive.RebuildProgress = &progress
				}
			}
		}
	}
}

func collectStorCLIRebuildRecords(value any, records *[]map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		if _, exists := typed["Drive-ID"]; exists {
			*records = append(*records, typed)
		}
		for key, child := range typed {
			if storCLIRebuildDrivePattern.MatchString(key) {
				if object, ok := child.(map[string]any); ok {
					copy := make(map[string]any, len(object)+1)
					for field, fieldValue := range object {
						copy[field] = fieldValue
					}
					copy["Drive-ID"] = key
					*records = append(*records, copy)
				}
			}
			collectStorCLIRebuildRecords(child, records)
		}
	case []any:
		for _, child := range typed {
			collectStorCLIRebuildRecords(child, records)
		}
	}
}

func decodeStorCLIJSON(data []byte) (map[string]any, error) {
	start := bytes.IndexByte(data, '{')
	if start < 0 {
		return nil, fmt.Errorf("storcli response is not JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data[start:]))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode storcli response: %w", err)
	}
	return root, nil
}

func objectValue(object map[string]any, key string) map[string]any {
	value, _ := object[key].(map[string]any)
	return value
}

func arrayValue(object map[string]any, key string) []map[string]any {
	values, _ := object[key].([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if row, ok := value.(map[string]any); ok {
			result = append(result, row)
		}
	}
	return result
}

func textValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func firstText(values ...any) string {
	for _, value := range values {
		if result := textValue(value); result != "" && result != "-" {
			return result
		}
	}
	return ""
}

func intValue(value any) (int, bool) {
	text := textValue(value)
	if text == "" || text == "-" {
		return 0, false
	}
	result, err := strconv.Atoi(text)
	return result, err == nil
}

func findIntField(value any, key string) (int, bool) {
	switch typed := value.(type) {
	case map[string]any:
		if result, ok := intValue(typed[key]); ok {
			return result, true
		}
		for _, child := range typed {
			if result, ok := findIntField(child, key); ok {
				return result, true
			}
		}
	case []any:
		for _, child := range typed {
			if result, ok := findIntField(child, key); ok {
				return result, true
			}
		}
	}
	return 0, false
}

func parseStorCLISlot(value string) (*int, int, bool) {
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		slot, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		return nil, slot, err == nil && slot >= 0
	}
	if len(parts) != 2 {
		return nil, 0, false
	}
	enclosure, enclosureErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	slot, slotErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if enclosureErr != nil || slotErr != nil || enclosure < 0 || slot < 0 {
		return nil, 0, false
	}
	return &enclosure, slot, true
}

func parseStorCLISize(value string) (uint64, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "" || normalized == "-" {
		return 0, fmt.Errorf("missing size")
	}
	fields := strings.Fields(normalized)
	if len(fields) == 1 {
		for index, character := range fields[0] {
			if character < '0' && character != '.' || character > '9' {
				fields = []string{fields[0][:index], fields[0][index:]}
				break
			}
		}
	}
	if len(fields) != 2 {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	amount, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || amount < 0 {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	multipliers := map[string]float64{
		"B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30,
		"TB": 1 << 40, "PB": 1 << 50,
	}
	multiplier, ok := multipliers[fields[1]]
	if !ok || amount > float64(math.MaxUint64)/multiplier {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	return uint64(math.Round(amount * multiplier)), nil
}

func percentValue(value any) (float64, bool) {
	text := strings.TrimSpace(strings.TrimSuffix(textValue(value), "%"))
	result, err := strconv.ParseFloat(text, 64)
	return result, err == nil && result >= 0 && result <= 100
}

func countValue(value any, fallback int) int {
	if result, ok := intValue(value); ok && result >= 0 {
		return result
	}
	return fallback
}

func distinctDriveGroupCount(controller report.RAIDControllerStatus) int {
	groups := make(map[int]struct{})
	for _, drive := range controller.VirtualDrives {
		groups[drive.DriveGroupID] = struct{}{}
	}
	for _, drive := range controller.PhysicalDrives {
		if drive.DriveGroupID != nil {
			groups[*drive.DriveGroupID] = struct{}{}
		}
	}
	return len(groups)
}

func inferredRAIDControllerState(drives []report.RAIDVirtualDriveStatus) string {
	for _, drive := range drives {
		if !strings.EqualFold(drive.State, "Optl") && !strings.EqualFold(drive.State, "Optimal") {
			return "Needs Attention"
		}
	}
	return "Optimal"
}

func hasRebuildingDrive(controllers []report.RAIDControllerStatus) bool {
	for _, controller := range controllers {
		for _, drive := range controller.PhysicalDrives {
			if strings.EqualFold(drive.State, "Rbld") || strings.Contains(strings.ToLower(drive.State), "rebuild") {
				return true
			}
		}
	}
	return false
}
