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

	app "github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal"
	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/cloudwatch"
	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/observer"
)

func main() {
	cfg, err := app.LoadConfig()
	if err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
		logger.Error("Failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	logger.Info("Configuration loaded",
		slog.String("logLevel", cfg.LogLevel.String()),
		slog.String("awsRegion", cfg.AWSRegion),
		slog.String("clusterName", cfg.ClusterName),
		slog.String("logGroupPrefix", cfg.LogGroupPrefix),
		slog.String("queryTimeout", cfg.QueryTimeout.String()),
		slog.String("serverPort", cfg.ServerPort),
	)

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer bootstrapCancel()

	cwClient, err := cloudwatch.NewClient(bootstrapCtx, cloudwatch.Config{
		Region:                     cfg.AWSRegion,
		ClusterName:                cfg.ClusterName,
		LogGroupPrefix:             cfg.LogGroupPrefix,
		QueryTimeout:               cfg.QueryTimeout,
		PollEvery:                  cfg.QueryPollEvery,
		AlertMetricNamespace:       cfg.AlertMetricNamespace,
		AlarmActionARNs:            cfg.AlarmActionARNs,
		OKActionARNs:               cfg.OKActionARNs,
		InsufficientDataActionARNs: cfg.InsufficientDataActionARNs,
	}, logger)
	if err != nil {
		logger.Error("Failed to initialise AWS CloudWatch client", slog.Any("error", err))
		os.Exit(1)
	}

	// Verify AWS credentials up-front so the pod crashes-and-restarts rather than
	// silently serving failing queries. Matches the openobserve adapter's behaviour.
	if err := cwClient.Ping(bootstrapCtx); err != nil {
		logger.Error("Failed to verify AWS credentials via sts:GetCallerIdentity", slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("AWS credentials verified successfully")

	var observerClient *observer.Client
	if cfg.ObserverURL != "" {
		observerClient = observer.NewClient(cfg.ObserverURL)
	}
	if cfg.WebhookAuthEnabled {
		logger.Info("Webhook authentication enabled")
	}
	logsHandler := app.NewLogsHandlerWithOptions(cwClient, app.HandlerOptions{
		ObserverClient:           observerClient,
		SNSAllowSubscribeConfirm: cfg.SNSAllowSubscribeConfirm,
		ForwardRecovery:          cfg.ForwardRecovery,
	}, logger)
	srv := app.NewServer(cfg.ServerPort, logsHandler, cfg.WebhookSharedSecret, cfg.WebhookAuthEnabled, logger)

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Error during shutdown", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Server stopped")
}
