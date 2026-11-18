package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/guohai/server-status/db"
	appserver "github.com/guohai/server-status/internal/server"
	"github.com/guohai/server-status/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config, err := appserver.ConfigFromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) == 2 && os.Args[1] == "migrate" {
		migrationCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := db.MigrateComposeDatabase(migrationCtx, config.DatabaseURL, os.Getenv("SERVER_STATUS_POSTGRES_ADMIN_PASSWORD"))
		cancel()
		if err != nil {
			logger.Error("database migration failed", "error", err)
			os.Exit(1)
		}
		logger.Info("database migrations complete")
		return
	}
	database, err := store.New(ctx, config.DatabaseURL)
	if err != nil {
		logger.Error("database initialization failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	api := appserver.NewAPI(database, config, logger)
	defer api.Close()
	httpServer := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("central server started", "address", config.ListenAddress, "latest_agent_version", appserver.Version)
		serverErrors <- httpServer.ListenAndServe()
	}()
	go appserver.RunMaintenance(ctx, database, logger)
	go appserver.RunAlertEvaluation(ctx, database, logger)

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped", "error", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
