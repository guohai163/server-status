package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/guohai/server-status/internal/store"
)

func RunAlertEvaluation(ctx context.Context, database *store.Store, logger *slog.Logger) {
	runAlertEvaluationCycle(ctx, database, logger)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case evaluatedAt := <-ticker.C:
			runAlertEvaluationCycle(ctx, database, logger, evaluatedAt)
		}
	}
}

func runAlertEvaluationCycle(ctx context.Context, database *store.Store, logger *slog.Logger, evaluatedAt ...time.Time) {
	now := time.Now().UTC()
	if len(evaluatedAt) > 0 {
		now = evaluatedAt[0].UTC()
	}
	cycleCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := database.ReconcileAlerts(cycleCtx, now); err != nil {
		logger.Error("alert evaluation failed", "error", err)
	}
}
