// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/api/gen"
)

type Server struct {
	port       string
	httpServer *http.Server
	listener   net.Listener
	logger     *slog.Logger
}

func NewServer(port string, metricsHandler *MetricsHandler, logger *slog.Logger) *Server {
	strictHandler := gen.NewStrictHandler(metricsHandler, nil)

	mux := http.NewServeMux()
	handler := gen.HandlerFromMux(strictHandler, mux)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return &Server{
		port:       port,
		httpServer: httpServer,
		logger:     logger,
	}
}

func (s *Server) Start() error {
	s.logger.Info("Starting server", slog.String("port", s.port))

	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	s.listener = listener

	if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

// Addr returns the actual network address the server is listening on.
// This is useful when the server is started with port "0" and the OS assigns a free port.
// Returns empty string if the server hasn't been started yet.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down server")
	return s.httpServer.Shutdown(ctx)
}
