package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/guohai/server-status/internal/store"
)

func RunMaintenance(ctx context.Context, database *store.Store, logger *slog.Logger) {
	runMaintenanceCycle(ctx, database, logger)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runMaintenanceCycle(ctx, database, logger)
		}
	}
}

func runMaintenanceCycle(ctx context.Context, database *store.Store, logger *slog.Logger) {
	cycleCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	if err := database.MaintainPartitions(cycleCtx); err != nil {
		logger.Error("partition maintenance failed", "error", err)
		return
	}
	currentHour := time.Now().UTC().Truncate(time.Hour)
	for offset := 1; offset <= 25; offset++ {
		hour := currentHour.Add(-time.Duration(offset) * time.Hour)
		if err := database.RollupHour(cycleCtx, hour); err != nil {
			logger.Error("hourly rollup failed", "hour", hour, "error", err)
			return
		}
	}
	logger.Info("database maintenance completed", "through_hour", currentHour.Add(-time.Hour))
}
