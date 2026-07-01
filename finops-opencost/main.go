// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	app "github.com/openchoreo/community-modules/finops-opencost/internal"
	"github.com/openchoreo/community-modules/finops-opencost/internal/observer"
	"github.com/openchoreo/community-modules/finops-opencost/internal/opencost"
	"github.com/openchoreo/community-modules/finops-opencost/internal/recommend"
)

func main() {
	cfg, err := app.LoadConfig()
	if err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
		logger.Error("Failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	logger.Info("Configuration loaded from environment variables successfully",
		slog.String("LOG_LEVEL", cfg.LogLevel.String()),
		slog.String("OPENCOST_URL", cfg.OpenCostURL),
		slog.String("OBSERVER_URL", cfg.ObserverURL),
		slog.String("SERVER_PORT", cfg.ServerPort),
		slog.String("METRICS_STEP", cfg.MetricsStep),
		slog.Float64("RECOMMENDATION_CPU_PERCENTILE", cfg.RecommendationCPUPercentile),
		slog.Float64("RECOMMENDATION_MEMORY_PERCENTILE", cfg.RecommendationMemoryPercentile),
		slog.Float64("RECOMMENDATION_CPU_HEADROOM", cfg.RecommendationCPUHeadroom),
		slog.Float64("RECOMMENDATION_MEMORY_HEADROOM", cfg.RecommendationMemoryHeadroom),
		slog.Float64("RECOMMENDATION_CPU_MIN_REQUEST_MILLICORES", cfg.RecommendationMinCPURequest*1000),
		slog.Float64("RECOMMENDATION_MEMORY_MIN_REQUEST_MI", cfg.RecommendationMinMemRequest/(1024*1024)),
	)

	openCostClient := opencost.NewClient(cfg.OpenCostURL, logger)
	observerClient := observer.NewClient(cfg.ObserverURL)
	recommendCfg := recommend.Config{
		RecommendationCPUPercentile:    cfg.RecommendationCPUPercentile,
		RecommendationMemoryPercentile: cfg.RecommendationMemoryPercentile,
		RecommendationCPUHeadroom:      cfg.RecommendationCPUHeadroom,
		RecommendationMemoryHeadroom:   cfg.RecommendationMemoryHeadroom,
		RecommendationMinCPURequest:    cfg.RecommendationMinCPURequest,
		RecommendationMinMemRequest:    cfg.RecommendationMinMemRequest,
	}
	handler := app.NewCostHandler(openCostClient, observerClient, recommendCfg, cfg.MetricsStep, logger)
	srv := app.NewServer(cfg.ServerPort, handler, logger)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	var startErr bool
	select {
	case err := <-errCh:
		logger.Error("Server error", slog.Any("error", err))
		startErr = true
	case <-quit:
		logger.Info("Shutting down gracefully")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), app.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Error during shutdown", slog.Any("error", err))
		os.Exit(1)
	}

	if startErr {
		os.Exit(1)
	}

	logger.Info("Server stopped")
}
