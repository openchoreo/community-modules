// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	app "github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal"
	"github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal/appinsights"
)

func main() {
	cfg, err := app.LoadConfig()
	if err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
		logger.Error("Failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	logger.Info("Configurations loaded from environment variables successfully",
		slog.String("Log Level", cfg.LogLevel.String()),
		slog.String("Log Analytics Workspace ID", cfg.WorkspaceID),
		slog.Duration("Query Timeout", cfg.QueryTimeout),
		slog.String("Server Port", cfg.ServerPort),
	)

	// DefaultAzureCredential resolves to Workload Identity in-cluster and to
	// the az CLI credential for local development.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		logger.Error("Failed to create Azure credential", slog.Any("error", err))
		os.Exit(1)
	}

	client, err := appinsights.NewClient(cred, appinsights.Config{
		WorkspaceID:  cfg.WorkspaceID,
		QueryTimeout: cfg.QueryTimeout,
	}, logger)
	if err != nil {
		logger.Error("Failed to create App Insights client", slog.Any("error", err))
		os.Exit(1)
	}

	// Check workspace reachability and credentials when starting the adapter.
	logger.Info("Checking Log Analytics workspace connectivity")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		logger.Error("Failed to query the Log Analytics workspace. Cannot continue without it. Hence shutting down", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Successfully connected to the Log Analytics workspace")

	// Create handlers and server
	tracingHandler := app.NewTracingHandler(client, logger)
	srv := app.NewServer(cfg.ServerPort, tracingHandler, logger)

	go func() {
		if err := srv.Start(); err != nil {
			logger.Error("Server error", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	// Shutdown logic
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down gracefully")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Error during shutdown", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Server stopped")
}
