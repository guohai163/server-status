package store

import (
	"testing"
	"time"
)

func TestNetworkUsageIncludesFirstDayOfLongMonth(t *testing.T) {
	monthStart := time.Date(2026, time.October, 1, 0, 0, 0, 0, networkUsageLocation)
	today := monthStart.AddDate(0, 0, 30)
	chartStart := today.AddDate(0, 0, 1-networkUsageDays)
	result := summarizeNetworkUsage(chartStart, monthStart, today, map[string]NetworkUsageBytes{
		"2026-09-30": {RXBytes: 999},
		"2026-10-01": {RXBytes: 300, TXBytes: 30},
		"2026-10-02": {},
		"2026-10-31": {RXBytes: 500, TXBytes: 50},
	})
	if len(result.Daily) != 30 || result.Daily[0].Date != "2026-10-02" || result.Daily[29].Date != "2026-10-31" {
		t.Fatalf("unexpected chart dates: %+v", result.Daily)
	}
	if result.MonthToDate == nil || *result.MonthToDate != (NetworkUsageBytes{RXBytes: 800, TXBytes: 80}) || result.MonthDaysSeen != 3 {
		t.Fatalf("unexpected monthly totals: %+v (%d days)", result.MonthToDate, result.MonthDaysSeen)
	}
	if result.Daily[0].Usage == nil || *result.Daily[0].Usage != (NetworkUsageBytes{}) || result.Daily[1].Usage != nil {
		t.Fatal("expected a zero-usage sample on October 2 and no sample on October 3")
	}
}
