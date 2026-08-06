//go:build linux

package agent

import (
	"os/exec"
	"syscall"
)

func isolateKVMCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
