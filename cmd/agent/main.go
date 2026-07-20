package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/guohai/server-status/internal/agent"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(agent.Version)
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config, err := agent.ConfigFromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	lock, err := agent.AcquireLock(config.LockFile)
	if err != nil {
		logger.Error("cannot acquire process lock", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("agent started", "version", agent.Version, "interval", config.Interval, "server", config.ServerURL)
	runError := agent.Run(ctx, config, logger)
	if errors.Is(runError, agent.ErrUpdateApplied) {
		if err := lock.Close(); err != nil {
			logger.Error("close process lock for restart", "error", err)
			os.Exit(1)
		}
		executable, err := os.Executable()
		if err != nil {
			logger.Error("locate updated Agent", "error", err)
			os.Exit(1)
		}
		logger.Info("restarting updated Agent")
		if err := syscall.Exec(executable, os.Args, os.Environ()); err != nil {
			logger.Error("restart updated Agent", "error", err)
			os.Exit(1)
		}
	}
	_ = lock.Close()
	if runError != nil {
		logger.Error("agent stopped with error", "error", runError)
		os.Exit(1)
	}
	logger.Info("agent stopped")
}
