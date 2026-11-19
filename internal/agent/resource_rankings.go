package agent

import (
	"cmp"
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/guohai/server-status/internal/report"
	"github.com/tklauser/go-sysconf"
)

type taskKey struct {
	pid, tid int
}

type taskSample struct {
	startTicks uint64
	cpuTicks   uint64
	readBytes  uint64
	writeBytes uint64
	hasIO      bool
	at         time.Time
}

func (collector *Collector) collectTaskRankings(ctx context.Context, procRoot string) *report.ResourceRankings {
	clockTicks, _ := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	collector.mu.Lock()
	defer collector.mu.Unlock()

	rankings, tasks := scanTaskRankings(ctx, procRoot, float64(clockTicks), collector.previousTasks)
	collector.previousTasks = tasks
	return rankings
}

func scanTaskRankings(ctx context.Context, procRoot string, clockTicks float64, previous map[taskKey]taskSample) (*report.ResourceRankings, map[taskKey]taskSample) {
	rankings := &report.ResourceRankings{}
	processDirs, err := os.ReadDir(procRoot)
	if err != nil {
		return rankings, nil
	}
	tasks := make(map[taskKey]taskSample, len(previous))
	for _, processDir := range processDirs {
		pid, err := strconv.Atoi(processDir.Name())
		if err != nil || pid <= 0 || !processDir.IsDir() {
			continue
		}
		taskRoot := filepath.Join(procRoot, processDir.Name(), "task")
		threadDirs, _ := os.ReadDir(taskRoot)
		for _, threadDir := range threadDirs {
			if ctx.Err() != nil {
				return &report.ResourceRankings{}, nil
			}
			tid, err := strconv.Atoi(threadDir.Name())
			if err != nil || tid <= 0 || !threadDir.IsDir() {
				continue
			}
			path := filepath.Join(taskRoot, threadDir.Name())
			stat, err := os.ReadFile(filepath.Join(path, "stat"))
			if err != nil {
				continue
			}
			name, rss, sample, ok := parseTaskStat(string(stat))
			if !ok {
				continue
			}
			sample.at = time.Now()
			if pid == tid {
				if rankings.Memory == nil {
					rankings.Memory = []report.ProcessMemoryUsage{}
				}
				if rss > 0 {
					rankings.Memory = append(rankings.Memory, report.ProcessMemoryUsage{PID: pid, Name: name, RSSBytes: rss})
				}
			}
			if data, err := os.ReadFile(filepath.Join(path, "io")); err == nil {
				sample.readBytes, sample.writeBytes, sample.hasIO = parseTaskIO(string(data))
			}

			key := taskKey{pid: pid, tid: tid}
			tasks[key] = sample
			prior, ok := previous[key]
			if ok && prior.startTicks == sample.startTicks {
				thread := report.ThreadIdentity{PID: pid, TID: tid, Name: name}
				appendTaskRates(rankings, thread, sample, prior, clockTicks)
			}
		}
	}
	rankings.CPU = topResourceUsage(rankings.CPU, func(row report.ThreadCPUUsage) (float64, int) { return row.Percent, row.TID })
	rankings.Memory = topResourceUsage(rankings.Memory, func(row report.ProcessMemoryUsage) (uint64, int) { return row.RSSBytes, row.PID })
	rankings.DiskIO = topThreadIO(rankings.DiskIO)
	return rankings, tasks
}

func appendTaskRates(rankings *report.ResourceRankings, thread report.ThreadIdentity, sample, prior taskSample, clockTicks float64) {
	seconds := sample.at.Sub(prior.at).Seconds()
	if seconds <= 0 {
		return
	}
	if clockTicks > 0 && sample.cpuTicks >= prior.cpuTicks {
		if rankings.CPU == nil {
			rankings.CPU = []report.ThreadCPUUsage{}
		}
		if delta := sample.cpuTicks - prior.cpuTicks; delta > 0 {
			percent := math.Min(100, float64(delta)*100/clockTicks/seconds)
			rankings.CPU = append(rankings.CPU, report.ThreadCPUUsage{ThreadIdentity: thread, Percent: percent})
		}
	}
	if sample.hasIO && prior.hasIO && sample.readBytes >= prior.readBytes && sample.writeBytes >= prior.writeBytes {
		if rankings.DiskIO == nil {
			rankings.DiskIO = []report.ThreadIOUsage{}
		}
		read, write := sample.readBytes-prior.readBytes, sample.writeBytes-prior.writeBytes
		if read > 0 || write > 0 {
			rankings.DiskIO = append(rankings.DiskIO, report.ThreadIOUsage{
				ThreadIdentity: thread, InputBytesPerSecond: float64(read) / seconds, OutputBytesPerSecond: float64(write) / seconds,
			})
		}
	}
}

func parseTaskStat(data string) (name string, rss uint64, sample taskSample, ok bool) {
	// The comm field can contain whitespace and parentheses, including ')'.
	open, close := strings.IndexByte(data, '('), strings.LastIndexByte(data, ')')
	if open < 0 || close <= open {
		return "", 0, taskSample{}, false
	}
	fields := strings.Fields(data[close+1:])
	if len(fields) < 22 {
		return "", 0, taskSample{}, false
	}
	user, userErr := strconv.ParseUint(fields[11], 10, 64)     // utime, field 14
	system, systemErr := strconv.ParseUint(fields[12], 10, 64) // stime, field 15
	start, startErr := strconv.ParseUint(fields[19], 10, 64)   // starttime, field 22
	pages, rssErr := strconv.ParseUint(fields[21], 10, 64)     // rss, field 24
	pageSize := uint64(os.Getpagesize())
	if userErr != nil || systemErr != nil || startErr != nil || user > math.MaxUint64-system {
		return "", 0, taskSample{}, false
	}
	if rssErr == nil && pages <= math.MaxInt64/pageSize {
		rss = pages * pageSize
	}
	return taskName(data[open+1 : close]), rss, taskSample{startTicks: start, cpuTicks: user + system}, true
}

func taskName(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(value, "?")))
}

func parseTaskIO(data string) (read, write uint64, ok bool) {
	var hasRead, hasWrite bool
	for line := range strings.SplitSeq(data, "\n") {
		key, value, _ := strings.Cut(line, ":")
		switch key {
		case "read_bytes":
			var err error
			read, err = strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			hasRead = err == nil
		case "write_bytes":
			var err error
			write, err = strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			hasWrite = err == nil
		}
	}
	return read, write, hasRead && hasWrite
}

func topThreadIO(items []report.ThreadIOUsage) []report.ThreadIOUsage {
	return topResourceUsage(items, func(row report.ThreadIOUsage) (float64, int) {
		return row.InputBytesPerSecond + row.OutputBytesPerSecond, row.TID
	})
}

func topResourceUsage[T any, S cmp.Ordered](items []T, scoreAndID func(T) (S, int)) []T {
	slices.SortFunc(items, func(a, b T) int {
		left, leftID := scoreAndID(a)
		right, rightID := scoreAndID(b)
		if left != right {
			return cmp.Compare(right, left)
		}
		return cmp.Compare(leftID, rightID)
	})
	return items[:min(len(items), report.ResourceRankingLimit)]
}
