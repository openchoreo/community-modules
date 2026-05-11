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

	app "github.com/openchoreo/community-modules/observability-tracing-cloudwatch/internal"
	"github.com/openchoreo/community-modules/observability-tracing-cloudwatch/internal/xray"
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
		slog.String("serverPort", cfg.ServerPort),
	)

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer bootstrapCancel()

	xrayClient, err := xray.NewClient(bootstrapCtx, xray.Config{
		Region: cfg.AWSRegion,
	}, logger)
	if err != nil {
		logger.Error("Failed to initialise AWS X-Ray client", slog.Any("error", err))
		os.Exit(1)
	}

	if err := xrayClient.Ping(bootstrapCtx); err != nil {
		logger.Error("Failed to verify AWS credentials via sts:GetCallerIdentity", slog.Any("error", err))
		os.Exit(1)
	}
	logger.Info("AWS credentials verified successfully")

	tracingHandler := app.NewTracingHandler(xrayClient, logger)
	srv := app.NewServer(cfg.ServerPort, tracingHandler, logger)

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
