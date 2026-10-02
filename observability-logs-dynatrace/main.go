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

	app "github.com/openchoreo/community-modules/observability-logs-dynatrace/internal"
	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/dynatrace"
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
		slog.String("platformUrl", cfg.PlatformURL),
		slog.String("authMode", cfg.AuthMode),
		slog.String("containerLogsSource", cfg.ContainerLogsSource),
		slog.String("auditLogsSource", cfg.AuditLogsSource),
		slog.String("auditBucket", cfg.AuditBucket),
		slog.String("queryTimeout", cfg.QueryTimeout.String()),
		slog.String("serverPort", cfg.ServerPort),
	)

	var tokens dynatrace.TokenSource
	if cfg.AuthMode == app.AuthModeOAuth {
		tokens = dynatrace.NewOAuthTokenSource(cfg.OAuth, nil)
	} else {
		tokens = dynatrace.StaticToken(cfg.PlatformToken)
	}

	if cfg.AllowInsecureHTTP {
		logger.Warn("DT_ALLOW_INSECURE_HTTP is set: credentials may be sent over plain http. Use this for test doubles only")
	}

	client := dynatrace.NewClient(dynatrace.Config{
		PlatformURL:         cfg.PlatformURL,
		ContainerLogsSource: cfg.ContainerLogsSource,
		AuditLogsSource:     cfg.AuditLogsSource,
		AuditBucket:         cfg.AuditBucket,
		QueryTimeout:        cfg.QueryTimeout,
	}, tokens, nil, logger)

	// Verify the token and its scopes up-front so the pod crashes and restarts rather than
	// silently serving failing queries.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = client.Ping(pingCtx)
	pingCancel()
	if err != nil {
		logger.Error("Failed to query Dynatrace Grail; check DT_PLATFORM_URL, the credentials and their storage:logs:read / storage:buckets:read scopes",
			slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("Connected to Dynatrace Grail")

	srv := app.NewServer(cfg.ServerPort, cfg.QueryTimeout, app.NewLogsHandler(client, logger), logger)

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
