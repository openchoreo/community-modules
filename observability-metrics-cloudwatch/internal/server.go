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

	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/auth"
)

type Server struct {
	port       string
	httpServer *http.Server
	logger     *slog.Logger
}

func NewServer(port string, h *MetricsHandler, webhookSecret string, webhookAuthEnabled bool, logger *slog.Logger) *Server {
	strictHandler := gen.NewStrictHandler(h, nil)
	mux := http.NewServeMux()
	handler := gen.HandlerFromMux(strictHandler, mux)

	// /livez is a process-up check that never touches AWS — used by the
	// Kubernetes liveness probe so transient AWS hiccups can't crash-loop the pod.
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"alive"}`))
	})

	handler = auth.WebhookAuthMiddleware(webhookSecret, webhookAuthEnabled, logger, nil)(handler)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return &Server{port: port, httpServer: httpServer, logger: logger}
}

func (s *Server) Start() error {
	s.logger.Info("Starting server", slog.String("port", s.port))
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

func (s *Server) Serve(listener net.Listener) error {
	s.logger.Info("Starting server", slog.String("addr", listener.Addr().String()))
	if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down server")
	return s.httpServer.Shutdown(ctx)
}
