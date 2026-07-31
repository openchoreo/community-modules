// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/gen"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/internal/config"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/internal/envresolver"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/internal/handler"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/internal/search"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Error("failed to load configuration", slog.Any("error", err))
		os.Exit(1)
	}

	// Load environment UID-to-name mapping from environments API at startup.
	envResolver := envresolver.New(logger)
	if cfg.OAuthClientID != "" && cfg.OAuthClientSecret != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := envResolver.LoadFromAPI(ctx, cfg.EnvAPIBaseURL, cfg.OAuthTokenURL, cfg.OAuthClientID, cfg.OAuthClientSecret); err != nil {
			logger.Warn("failed to load environments from API, continuing without environment resolution", slog.Any("error", err))
		}
	} else {
		logger.Warn("OAUTH_CLIENT_ID or OAUTH_CLIENT_SECRET not set, skipping environment resolution")
	}

	searchClient := search.NewClient(cfg, logger)
	searchClient.SetEnvResolver(envResolver)

	r := gin.Default()
	gen.RegisterHandlers(r, handler.New(searchClient))

	logger.Info("starting server", slog.String("port", cfg.ServerPort))
	if err := r.Run(":" + cfg.ServerPort); err != nil {
		logger.Error("server exited", slog.Any("error", err))
		os.Exit(1)
	}
}
