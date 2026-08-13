// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/gen"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/internal/config"
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

	searchClient := search.NewClient(cfg, logger)

	r := gin.Default()
	gen.RegisterHandlers(r, handler.New(searchClient))

	logger.Info("starting server", slog.String("port", cfg.ServerPort))
	if err := r.Run(":" + cfg.ServerPort); err != nil {
		logger.Error("server exited", slog.Any("error", err))
		os.Exit(1)
	}
}
