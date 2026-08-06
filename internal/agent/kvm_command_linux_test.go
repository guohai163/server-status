//go:build linux

package agent

import (
	"os/exec"
	"testing"
)

func TestKVMCommandRunsWithoutControllingTerminal(t *testing.T) {
	command := exec.Command("true")
	isolateKVMCommand(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.Setsid {
		t.Fatal("KVM command must start in a new session")
	}
}
