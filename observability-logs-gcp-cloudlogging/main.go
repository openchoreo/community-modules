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

	"cloud.google.com/go/logging/logadmin"

	app "github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/auth"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/cloudlogging"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/cloudmonitoring"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/observer"
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
		slog.String("logLevel", cfg.LogLevel.String()),
		slog.String("serverPort", cfg.ServerPort),
		slog.String("projectId", cfg.ProjectID),
		slog.Duration("queryTimeout", cfg.QueryTimeout),
		slog.Bool("sanitizePodLabelDots", cfg.SanitizePodLabelDots),
	)

	// Apply the pod-label dot-sanitization policy to both the query and alert
	// filter builders before any request is served.
	cloudlogging.SanitizePodLabelDots = cfg.SanitizePodLabelDots
	cloudmonitoring.SanitizePodLabelDots = cfg.SanitizePodLabelDots

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer bootstrapCancel()

	// logadmin resolves credentials through Application Default Credentials:
	// GKE Workload Identity in production, GOOGLE_APPLICATION_CREDENTIALS for
	// the static-key fallback on non-GKE clusters.
	logClient, err := cloudlogging.NewClient(bootstrapCtx, cloudlogging.Config{
		ProjectID:    cfg.ProjectID,
		QueryTimeout: cfg.QueryTimeout,
	}, logger.With("component", "cloudlogging"))
	if err != nil {
		logger.Error("failed to construct Cloud Logging client", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = logClient.Close() }()

	if err := logClient.Ping(bootstrapCtx); err != nil {
		logger.Error("Cloud Logging ping failed at boot",
			slog.String("projectId", cfg.ProjectID),
			slog.Any("error", err),
		)
		os.Exit(1)
	}
	logger.Info("Cloud Logging reachable", slog.String("projectId", cfg.ProjectID))

	// A dedicated logadmin client backs the log-based metric CRUD used by
	// alerting (kept separate from the query client's internals). It shares
	// ADC, so no extra credential wiring is needed.
	metricAdmin, err := logadmin.NewClient(bootstrapCtx, cfg.ProjectID)
	if err != nil {
		logger.Error("failed to construct log metric admin client", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = metricAdmin.Close() }()

	alertClient, err := cloudmonitoring.NewClient(bootstrapCtx, metricAdmin, cloudmonitoring.Config{
		ProjectID:             cfg.ProjectID,
		NotificationChannelID: cfg.NotificationChannelID,
	}, logger.With("component", "cloudmonitoring"))
	if err != nil {
		logger.Error("failed to construct Cloud Monitoring client", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = alertClient.Close() }()

	if err := alertClient.VerifyNotificationChannel(bootstrapCtx); err != nil {
		logger.Error("notification channel verification failed at boot",
			slog.String("notificationChannelId", cfg.NotificationChannelID),
			slog.Any("error", err),
		)
		os.Exit(1)
	}
	if cfg.NotificationChannelID != "" {
		logger.Info("notification channel reachable", slog.String("notificationChannelId", cfg.NotificationChannelID))
	}

	obsClient := observer.NewClient(cfg.ObserverURL)
	handler := app.NewLogsHandler(logClient, alertClient, obsClient, logger.With("component", "handler"))

	webhookAuth := app.Middleware(
		auth.WebhookAuthMiddleware(cfg.WebhookSharedSecret, cfg.WebhookAuthEnabled, logger.With("component", "webhook-auth")),
	)

	srv := app.NewServer(cfg.ServerPort, handler, logger.With("component", "server"), webhookAuth)

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
