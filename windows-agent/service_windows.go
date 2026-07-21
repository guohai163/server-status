//go:build windows
// +build windows

package main

import (
	"fmt"
	"log"
	"os"

	"golang.org/x/sys/windows/svc"
)

const serviceName = "ServerStatusAgent"

// windowsService connects the Agent lifecycle to the Windows service manager.
type windowsService struct {
	configPath string
}

func runWindowsService(configPath string) error {
	appendServiceBootstrapLog(configPath, "service dispatcher starting")
	err := svc.Run(serviceName, &windowsService{configPath: configPath})
	if err != nil {
		appendServiceBootstrapLog(configPath, fmt.Sprintf("service dispatcher failed: %v", err))
	}
	return err
}

// Execute reports service state transitions and runs the Agent until SCM asks it to stop.
func (service *windowsService) Execute(
	_ []string,
	requests <-chan svc.ChangeRequest,
	statuses chan<- svc.Status,
) (bool, uint32) {
	appendServiceBootstrapLog(service.configPath, "service main callback entered")
	statuses <- svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: 15000}

	config, err := loadConfig(service.configPath)
	if err != nil {
		appendServiceBootstrapLog(service.configPath, fmt.Sprintf("load configuration failed: %v", err))
		return true, 1
	}
	logger, logFile, err := serviceLogger(service.configPath)
	if err != nil {
		appendServiceBootstrapLog(service.configPath, fmt.Sprintf("open service log failed: %v", err))
		return true, 2
	}
	defer logFile.Close()

	stop := make(chan struct{})
	result := make(chan error, 1)
	currentStatus := svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}
	statuses <- currentStatus
	logger.Printf("service started: version=%s server=%s", Version, config.ServerURL)
	go func() {
		result <- runAgent(stop, config, newWindowsCollector(), logger)
	}()

	stopping := false
	for {
		select {
		case err := <-result:
			if err != nil {
				logger.Printf("service stopped with error: %v", err)
				return true, 3
			}
			logger.Printf("service stopped")
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- currentStatus
			case svc.Stop, svc.Shutdown:
				if stopping {
					continue
				}
				stopping = true
				currentStatus = svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 30000}
				statuses <- currentStatus
				close(stop)
			}
		}
	}
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
