// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	app "github.com/openchoreo/community-modules/observability-logs-openobserve/internal"
	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/openobserve"
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
		slog.String("OpenObserve URL", cfg.OpenObserveURL),
		slog.String("OpenObserve Org", cfg.OpenObserveOrg),
		slog.String("OpenObserve Stream", cfg.OpenObserveStream),
		slog.String("OpenObserve User", cfg.OpenObserveUser),
		slog.String("OpenObserve Password", string(cfg.OpenObservePassword[0])+"*****"),
		slog.String("Server Port", cfg.ServerPort),
	)

	client := openobserve.NewClient(
		cfg.OpenObserveURL,
		cfg.OpenObserveOrg,
		cfg.OpenObserveStream,
		cfg.OpenObserveUser,
		cfg.OpenObservePassword,
		logger,
	)

	// Check OpenObserve connectivity when starting the adapter. If the connection fails,
	// exit with an error because the adapter cannot function without connecting to
	// OpenObserve.
	healthURL := cfg.OpenObserveURL + "/healthz"
	logger.Info("Checking OpenObserve connectivity", slog.String("url", healthURL))

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(healthURL)
	if err != nil {
		logger.Error("Failed to connect to OpenObserve. Cannot continue without it. Hence shutting down", slog.Any("error", err))
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read OpenObserve health response", slog.Any("error", err))
		os.Exit(1)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("OpenObserve health check failed",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		os.Exit(1)
	}

	var healthResp map[string]interface{}
	if err := json.Unmarshal(body, &healthResp); err != nil {
		logger.Error("Failed to parse OpenObserve health response", slog.Any("error", err))
		os.Exit(1)
	}

	status, ok := healthResp["status"].(string)
	if !ok || status != "ok" {
		logger.Error("OpenObserve health check returned unexpected status",
			slog.String("status", fmt.Sprintf("%v", healthResp["status"])))
		os.Exit(1)
	}

	logger.Info("Successfully connected to OpenObserve")

	// Create handlers and server
	logsHandler := app.NewLogsHandler(client, logger)
	srv := app.NewServer(cfg.ServerPort, logsHandler, logger)

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Error during shutdown", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Server stopped")
}
