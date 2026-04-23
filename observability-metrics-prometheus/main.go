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

	app "github.com/openchoreo/community-modules/observability-metrics-prometheus/internal"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/k8s"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/observer"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/prometheus"
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
		slog.String("Server Port", cfg.ServerPort),
		slog.String("Prometheus Address", cfg.PrometheusAddress),
		slog.String("Alert Rule Namespace", cfg.AlertRuleNamespace),
	)

	promClient, err := prometheus.NewClient(cfg.PrometheusAddress, logger)
	if err != nil {
		logger.Error("Failed to create Prometheus client", slog.Any("error", err))
		os.Exit(1)
	}

	if err := promClient.HealthCheck(context.Background()); err != nil {
		logger.Error("Failed to connect to Prometheus", slog.String("address", cfg.PrometheusAddress), slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("Successfully connected to Prometheus", slog.String("address", cfg.PrometheusAddress))

	k8sClient, err := k8s.NewClient(cfg.AlertRuleNamespace)
	if err != nil {
		logger.Warn("Failed to create Kubernetes client; alert rule management will be unavailable", slog.Any("error", err))
	}

	observerClient := observer.NewClient(cfg.ObserverAPIInternalURL)
	metricsHandler := app.NewMetricsHandler(promClient, k8sClient, observerClient, cfg.AlertRuleNamespace, logger)
	srv := app.NewServer(cfg.ServerPort, metricsHandler, logger)

	serverErrCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			serverErrCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	exitCode := 0
	select {
	case <-quit:
	case err := <-serverErrCh:
		logger.Error("Server error", slog.Any("error", err))
		exitCode = 1
	}

	logger.Info("Shutting down gracefully")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Error during shutdown", slog.Any("error", err))
		exitCode = 1
	}

	logger.Info("Server stopped")
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
