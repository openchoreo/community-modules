// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
)

// Server wraps the HTTP server.
type Server struct {
	port       string
	httpServer *http.Server
	logger     *slog.Logger
}

// NewServer creates a new HTTP server with the generated handler.
func NewServer(port string, logsHandler *LogsHandler, logger *slog.Logger) *Server {
	strictHandler := gen.NewStrictHandler(logsHandler, nil)

	mux := http.NewServeMux()
	handler := gen.HandlerFromMux(strictHandler, mux)

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		port:       port,
		httpServer: httpServer,
		logger:     logger,
	}
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	s.logger.Info("Starting server", slog.String("port", s.port))
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down server")
	return s.httpServer.Shutdown(ctx)
}
