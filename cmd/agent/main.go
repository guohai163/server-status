package main

import (
	"context"
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
	defer lock.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("agent started", "version", agent.Version, "interval", config.Interval, "server", config.ServerURL)
	if err := agent.Run(ctx, config, logger); err != nil {
		logger.Error("agent stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("agent stopped")
}
