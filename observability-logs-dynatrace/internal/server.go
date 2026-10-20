// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/api/gen"
)

// Server serves the logging adapter API.
type Server struct {
	port       string
	httpServer *http.Server
	logger     *slog.Logger
}

// writeTimeout bounds a response by the slowest request path: events run the page and count
// queries in parallel and then the tie extension, so two query timeouts back to back, plus a
// margin for encoding and the network.
func writeTimeout(queryTimeout time.Duration) time.Duration {
	return 2*queryTimeout + 15*time.Second
}

// NewServer wires the handler into the generated router.
func NewServer(port string, queryTimeout time.Duration, logsHandler *LogsHandler, logger *slog.Logger) *Server {
	strictHandler := gen.NewStrictHandler(logsHandler, nil)

	mux := http.NewServeMux()
	handler := gen.HandlerFromMux(strictHandler, mux)

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: writeTimeout(queryTimeout),
		IdleTimeout:  60 * time.Second,
	}

	return &Server{port: port, httpServer: httpServer, logger: logger}
}

// Handler exposes the router, for tests.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start serves until Shutdown is called.
func (s *Server) Start() error {
	s.logger.Info("Starting server", slog.String("port", s.port))
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down server")
	return s.httpServer.Shutdown(ctx)
}
