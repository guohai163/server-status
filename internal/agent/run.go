package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"
)

func AcquireLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another agent process is already running: %w", err)
	}
	return file, nil
}

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	collector := NewCollector()
	client := NewClient(config.ServerURL, config.Token)
	updater := NewUpdater(config.ServerURL, Version)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			started := time.Now()
			payload, err := collector.Collect(ctx, config)
			if err != nil {
				logger.Error("collection failed", "error", err)
			} else if receipt, err := client.Send(ctx, payload); err != nil {
				logger.Error("report failed", "error", err, "collected_at", payload.CollectedAt)
			} else {
				logger.Info("report accepted", "collected_at", payload.CollectedAt, "duration", time.Since(started))
				if receipt.AgentUpdate != nil {
					logger.Info("Agent update requested", "current_version", Version, "target_version", receipt.AgentUpdate.Version)
					if err := updater.Apply(ctx, receipt.AgentUpdate.Version); err != nil {
						logger.Error("Agent update failed", "target_version", receipt.AgentUpdate.Version, "error", err)
					} else {
						logger.Info("Agent update installed; restarting", "target_version", receipt.AgentUpdate.Version)
						return ErrUpdateApplied
					}
				}
			}
			nextDelay := config.Interval - time.Since(started)
			if nextDelay < 0 {
				nextDelay = 0
			}
			timer.Reset(nextDelay)
		}
	}
}
