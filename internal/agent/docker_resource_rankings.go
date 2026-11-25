package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/guohai/server-status/internal/report"
)

var dockerIDPattern = regexp.MustCompile("^[0-9a-f]{64}$")

func addDockerContainerNames(ctx context.Context, procRoot string, rankings *report.ResourceRankings) {
	destinations := make(map[int][]*string)
	for i := range rankings.CPU {
		row := &rankings.CPU[i]
		destinations[row.PID] = append(destinations[row.PID], &row.DockerContainer)
	}
	for i := range rankings.Memory {
		row := &rankings.Memory[i]
		destinations[row.PID] = append(destinations[row.PID], &row.DockerContainer)
	}
	for _, rows := range [][]report.ThreadIOUsage{rankings.DiskIO, rankings.Network} {
		for i := range rows {
			row := &rows[i]
			destinations[row.PID] = append(destinations[row.PID], &row.DockerContainer)
		}
	}

	containersByPID := make(map[int]string)
	for pid := range destinations {
		cgroup, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cgroup"))
		if err != nil {
			continue
		}
		if id := dockerIDFromCgroup(string(cgroup)); id != "" {
			containersByPID[pid] = id
		}
	}
	if len(containersByPID) == 0 {
		return
	}

	names := listDockerContainerNames(ctx)
	for pid, id := range containersByPID {
		for _, destination := range destinations[pid] {
			*destination = names[id]
		}
	}
}

func dockerIDFromCgroup(data string) string {
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 {
			continue
		}
		parts := strings.Split(fields[2], "/")
		for index, part := range slices.Backward(parts) {
			if scope, ok := strings.CutPrefix(part, "docker-"); ok {
				if id, ok := strings.CutSuffix(scope, ".scope"); ok && dockerIDPattern.MatchString(id) {
					return id
				}
			}
			if index > 0 && parts[index-1] == "docker" && dockerIDPattern.MatchString(part) {
				return part
			}
		}
	}
	return ""
}

func listDockerContainerNames(ctx context.Context) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Local process IDs must only be matched against the local Docker daemon.
	cmd := exec.CommandContext(ctx, "docker", "--host=unix:///var/run/docker.sock", "container", "ls",
		"--no-trunc", "--format", "{{.ID}}\t{{.Names}}")
	cmd.Env = append(cmd.Environ(), "DOCKER_CONTEXT=")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	names := make(map[string]string)
	for line := range strings.SplitSeq(string(out), "\n") {
		id, namesField, _ := strings.Cut(line, "\t")
		name, _, _ := strings.Cut(namesField, ",")
		if dockerIDPattern.MatchString(id) && name != "" && len(name) <= report.DockerContainerNameLimit && !strings.ContainsRune(name, 0) {
			names[id] = name
		}
	}
	return names
}
