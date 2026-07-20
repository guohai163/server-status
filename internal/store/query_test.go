package store

import (
	"testing"
	"time"
)

func TestNormalizeNodeSummaryMarksNodesWithoutMetricsPending(t *testing.T) {
	item := NodeSummary{Status: "online"}
	normalizeNodeSummary(&item)
	if item.Status != "pending" {
		t.Fatalf("expected pending status, got %q", item.Status)
	}
}

func TestNormalizeNodeSummaryPreservesReportedAndDisabledStatus(t *testing.T) {
	now := time.Now()
	reported := NodeSummary{Status: "online", LatestBucketAt: &now}
	normalizeNodeSummary(&reported)
	if reported.Status != "online" {
		t.Fatalf("expected online status, got %q", reported.Status)
	}

	disabled := NodeSummary{Status: "disabled"}
	normalizeNodeSummary(&disabled)
	if disabled.Status != "disabled" {
		t.Fatalf("expected disabled status, got %q", disabled.Status)
	}
}
