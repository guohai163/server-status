//go:build windows
// +build windows

package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
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
	windowsInfinite        = 0xffffffff
	windowsWaitObject0     = 0
)

var (
	advapi32                          = syscall.NewLazyDLL("advapi32.dll")
	procStartServiceCtrlDispatcherW   = advapi32.NewProc("StartServiceCtrlDispatcherW")
	procRegisterServiceCtrlHandlerExW = advapi32.NewProc("RegisterServiceCtrlHandlerExW")
	procSetServiceStatus              = advapi32.NewProc("SetServiceStatus")
	procCreateEventW                  = kernel32.NewProc("CreateEventW")
	procCloseHandle                   = kernel32.NewProc("CloseHandle")
	procSetEvent                      = kernel32.NewProc("SetEvent")
	procWaitForSingleObject           = kernel32.NewProc("WaitForSingleObject")
	activeServiceConfigPath           string
	activeServiceStop                 chan struct{}
	activeServiceStopOnce             sync.Once
	activeServiceStatusHandle         uintptr
	activeServiceNamePointer          *uint16
	activeServiceTable                [2]serviceTableEntry
	serviceArgumentCount              uintptr
	serviceArgumentVector             **uint16
	serviceWorkerReadyEventHandle     uintptr
	serviceMainDoneEventHandle        uintptr
	serviceRegisterHandlerAddress     uintptr
	serviceSetEventAddress            uintptr
	serviceWaitForSingleObjectAddress uintptr
	serviceCallbacksOnce              sync.Once
	serviceControlCallbackPointer     uintptr
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
	// Go 1.10 cannot enter the runtime from a callback on the thread created by
	// SCM for ServiceMain. Keep the dispatcher on this Go-managed OS thread and
	// use the native assembly bridge in service_native_*.s for ServiceMain.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	activeServiceConfigPath = configPath
	activeServiceStatusHandle = 0
	name, err := syscall.UTF16PtrFromString(serviceName)
	if err != nil {
		return err
	}
	serviceCallbacksOnce.Do(func() {
		serviceControlCallbackPointer = syscall.NewCallback(serviceControlCallback)
	})
	workerReady, err := createWindowsEvent()
	if err != nil {
		return fmt.Errorf("create service worker event: %v", err)
	}
	defer closeWindowsHandle(workerReady)
	mainDone, err := createWindowsEvent()
	if err != nil {
		return fmt.Errorf("create service main event: %v", err)
	}
	defer closeWindowsHandle(mainDone)

	// The dispatcher retains this table for the lifetime of the service. Keep
	// both it and its UTF-16 name in globals because Go 1.10 cannot track their
	// lifetime after they are converted to uintptr for LazyProc.Call.
	activeServiceNamePointer = name
	activeServiceTable[0] = serviceTableEntry{Name: activeServiceNamePointer, Proc: serviceMainAddress()}
	activeServiceTable[1] = serviceTableEntry{}
	serviceWorkerReadyEventHandle = workerReady
	serviceMainDoneEventHandle = mainDone
	serviceRegisterHandlerAddress = procRegisterServiceCtrlHandlerExW.Addr()
	serviceSetEventAddress = procSetEvent.Addr()
	serviceWaitForSingleObjectAddress = procWaitForSingleObject.Addr()

	workerResult := make(chan error, 1)
	go func() {
		workerResult <- runServiceWorker(configPath)
	}()
	appendServiceBootstrapLog(configPath, "service dispatcher starting")
	ok, _, callErr := procStartServiceCtrlDispatcherW.Call(uintptr(unsafe.Pointer(&activeServiceTable[0])))
	if ok == 0 {
		_ = signalWindowsEvent(workerReady)
		<-workerResult
		appendServiceBootstrapLog(configPath, fmt.Sprintf("service dispatcher failed: %v", callErr))
		return fmt.Errorf("StartServiceCtrlDispatcher failed: %v", callErr)
	}
	return <-workerResult
}

func runServiceWorker(configPath string) error {
	if err := waitForWindowsEvent(serviceWorkerReadyEventHandle); err != nil {
		return fmt.Errorf("wait for native ServiceMain: %v", err)
	}
	defer signalWindowsEvent(serviceMainDoneEventHandle)
	if activeServiceStatusHandle == 0 {
		appendServiceBootstrapLog(configPath, "register service control handler failed")
		return fmt.Errorf("RegisterServiceCtrlHandlerEx failed")
	}

	appendServiceBootstrapLog(activeServiceConfigPath, "service main callback entered")
	setWindowsServiceStatus(serviceStartPending, 0, 1, 15000, 0)

	config, err := loadConfig(configPath)
	if err != nil {
		appendServiceBootstrapLog(configPath, fmt.Sprintf("load configuration failed: %v", err))
		setWindowsServiceStatus(serviceStopped, 0, 0, 0, 1)
		return err
	}
	logger, logFile, err := serviceLogger(configPath)
	if err != nil {
		appendServiceBootstrapLog(configPath, fmt.Sprintf("open service log failed: %v", err))
		setWindowsServiceStatus(serviceStopped, 0, 0, 0, 2)
		return err
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
		return err
	}
	logger.Printf("service stopped")
	setWindowsServiceStatus(serviceStopped, 0, 0, 0, 0)
	return nil
}

const pointerSize = 4 << (^uintptr(0) >> 63)

func addPointer(pointer unsafe.Pointer, offset uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(pointer) + offset)
}

func functionAddress(function interface{}) uintptr {
	return **(**uintptr)(addPointer(unsafe.Pointer(&function), pointerSize))
}

func serviceMainAddress() uintptr {
	return functionAddress(serviceMainNative)
}

// Implemented in service_native_386.s and service_native_amd64.s.
func serviceMainNative(argc uint32, argv **uint16)
func serviceControlNative(control uint32) uintptr

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

func createWindowsEvent() (uintptr, error) {
	handle, _, callErr := procCreateEventW.Call(0, 0, 0, 0)
	if handle == 0 {
		return 0, callErr
	}
	return handle, nil
}

func closeWindowsHandle(handle uintptr) {
	if handle != 0 {
		procCloseHandle.Call(handle)
	}
}

func signalWindowsEvent(handle uintptr) error {
	ok, _, callErr := procSetEvent.Call(handle)
	if ok == 0 {
		return callErr
	}
	return nil
}

func waitForWindowsEvent(handle uintptr) error {
	result, _, callErr := procWaitForSingleObject.Call(handle, windowsInfinite)
	if result != windowsWaitObject0 {
		return callErr
	}
	return nil
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

func appendServiceBootstrapLog(configPath, message string) {
	path := directoryOf(configPath) + "\\agent.log"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	logger := log.New(file, "", log.Ldate|log.Ltime|log.LUTC)
	logger.Printf("service bootstrap: %s", message)
	_ = file.Close()
	_ = protectPath(path)
}
