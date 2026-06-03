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

	app "github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/auth"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/azuremonitor"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/observer"
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
		slog.String("workspaceId", cfg.WorkspaceID),
		slog.Duration("queryTimeout", cfg.QueryTimeout),
	)

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer bootstrapCancel()

	// DefaultAzureCredential walks the standard chain: env vars, Workload
	// Identity, Managed Identity, Azure CLI. It handles audience switching
	// (api.loganalytics.io for queries, management.azure.com for ARM) so the
	// same credential is reused for both clients.
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		logger.Error("failed to construct DefaultAzureCredential", slog.Any("error", err))
		os.Exit(1)
	}

	laClient, err := loganalytics.NewClient(cred, loganalytics.Config{
		WorkspaceID:  cfg.WorkspaceID,
		QueryTimeout: cfg.QueryTimeout,
	}, logger.With("component", "loganalytics"))
	if err != nil {
		logger.Error("failed to construct Log Analytics client", slog.Any("error", err))
		os.Exit(1)
	}

	if err := laClient.Ping(bootstrapCtx); err != nil {
		logger.Error("Log Analytics ping failed at boot",
			slog.String("workspaceId", cfg.WorkspaceID),
			slog.Any("error", err),
		)
		os.Exit(1)
	}
	logger.Info("Log Analytics workspace reachable", slog.String("workspaceId", cfg.WorkspaceID))

	alertClient, err := azuremonitor.NewClient(cred, azuremonitor.Config{
		SubscriptionID:             cfg.SubscriptionID,
		ResourceGroup:              cfg.ResourceGroup,
		Region:                     cfg.Region,
		WorkspaceResourceID:        cfg.WorkspaceResourceID,
		ActionGroupID:              cfg.ActionGroupID,
		DefaultEvaluationFrequency: cfg.DefaultEvaluationFrequency,
		DefaultWindowSize:          cfg.DefaultWindowSize,
	}, logger.With("component", "azuremonitor"))
	if err != nil {
		logger.Error("failed to construct Azure Monitor client", slog.Any("error", err))
		os.Exit(1)
	}

	if err := alertClient.VerifyActionGroup(bootstrapCtx); err != nil {
		logger.Error("action group verification failed at boot",
			slog.String("actionGroupId", cfg.ActionGroupID),
			slog.Any("error", err),
		)
		os.Exit(1)
	}
	logger.Info("Action Group reachable", slog.String("actionGroupId", cfg.ActionGroupID))

	obsClient := observer.NewClient(cfg.ObserverURL)
	handler := app.NewLogsHandler(laClient, alertClient, obsClient, logger.With("component", "handler"))

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
