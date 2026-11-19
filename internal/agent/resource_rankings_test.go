package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTaskRankingsUseThreadDeltasAndProcessRSS(t *testing.T) {
	procRoot := t.TempDir()
	previous := make(map[taskKey]taskSample)
	for tid := 100; tid < 112; tid++ {
		path := filepath.Join(procRoot, "100", "task", strconv.Itoa(tid))
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		stat := fmt.Sprintf("%d (worker ) x) S 0 0 0 0 0 0 0 0 0 0 %d 0 0 0 0 0 0 0 10 0 5\n", tid, tid)
		if err := os.WriteFile(filepath.Join(path, "stat"), []byte(stat), 0600); err != nil {
			t.Fatal(err)
		}
		io := fmt.Sprintf("rchar: 5000\nread_bytes: %d\nwrite_bytes: 1000\n", tid*100)
		if err := os.WriteFile(filepath.Join(path, "io"), []byte(io), 0600); err != nil {
			t.Fatal(err)
		}
		previous[taskKey{100, tid}] = taskSample{startTicks: 10, cpuTicks: 100, hasIO: true, at: time.Now().Add(-10 * time.Second)}
	}
	// A recycled TID must start with a fresh baseline.
	previous[taskKey{100, 111}] = taskSample{startTicks: 9, hasIO: true, at: time.Now().Add(-10 * time.Second)}

	first, _ := scanTaskRankings(t.Context(), procRoot, 100, nil)
	if first.CPU != nil || first.DiskIO != nil {
		t.Fatalf("first sample has rate data: %+v", first)
	}
	ranks, current := scanTaskRankings(t.Context(), procRoot, 100, previous)
	if len(ranks.CPU) != 10 || ranks.CPU[0].TID != 110 || ranks.CPU[0].Name != "worker ) x" {
		t.Fatalf("CPU ranking: %+v", ranks.CPU)
	}
	if len(ranks.DiskIO) != 10 || ranks.DiskIO[0].TID != 110 || ranks.DiskIO[0].InputBytesPerSecond <= 0 {
		t.Fatalf("disk I/O ranking: %+v", ranks.DiskIO)
	}
	if len(ranks.Memory) != 1 || ranks.Memory[0].PID != 100 || ranks.Memory[0].RSSBytes != 5*uint64(os.Getpagesize()) {
		t.Fatalf("memory ranking: %+v", ranks.Memory)
	}
	if len(current) != 12 {
		t.Fatalf("expected a baseline for all 12 threads, got %d", len(current))
	}
}

func TestThreadNetworkRankingCombinesReceiveAndSend(t *testing.T) {
	var output strings.Builder
	output.WriteString("sample complete\n@rx[100, 105]: 300\n@tx[100,105]: 450\n")
	for tid := 106; tid < 117; tid++ {
		fmt.Fprintf(&output, "@tx[100, %d]: 600\n", tid)
	}
	ranks := parseThreadNetwork(output.String())
	if len(ranks) != 10 || ranks[0].PID != 100 || ranks[0].TID != 105 ||
		ranks[0].InputBytesPerSecond != 100 || ranks[0].OutputBytesPerSecond != 150 ||
		ranks[1].TID != 106 {
		t.Fatalf("network ranking: %+v", ranks)
	}
	if empty := parseThreadNetwork("sample complete\n"); empty == nil || len(empty) != 0 {
		t.Fatalf("completed sample with no traffic: %+v", empty)
	}
	if parseThreadNetwork("@rx[100, 105]: 300\n") != nil {
		t.Fatal("interrupted sample should be unavailable")
	}
}
