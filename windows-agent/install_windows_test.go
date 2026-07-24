//go:build windows
// +build windows

package main

import (
	"strings"
	"testing"
)

func TestSmartctlInstallerCommandLeavesNSISDestinationUnquoted(t *testing.T) {
	installer := `C:\Program Files\ServerStatus\.smartctl-setup.exe`
	destination := `C:\Program Files\ServerStatus\smartmontools.new`
	arguments := smartctlInstallerArguments("amd64", destination)
	command := smartctlInstallerCommand(installer, arguments)
	if command.SysProcAttr == nil {
		t.Fatal("installer command is missing Windows process attributes")
	}
	commandLine := command.SysProcAttr.CmdLine
	if !strings.HasPrefix(commandLine, `"C:\Program Files\ServerStatus\.smartctl-setup.exe" `) {
		t.Fatalf("installer executable is not quoted correctly: %q", commandLine)
	}
	if !strings.HasSuffix(commandLine, " /D="+destination) {
		t.Fatalf("NSIS destination is not the final unquoted argument: %q", commandLine)
	}
	if strings.Contains(commandLine, `"/D=`) {
		t.Fatalf("NSIS destination must not be quoted: %q", commandLine)
	}
}
