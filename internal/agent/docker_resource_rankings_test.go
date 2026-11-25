package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guohai/server-status/internal/report"
)

func TestDockerIDFromCgroup(t *testing.T) {
	id := strings.Repeat("a", 64)
	inner := strings.Repeat("b", 64)
	tests := []struct {
		name, cgroup, want string
	}{
		{"systemd", "0::/system.slice/docker-" + id + ".scope\n", id},
		{"cgroupfs", "11:memory:/docker/" + id + "/child\n5:cpu:/\n", id},
		{"nested", "0::/docker/" + id + "/docker-" + inner + ".scope\n", inner},
		{"other runtime", "0::/system.slice/libpod-" + id + ".scope\n", ""},
		{"host", "0::/system.slice/postgresql.service\n", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dockerIDFromCgroup(test.cgroup); got != test.want {
				t.Errorf("docker ID = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAddDockerContainerNames(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("a", 64)
	processDir := filepath.Join(root, "42")
	if err := os.Mkdir(processDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(processDir, "cgroup"), []byte("0::/system.slice/docker-"+id+".scope\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("DOCKER_CONTEXT", "remote")
	docker := "#!/bin/sh\n" +
		"test \"$1\" = '--host=unix:///var/run/docker.sock' && test -z \"$DOCKER_CONTEXT\" || exit 1\n" +
		"printf '%s\\t%s\\n' '" + id + "' 'service-postgres,legacy/alias'\n"
	if err := os.WriteFile(filepath.Join(root, "docker"), []byte(docker), 0700); err != nil {
		t.Fatal(err)
	}

	thread := report.ThreadIdentity{PID: 42, TID: 43}
	rankings := report.ResourceRankings{
		CPU:     []report.ThreadCPUUsage{{ThreadIdentity: thread}},
		Memory:  []report.ProcessMemoryUsage{{PID: 42, Name: "postgres"}, {PID: 99, Name: "postgres"}},
		DiskIO:  []report.ThreadIOUsage{{ThreadIdentity: thread}},
		Network: []report.ThreadIOUsage{{ThreadIdentity: thread}},
	}
	addDockerContainerNames(t.Context(), root, &rankings)
	for metric, name := range map[string]string{
		"cpu":     rankings.CPU[0].DockerContainer,
		"memory":  rankings.Memory[0].DockerContainer,
		"disk_io": rankings.DiskIO[0].DockerContainer,
		"network": rankings.Network[0].DockerContainer,
	} {
		if name != "service-postgres" {
			t.Errorf("%s container = %q, want service-postgres", metric, name)
		}
	}
	if name := rankings.Memory[1].DockerContainer; name != "" {
		t.Errorf("unexpected container for PID 99: %q", name)
	}
}
