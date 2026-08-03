package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guohai/server-status/internal/report"
)

func collectPhysicalHardwareSensors(ctx context.Context, hwmonRoot string) []report.HardwareSensorMetric {
	ipmiSensors := []report.HardwareSensorMetric(nil)
	if ipmitoolPath := findIPMITool(); ipmitoolPath != "" {
		ipmiSensors = collectIPMISensors(ctx, ipmitoolPath)
	}
	return preferIPMIHardwareSensors(ipmiSensors, collectHWMONSensors(hwmonRoot))
}

func findIPMITool() string {
	if path, err := exec.LookPath("ipmitool"); err == nil {
		return path
	}
	for _, path := range []string{"/usr/bin/ipmitool", "/usr/sbin/ipmitool", "/usr/local/bin/ipmitool"} {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return filepath.Clean(path)
		}
	}
	return ""
}

func collectIPMISensors(ctx context.Context, ipmitoolPath string) []report.HardwareSensorMetric {
	commandContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, ipmitoolPath, "-I", "open", "sensor", "list")
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.Output()
	if err != nil {
		return nil
	}
	return parseIPMISensors(output)
}

func parseIPMISensors(data []byte) []report.HardwareSensorMetric {
	result := make([]report.HardwareSensorMetric, 0)
	keyOccurrences := make(map[string]int)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "|")
		if len(fields) < 4 {
			continue
		}
		for index := range fields {
			fields[index] = strings.TrimSpace(fields[index])
		}
		label := fields[0]
		sensorType := ipmiSensorType(fields[2])
		value, err := strconv.ParseFloat(fields[1], 64)
		if label == "" || sensorType == "" || err != nil || !report.ValidHardwareSensorValue(sensorType, value) {
			continue
		}

		keyBase := "ipmi:" + sensorType + ":" + strings.ToLower(strings.Join(strings.Fields(label), " "))
		keyOccurrences[keyBase]++
		key := keyBase
		if keyOccurrences[keyBase] > 1 {
			key = fmt.Sprintf("%s#%d", keyBase, keyOccurrences[keyBase])
		}
		alarm, fault := ipmiSensorFlags(fields[3])
		metric := report.HardwareSensorMetric{
			Key: key, SensorType: sensorType, Label: label, Value: value, Alarm: alarm, Fault: fault,
		}
		metric.MinValue = firstIPMIThreshold(fields, sensorType, 6, 5, 4)
		metric.MaxValue = firstIPMIThreshold(fields, sensorType, 7)
		metric.CriticalValue = firstIPMIThreshold(fields, sensorType, 8, 9)
		result = append(result, metric)
		if len(result) >= 4096 {
			break
		}
	}
	sortHardwareSensors(result)
	return result
}

func ipmiSensorType(unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "rpm":
		return "fan"
	case "v", "volt", "volts":
		return "voltage"
	case "a", "amp", "amps", "ampere", "amperes":
		return "current"
	case "w", "watt", "watts":
		return "power"
	default:
		return ""
	}
}

func firstIPMIThreshold(fields []string, sensorType string, indexes ...int) *float64 {
	for _, index := range indexes {
		if index >= len(fields) {
			continue
		}
		value, err := strconv.ParseFloat(fields[index], 64)
		if err == nil && report.ValidHardwareSensorValue(sensorType, value) {
			return &value
		}
	}
	return nil
}

func ipmiSensorFlags(status string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "ok", "na", "ns":
		return false, false
	case "cr", "nr", "lcr", "lnr", "ucr", "unr", "critical", "non-recoverable":
		return false, true
	default:
		return true, false
	}
}

func preferIPMIHardwareSensors(ipmiSensors, hwmonSensors []report.HardwareSensorMetric) []report.HardwareSensorMetric {
	if len(ipmiSensors) == 0 {
		return hwmonSensors
	}
	ipmiTypes := make(map[string]struct{}, len(ipmiSensors))
	result := append([]report.HardwareSensorMetric(nil), ipmiSensors...)
	for _, sensor := range ipmiSensors {
		ipmiTypes[sensor.SensorType] = struct{}{}
	}
	for _, sensor := range hwmonSensors {
		if _, exists := ipmiTypes[sensor.SensorType]; !exists {
			result = append(result, sensor)
		}
	}
	sortHardwareSensors(result)
	return result
}

func sortHardwareSensors(sensors []report.HardwareSensorMetric) {
	sort.Slice(sensors, func(i, j int) bool {
		if sensors[i].SensorType != sensors[j].SensorType {
			return sensors[i].SensorType < sensors[j].SensorType
		}
		return sensors[i].Key < sensors[j].Key
	})
}
