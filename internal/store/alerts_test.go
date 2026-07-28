package store

import (
	"strings"
	"testing"
	"time"
)

func TestHomepageSummaryQueryUsesNodeStatusAndOpenAlerts(t *testing.T) {
	for _, fragment := range []string{
		"monitoring.v_node_status",
		"monitoring.alert_events",
		"alert.resolved_at IS NULL",
		"alert.category <> 'offline'",
		"latest_bucket_at IS NOT NULL",
		"status = 'online' AND NOT alerting",
		"status = 'offline'",
		"status = 'online' AND alerting",
	} {
		if !strings.Contains(homepageSummarySQL, fragment) {
			t.Errorf("Homepage summary query does not contain %q", fragment)
		}
	}
}

func TestThresholdAlertSeverityBoundaries(t *testing.T) {
	tests := []struct {
		name                         string
		value, warning, critical, up float64
		open                         bool
		want                         string
	}{
		{"below warning", 89.9, 90, 95, 88, false, ""},
		{"warning boundary", 90, 90, 95, 88, false, AlertSeverityWarning},
		{"critical boundary", 95, 90, 95, 88, false, AlertSeverityCritical},
		{"open at recovery boundary", 88, 90, 95, 88, true, AlertSeverityWarning},
		{"open below recovery", 87.9, 90, 95, 88, true, ""},
		{"load warning", 1, 1, 1.5, .8, false, AlertSeverityWarning},
		{"load critical", 1.5, 1, 1.5, .8, false, AlertSeverityCritical},
		{"load remains open", .8, 1, 1.5, .8, true, AlertSeverityWarning},
		{"load recovers", .79, 1, 1.5, .8, true, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := thresholdAlertSeverity(test.value, test.warning, test.critical, test.up, test.open); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestTemperatureAlertSeverityBoundaries(t *testing.T) {
	high, critical := 90.0, 100.0
	tests := []struct {
		name  string
		value float64
		open  bool
		want  string
	}{
		{"healthy", 89.9, false, ""},
		{"warning boundary", 90, false, AlertSeverityWarning},
		{"critical boundary", 100, false, AlertSeverityCritical},
		{"recovery boundary remains open", 87, true, AlertSeverityWarning},
		{"below recovery", 86.9, true, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := temperatureAlertSeverity(test.value, &high, &critical, test.open); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
	if got := temperatureAlertSeverity(97, nil, &critical, true); got != AlertSeverityWarning {
		t.Fatalf("critical-only sensor at recovery boundary got %q", got)
	}
	if got := temperatureAlertSeverity(96.9, nil, &critical, true); got != "" {
		t.Fatalf("critical-only sensor below recovery boundary got %q", got)
	}
}

func TestRAIDAlertSeverity(t *testing.T) {
	tests := map[string]string{
		"Optimal": "", "Onln": "", "Online": "", "JBOD": "", "UGood": "",
		"Dgrd": AlertSeverityWarning, "Pdgd": AlertSeverityWarning, "Rbld": AlertSeverityWarning,
		"Rebuild": AlertSeverityWarning, "Needs Attention": AlertSeverityWarning,
		"Offln": AlertSeverityCritical, "OfLn": AlertSeverityCritical, "Failed": AlertSeverityCritical,
		"Msng": AlertSeverityCritical, "UBad": AlertSeverityCritical, "DHS": AlertSeverityCritical,
	}
	for input, want := range tests {
		if got := raidAlertSeverity(input); got != want {
			t.Errorf("state %q: got %q, want %q", input, got, want)
		}
	}
}

func TestAlertResolutionPolicy(t *testing.T) {
	tests := []struct {
		name             string
		active, exists   bool
		status, category string
		want             bool
	}{
		{"active alert stays open", true, true, "online", AlertCategoryLoad, false},
		{"online recovery closes", false, true, "online", AlertCategoryLoad, true},
		{"offline resource freezes", false, true, "offline", AlertCategorySMART, false},
		{"offline event closes on recovery", false, true, "online", AlertCategoryOffline, true},
		{"disabled node closes", false, true, "disabled", AlertCategoryFilesystem, true},
		{"pending node closes", false, true, "pending", AlertCategoryRAID, true},
		{"deleted node closes", false, false, "", AlertCategoryTemperature, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldResolveAlert(test.active, test.exists, test.status, test.category); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestAlertCursorRoundTripAndValidation(t *testing.T) {
	want := alertCursor{OpenedAt: time.Date(2026, 7, 27, 4, 5, 6, 0, time.UTC), AlertID: "10000000-0000-4000-8000-000000000001"}
	got, err := decodeAlertCursor(encodeAlertCursor(want))
	if err != nil {
		t.Fatal(err)
	}
	if !got.OpenedAt.Equal(want.OpenedAt) || got.AlertID != want.AlertID {
		t.Fatalf("cursor round trip got %#v, want %#v", got, want)
	}
	for _, value := range []string{"not-base64", "e30"} {
		if _, err := decodeAlertCursor(value); err == nil {
			t.Errorf("cursor %q unexpectedly passed validation", value)
		}
	}
}
