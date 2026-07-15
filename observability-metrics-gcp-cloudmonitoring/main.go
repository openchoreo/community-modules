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

	app "github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/auth"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/cloudmonitoring"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/observer"
)

func main() {
	cfg, err := app.LoadConfig()
	if err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
		logger.Error("failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	logger.Info("configuration loaded",
		slog.String("LOG_LEVEL", cfg.LogLevel.String()),
		slog.String("SERVER_PORT", cfg.ServerPort),
		slog.String("GCP_PROJECT_ID", cfg.ProjectID),
		slog.Duration("QUERY_TIMEOUT", cfg.QueryTimeout),
	)

	bootstrap := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 30*time.Second)
	}

	stepCtx, cancel := bootstrap()
	metricsClient, err := cloudmonitoring.NewClient(stepCtx, cloudmonitoring.Config{
		ProjectID:    cfg.ProjectID,
		QueryTimeout: cfg.QueryTimeout,
	}, logger.With("component", "cloudmonitoring"))
	cancel()
	if err != nil {
		logger.Error("failed to construct Cloud Monitoring client", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = metricsClient.Close() }()

	stepCtx, cancel = bootstrap()
	err = metricsClient.Ping(stepCtx)
	cancel()
	if err != nil {
		logger.Error("Cloud Monitoring ping failed at boot",
			slog.String("GCP_PROJECT_ID", cfg.ProjectID),
			slog.Any("error", err),
		)
		os.Exit(1)
	}
	logger.Info("Cloud Monitoring reachable", slog.String("GCP_PROJECT_ID", cfg.ProjectID))

	// Alert rule management. Wired only when both an Observer URL
	// and a pre-configured notification channel are set; otherwise the
	// alert-rule endpoints report not-implemented.
	var handler *app.MetricsHandler
	if cfg.AlertingEnabled() {
		stepCtx, cancel = bootstrap()
		alertClient, err := cloudmonitoring.NewAlertClient(stepCtx, cloudmonitoring.TranslatorConfig{
			ProjectID:             cfg.ProjectID,
			NotificationChannelID: cfg.NotificationChannelID,
			DefaultInterval:       cfg.AlertEvaluationInterval,
			DefaultWindow:         cfg.AlertWindow,
		}, cfg.QueryTimeout, logger.With("component", "alerts"))
		cancel()
		if err != nil {
			logger.Error("failed to construct alert client", slog.Any("error", err))
			os.Exit(1)
		}
		defer func() { _ = alertClient.Close() }()

		stepCtx, cancel = bootstrap()
		err = alertClient.VerifyNotificationChannel(stepCtx)
		cancel()
		if err != nil {
			logger.Error("notification channel verification failed at boot",
				slog.String("NOTIFICATION_CHANNEL_ID", cfg.NotificationChannelID), slog.Any("error", err))
			os.Exit(1)
		}
		logger.Info("alert rule management enabled",
			slog.String("OBSERVER_URL", cfg.ObserverURL),
			slog.String("NOTIFICATION_CHANNEL_ID", cfg.NotificationChannelID))

		obsClient := observer.NewClient(cfg.ObserverURL)
		handler = app.NewMetricsHandlerWithAlerting(metricsClient, alertClient, obsClient, logger.With("component", "handler"))
	} else {
		logger.Info("alert rule management disabled (set OBSERVER_URL and NOTIFICATION_CHANNEL_ID to enable)")
		handler = app.NewMetricsHandler(metricsClient, logger.With("component", "handler"))
	}

	// Guard the publicly exposed webhook path with the shared-secret check
	// when alerting is enabled.
	var middlewares []app.Middleware
	if cfg.AlertingEnabled() {
		middlewares = append(middlewares, auth.WebhookAuthMiddleware(cfg.WebhookSharedSecret, cfg.WebhookAuthEnabled, logger.With("component", "webhook-auth")))
	}
	srv := app.NewServer(cfg.ServerPort, handler, logger.With("component", "server"), middlewares...)

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
		logger.Info("shutdown signal received")
	case err := <-serverErrCh:
		logger.Error("server error", slog.Any("error", err))
		exitCode = 1
	}

	logger.Info("shutting down gracefully")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("error during shutdown", slog.Any("error", err))
		exitCode = 1
	}
	logger.Info("server stopped")
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
