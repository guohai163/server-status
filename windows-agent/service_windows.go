//go:build windows
// +build windows

package main

import (
	"fmt"
	"log"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

const (
	serviceName            = "ServerStatusAgent"
	serviceWin32OwnProcess = 0x00000010
	serviceStartPending    = 0x00000002
	serviceStopPending     = 0x00000003
	serviceRunning         = 0x00000004
	serviceStopped         = 0x00000001
	serviceAcceptStop      = 0x00000001
	serviceAcceptShutdown  = 0x00000004
	serviceControlStop     = 0x00000001
	serviceControlShutdown = 0x00000005
	serviceErrorSpecific   = 1066
)

var (
	advapi32                          = syscall.NewLazyDLL("advapi32.dll")
	procStartServiceCtrlDispatcherW   = advapi32.NewProc("StartServiceCtrlDispatcherW")
	procRegisterServiceCtrlHandlerExW = advapi32.NewProc("RegisterServiceCtrlHandlerExW")
	procSetServiceStatus              = advapi32.NewProc("SetServiceStatus")
	activeServiceConfigPath           string
	activeServiceStop                 chan struct{}
	activeServiceStopOnce             sync.Once
	activeServiceStatusHandle         uintptr
)

type serviceTableEntry struct {
	Name *uint16
	Proc uintptr
}

type serviceStatus struct {
	ServiceType             uint32
	CurrentState            uint32
	ControlsAccepted        uint32
	Win32ExitCode           uint32
	ServiceSpecificExitCode uint32
	CheckPoint              uint32
	WaitHint                uint32
}

func runWindowsService(configPath string) error {
	activeServiceConfigPath = configPath
	name, err := syscall.UTF16PtrFromString(serviceName)
	if err != nil {
		return err
	}
	table := [2]serviceTableEntry{
		{Name: name, Proc: syscall.NewCallback(serviceMainCallback)},
		{},
	}
	ok, _, callErr := procStartServiceCtrlDispatcherW.Call(uintptr(unsafe.Pointer(&table[0])))
	if ok == 0 {
		return fmt.Errorf("StartServiceCtrlDispatcher failed: %v", callErr)
	}
	return nil
}

func serviceMainCallback(argc, argv uintptr) uintptr {
	name, _ := syscall.UTF16PtrFromString(serviceName)
	handle, _, callErr := procRegisterServiceCtrlHandlerExW.Call(
		uintptr(unsafe.Pointer(name)), syscall.NewCallback(serviceControlCallback), 0)
	if handle == 0 {
		_ = callErr
		return 0
	}
	activeServiceStatusHandle = handle
	setWindowsServiceStatus(serviceStartPending, 0, 1, 15000, 0)

	config, err := loadConfig(activeServiceConfigPath)
	if err != nil {
		setWindowsServiceStatus(serviceStopped, 0, 0, 0, 1)
		return 0
	}
	logger, logFile, err := serviceLogger(activeServiceConfigPath)
	if err != nil {
		setWindowsServiceStatus(serviceStopped, 0, 0, 0, 2)
		return 0
	}
	defer logFile.Close()
	activeServiceStop = make(chan struct{})
	activeServiceStopOnce = sync.Once{}
	setWindowsServiceStatus(serviceRunning, serviceAcceptStop|serviceAcceptShutdown, 0, 0, 0)
	logger.Printf("service started: version=%s server=%s", Version, config.ServerURL)
	err = runAgent(activeServiceStop, config, newWindowsCollector(), logger)
	if err != nil {
		logger.Printf("service stopped with error: %v", err)
		setWindowsServiceStatus(serviceStopped, 0, 0, 0, 3)
		return 0
	}
	logger.Printf("service stopped")
	setWindowsServiceStatus(serviceStopped, 0, 0, 0, 0)
	return 0
}

func serviceControlCallback(control, eventType, eventData, context uintptr) uintptr {
	switch uint32(control) {
	case serviceControlStop, serviceControlShutdown:
		setWindowsServiceStatus(serviceStopPending, 0, 1, 30000, 0)
		if activeServiceStop != nil {
			activeServiceStopOnce.Do(func() { close(activeServiceStop) })
		}
	}
	return 0
}

func setWindowsServiceStatus(state, accepted, checkpoint, waitHint, specificExit uint32) {
	if activeServiceStatusHandle == 0 {
		return
	}
	status := serviceStatus{
		ServiceType: serviceWin32OwnProcess, CurrentState: state,
		ControlsAccepted: accepted, CheckPoint: checkpoint, WaitHint: waitHint,
	}
	if specificExit != 0 {
		status.Win32ExitCode = serviceErrorSpecific
		status.ServiceSpecificExitCode = specificExit
	}
	procSetServiceStatus.Call(activeServiceStatusHandle, uintptr(unsafe.Pointer(&status)))
}

func serviceLogger(configPath string) (*log.Logger, *os.File, error) {
	path := directoryOf(configPath) + "\\agent.log"
	if info, err := os.Stat(path); err == nil && info.Size() >= 10*1024*1024 {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, err
	}
	if err := protectPath(path); err != nil {
		file.Close()
		return nil, nil, err
	}
	return log.New(file, "", log.Ldate|log.Ltime|log.LUTC), file, nil
}
