//go:build !linux

package agent

import "os/exec"

func isolateKVMCommand(_ *exec.Cmd) {}
