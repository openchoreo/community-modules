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

	app "github.com/openchoreo/community-modules/observability-logs-opensearch/internal"
	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/observer"
	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
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
		slog.String("logLevel", cfg.LogLevel.String()),
		slog.String("openSearchAddress", cfg.OpenSearchAddress),
		slog.String("openSearchIndexPrefix", cfg.OpenSearchIndexPrefix),
		slog.String("serverPort", cfg.ServerPort),
		slog.String("observerURL", cfg.ObserverURL),
	)

	osClient, err := opensearch.NewClient(
		cfg.OpenSearchAddress,
		cfg.OpenSearchUsername,
		cfg.OpenSearchPassword,
		cfg.TLSSkipVerify,
		logger,
	)
	if err != nil {
		logger.Error("Failed to create OpenSearch client", slog.Any("error", err))
		os.Exit(1)
	}

	// Check OpenSearch connectivity when starting the adapter. If the connection fails,
	// exit with an error because the adapter cannot function without connecting to
	// OpenSearch.
	logger.Info("Checking OpenSearch connectivity", slog.String("address", cfg.OpenSearchAddress))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := osClient.CheckHealth(ctx); err != nil {
		logger.Error("Failed to connect to OpenSearch. Cannot continue without it. Shutting down",
			slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Successfully connected to OpenSearch")

	queryBuilder := opensearch.NewQueryBuilder(cfg.OpenSearchIndexPrefix)
	observerClient := observer.NewClient(cfg.ObserverURL)
	logsHandler := app.NewLogsHandler(osClient, queryBuilder, observerClient, logger)
	srv := app.NewServer(cfg.ServerPort, logsHandler, logger)

	go func() {
		if err := srv.Start(); err != nil {
			logger.Error("Server error", slog.Any("error", err))
			os.Exit(1)
		}
	}()

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
