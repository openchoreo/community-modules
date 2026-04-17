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

	app "github.com/openchoreo/community-modules/observability-tracing-opensearch/internal"
	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/opensearch"
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
		slog.String("OpenSearch Address", cfg.OpenSearchAddress),
		slog.String("OpenSearch Index Prefix", cfg.OpenSearchIndexPrefix),
		slog.String("OpenSearch Username", cfg.OpenSearchUsername),
		slog.String("OpenSearch Password", string(cfg.OpenSearchPassword[0])+"*****"),
		slog.Bool("OpenSearch TLS Skip Verify", cfg.OpenSearchTLSSkipVerify),
		slog.String("Server Port", cfg.ServerPort),
	)

	client, err := opensearch.NewClient(
		cfg.OpenSearchAddress,
		cfg.OpenSearchUsername,
		cfg.OpenSearchPassword,
		cfg.OpenSearchTLSSkipVerify,
		logger,
	)
	if err != nil {
		logger.Error("Failed to create OpenSearch client", slog.Any("error", err))
		os.Exit(1)
	}

	// Check OpenSearch connectivity when starting the adapter.
	logger.Info("Checking OpenSearch connectivity", slog.String("address", cfg.OpenSearchAddress))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.CheckHealth(ctx); err != nil {
		logger.Error("Failed to connect to OpenSearch. Cannot continue without it. Hence shutting down", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Successfully connected to OpenSearch")

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
