//go:build windows
// +build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	securityDescriptorRevision = 1
	daclSecurityInformation    = 0x00000004
	protectedDACLInformation   = 0x80000000
)

var (
	installAdvapi32                                          = syscall.NewLazyDLL("advapi32.dll")
	installKernel32                                          = syscall.NewLazyDLL("kernel32.dll")
	procConvertStringSecurityDescriptorToSecurityDescriptorW = installAdvapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procSetFileSecurityW                                     = installAdvapi32.NewProc("SetFileSecurityW")
	procLocalFree                                            = installKernel32.NewProc("LocalFree")
)

func defaultInstallDirectory() string {
	programFiles := strings.TrimSpace(os.Getenv("ProgramFiles"))
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	return filepath.Join(programFiles, "ServerStatus")
}

func defaultConfigPath() string {
	return filepath.Join(defaultInstallDirectory(), "agent.json")
}

func installService(config Config) error {
	directory := defaultInstallDirectory()
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create install directory: %v", err)
	}
	if err := protectPath(directory); err != nil {
		return fmt.Errorf("protect install directory: %v", err)
	}
	configPath := defaultConfigPath()
	if err := writeConfig(configPath, config); err != nil {
		return fmt.Errorf("write config: %v", err)
	}
	if err := protectPath(configPath); err != nil {
		return fmt.Errorf("protect config: %v", err)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %v", err)
	}
	executable, _ = filepath.Abs(executable)
	target := filepath.Join(directory, "server-status-agent.exe")
	serviceExists := exec.Command("sc.exe", "query", serviceName).Run() == nil
	if serviceExists {
		_ = exec.Command("sc.exe", "stop", serviceName).Run()
	}
	if !samePath(executable, target) {
		if err := replaceExecutableAfterStop(executable, target); err != nil {
			return err
		}
	}
	if err := protectPath(target); err != nil {
		return fmt.Errorf("protect executable: %v", err)
	}

	commandLine := fmt.Sprintf(`"%s" service --config "%s"`, target, configPath)
	if serviceExists {
		if output, err := exec.Command("sc.exe", "config", serviceName,
			"binPath=", commandLine, "start=", "auto", "DisplayName=", "Server Status Agent").CombinedOutput(); err != nil {
			return fmt.Errorf("update Windows service: %v: %s", err, strings.TrimSpace(string(output)))
		}
	} else {
		if output, err := exec.Command("sc.exe", "create", serviceName,
			"binPath=", commandLine, "start=", "auto", "DisplayName=", "Server Status Agent").CombinedOutput(); err != nil {
			return fmt.Errorf("create Windows service: %v: %s", err, strings.TrimSpace(string(output)))
		}
	}
	_ = exec.Command("sc.exe", "description", serviceName, "Collects Windows server status and reports it to the central service.").Run()
	if output, err := exec.Command("sc.exe", "start", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("start Windows service: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func removeService(purge bool) error {
	_ = exec.Command("sc.exe", "stop", serviceName).Run()
	time.Sleep(time.Second)
	output, err := exec.Command("sc.exe", "delete", serviceName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete Windows service: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if purge {
		return os.RemoveAll(defaultInstallDirectory())
	}
	return nil
}

func serviceCommand(action string) error {
	command := "query"
	switch action {
	case "start", "stop":
		command = action
	case "status":
	default:
		return errors.New("unsupported service action")
	}
	cmd := exec.Command("sc.exe", command, serviceName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func replaceExecutableAfterStop(source, target string) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := copyFile(source, target+".new")
		if err == nil {
			err = os.Rename(target+".new", target)
		}
		if err == nil {
			return nil
		}
		_ = os.Remove(target + ".new")
		if time.Now().After(deadline) {
			return fmt.Errorf("replace service executable after stopping it: %v", err)
		}
		time.Sleep(time.Second)
	}
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func protectPath(path string) error {
	sddl, _ := syscall.UTF16PtrFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)")
	var descriptor uintptr
	var descriptorSize uint32
	ok, _, callErr := procConvertStringSecurityDescriptorToSecurityDescriptorW.Call(
		uintptr(unsafe.Pointer(sddl)), securityDescriptorRevision,
		uintptr(unsafe.Pointer(&descriptor)), uintptr(unsafe.Pointer(&descriptorSize)))
	if ok == 0 {
		return callErr
	}
	defer procLocalFree.Call(descriptor)
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	ok, _, callErr = procSetFileSecurityW.Call(
		uintptr(unsafe.Pointer(pathPointer)), daclSecurityInformation|protectedDACLInformation, descriptor)
	if ok == 0 {
		return callErr
	}
	return nil
}

func samePath(first, second string) bool {
	firstAbsolute, _ := filepath.Abs(first)
	secondAbsolute, _ := filepath.Abs(second)
	return strings.EqualFold(firstAbsolute, secondAbsolute)
}

func directoryOf(path string) string {
	return filepath.Dir(path)
}
