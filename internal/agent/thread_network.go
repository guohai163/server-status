package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/guohai/server-status/internal/report"
)

const threadNetworkSampleSeconds = 3

// These tracepoints count payload bytes in the thread doing socket I/O.
// AF_INET/AF_INET6 exclude local UNIX sockets. MSG_PEEK and MSG_ERRQUEUE
// are excluded from receives because they do not consume socket payload.
const threadNetworkProgram = `
tracepoint:sock:sock_recv_length
/pid > 0 && args->ret > 0 && (args->family == 2 || args->family == 10) && !(args->flags & 0x2002)/
{
    @rx[pid, tid] = sum((uint64)args->ret);
}

tracepoint:sock:sock_send_length
/pid > 0 && args->ret > 0 && (args->family == 2 || args->family == 10)/
{
    @tx[pid, tid] = sum((uint64)args->ret);
}

interval:s:%d
{
    printf("sample complete\n");
    exit();
}
`

var threadNetworkCounter = regexp.MustCompile(`^@(rx|tx)\[(\d+),\s*(\d+)\]:\s*(\d+)$`)

func sampleThreadNetwork(ctx context.Context, procRoot string) []report.ThreadIOUsage {
	path, err := exec.LookPath("bpftrace")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "-q", "-f", "text", "-e", fmt.Sprintf(threadNetworkProgram, threadNetworkSampleSeconds)).Output()
	if err != nil {
		return nil
	}

	rows := parseThreadNetwork(string(output))
	for i := range rows {
		path := filepath.Join(procRoot, strconv.Itoa(rows[i].PID), "task", strconv.Itoa(rows[i].TID), "comm")
		if name, err := os.ReadFile(path); err == nil {
			rows[i].Name = taskName(string(name))
		}
	}
	return rows
}

func parseThreadNetwork(output string) []report.ThreadIOUsage {
	complete := false
	usage := make(map[taskKey]report.ThreadIOUsage)
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "sample complete" {
			complete = true
			continue
		}
		if !strings.HasPrefix(line, "@") {
			continue
		}
		match := threadNetworkCounter.FindStringSubmatch(line)
		if match == nil {
			return nil
		}
		pid, pidErr := strconv.Atoi(match[2])
		tid, tidErr := strconv.Atoi(match[3])
		bytes, bytesErr := strconv.ParseUint(match[4], 10, 64)
		if pidErr != nil || tidErr != nil || bytesErr != nil || pid <= 0 || tid <= 0 {
			return nil
		}
		if bytes == 0 {
			continue
		}
		key := taskKey{pid, tid}
		row := usage[key]
		row.PID, row.TID = pid, tid
		if match[1] == "rx" {
			row.InputBytesPerSecond = float64(bytes) / threadNetworkSampleSeconds
		} else {
			row.OutputBytesPerSecond = float64(bytes) / threadNetworkSampleSeconds
		}
		usage[key] = row
	}
	if !complete {
		return nil
	}
	rows := make([]report.ThreadIOUsage, 0, len(usage))
	for _, row := range usage {
		rows = append(rows, row)
	}
	return topThreadIO(rows)
}
