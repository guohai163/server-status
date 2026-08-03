package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/guohai/server-status/internal/report"
)

func TestParseIPMISensors(t *testing.T) {
	metrics := parseIPMISensors([]byte(`
FAN 1 RPM             | 4200.000 | RPM       | ok | 0.000 | 800.000 | 1200.000 | na | na | na
System Board Power    | 84.000   | Watts     | nc | na | na | na | 750.000 | 900.000 | 1000.000
Power Supply Voltage  | 230.000  | Volts     | cr | 180.000 | 190.000 | 200.000 | 240.000 | 250.000 | 260.000
Power Supply Current  | 0.800    | Amps      | ok | na | na | na | na | na | na
Inlet Temp            | 21.000   | degrees C | ok | na | na | na | na | na | na
Missing Fan           | na       | RPM       | ns | na | na | na | na | na | na
`))
	if len(metrics) != 4 {
		t.Fatalf("expected four IPMI sensors, got %+v", metrics)
	}
	byType := make(map[string]report.HardwareSensorMetric, len(metrics))
	for _, metric := range metrics {
		byType[metric.SensorType] = metric
	}
	fan := byType["fan"]
	if fan.Value != 4200 || fan.MinValue == nil || *fan.MinValue != 1200 || fan.Alarm || fan.Fault {
		t.Fatalf("unexpected IPMI fan metric: %+v", fan)
	}
	power := byType["power"]
	if power.Value != 84 || power.MaxValue == nil || *power.MaxValue != 750 || power.CriticalValue == nil || *power.CriticalValue != 900 || !power.Alarm || power.Fault {
		t.Fatalf("unexpected IPMI power metric: %+v", power)
	}
	voltage := byType["voltage"]
	if voltage.MinValue == nil || *voltage.MinValue != 200 || voltage.MaxValue == nil || *voltage.MaxValue != 240 || !voltage.Fault {
		t.Fatalf("unexpected IPMI voltage metric: %+v", voltage)
	}
	if current := byType["current"]; current.Value != 0.8 || current.Alarm || current.Fault {
		t.Fatalf("unexpected IPMI current metric: %+v", current)
	}
}

func TestCollectIPMISensorsUsesLocalOpenInterface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ipmitool")
	script := `#!/bin/sh
[ "$1" = "-I" ] && [ "$2" = "open" ] && [ "$3" = "sensor" ] && [ "$4" = "list" ] || exit 2
printf '%s\n' 'FAN 2 RPM | 3600.000 | RPM | ok | na | na | 1000.000 | na | na | na'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	metrics := collectIPMISensors(context.Background(), path)
	if len(metrics) != 1 || metrics[0].SensorType != "fan" || metrics[0].Value != 3600 {
		t.Fatalf("unexpected local IPMI metrics: %+v", metrics)
	}
}

func TestPreferIPMIHardwareSensorsByType(t *testing.T) {
	ipmi := []report.HardwareSensorMetric{
		{Key: "ipmi:fan:fan-1", SensorType: "fan", Label: "IPMI Fan", Value: 4200},
		{Key: "ipmi:power:system", SensorType: "power", Label: "IPMI Power", Value: 84},
	}
	hwmon := []report.HardwareSensorMetric{
		{Key: "hwmon0:fan1", SensorType: "fan", Label: "hwmon Fan", Value: 4000},
		{Key: "hwmon0:in0", SensorType: "voltage", Label: "hwmon Voltage", Value: 12.1},
	}
	metrics := preferIPMIHardwareSensors(ipmi, hwmon)
	if len(metrics) != 3 {
		t.Fatalf("expected IPMI sensors plus missing hwmon type, got %+v", metrics)
	}
	for _, metric := range metrics {
		if metric.Key == "hwmon0:fan1" {
			t.Fatalf("hwmon fan should not override IPMI fan: %+v", metrics)
		}
	}
}

func TestPreferIPMIHardwareSensorsFallsBackToHWMON(t *testing.T) {
	hwmon := []report.HardwareSensorMetric{{Key: "hwmon0:fan1", SensorType: "fan", Label: "Fan", Value: 4000}}
	metrics := preferIPMIHardwareSensors(nil, hwmon)
	if len(metrics) != 1 || metrics[0].Key != hwmon[0].Key {
		t.Fatalf("unexpected hwmon fallback: %+v", metrics)
	}
}
