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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	app "github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/auth"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/azuremonitor"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/observer"
)

// staticTokenCredential satisfies azcore.TokenCredential using a pre-fetched
// bearer token. Dev/testing only — tokens expire after ~1 hour.
type staticTokenCredential struct{ token string }

func (s *staticTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: s.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

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
		slog.Bool("alertsEnabled", cfg.AlertsEnabled),
	)

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer bootstrapCancel()

	// Log Analytics queries need a token with audience https://api.loganalytics.io.
	// ARM operations (scheduledQueryRules, actionGroups) need a token with audience
	// https://management.azure.com. Azure access tokens are audience-scoped, so the
	// static-token path needs two separate tokens. When both env vars are unset, we
	// fall back to DefaultAzureCredential which handles audience switching itself.
	var laCred, armCred azcore.TokenCredential
	staticLA := os.Getenv("AZURE_STATIC_TOKEN")
	staticARM := os.Getenv("AZURE_STATIC_TOKEN_ARM")
	if staticLA != "" || staticARM != "" {
		logger.Warn("using static bearer token(s) — dev only, tokens expire in ~1h",
			slog.Bool("la", staticLA != ""),
			slog.Bool("arm", staticARM != ""),
		)
		if staticLA != "" {
			laCred = &staticTokenCredential{token: staticLA}
		}
		if staticARM != "" {
			armCred = &staticTokenCredential{token: staticARM}
		}
	}
	if laCred == nil || armCred == nil {
		def, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			logger.Error("failed to construct DefaultAzureCredential", slog.Any("error", err))
			os.Exit(1)
		}
		if laCred == nil {
			laCred = def
		}
		if armCred == nil {
			armCred = def
		}
	}

	laClient, err := loganalytics.NewClient(laCred, loganalytics.Config{
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

	var (
		handler          *app.LogsHandler
		extraMiddlewares []app.Middleware
	)

	if cfg.AlertsEnabled {
		alertClient, err := azuremonitor.NewClient(armCred, azuremonitor.Config{
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
		handler = app.NewLogsHandlerWithAlerts(laClient, alertClient, obsClient, logger.With("component", "handler"))

		extraMiddlewares = append(extraMiddlewares, app.Middleware(
			auth.WebhookAuthMiddleware(cfg.WebhookSharedSecret, cfg.WebhookAuthEnabled, logger.With("component", "webhook-auth")),
		))
	} else {
		handler = app.NewLogsHandler(laClient, logger.With("component", "handler"))
	}

	srv := app.NewServer(cfg.ServerPort, handler, logger.With("component", "server"), extraMiddlewares...)

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
