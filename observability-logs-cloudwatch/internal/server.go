// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/auth"
)

type Server struct {
	port       string
	httpServer *http.Server
	logger     *slog.Logger
}

func NewServer(port string, logsHandler *LogsHandler, webhookSecret string, webhookAuthEnabled bool, logger *slog.Logger) *Server {
	strictHandler := gen.NewStrictHandler(logsHandler, nil)

	mux := http.NewServeMux()
	handler := gen.HandlerFromMux(strictHandler, mux)

	// The OpenAPI spec publishes the health endpoint at /health, but OpenChoreo
	// convention (and the original module) expose /healthz as well. Register
	// both so either probe configuration works.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/health"
		handler.ServeHTTP(w, r2)
	})

	// /livez is a cheap process-up check for the Kubernetes liveness probe.
	// Unlike /healthz it never touches AWS, so a transient DNS / STS hiccup
	// cannot crash-loop the pod.
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"alive"}`))
	})
	handler = auth.WebhookAuthMiddleware(webhookSecret, webhookAuthEnabled, logger, nil)(handler)

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		port:       port,
		httpServer: httpServer,
		logger:     logger,
	}
}

func (s *Server) Start() error {
	s.logger.Info("Starting server", slog.String("port", s.port))
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down server")
	return s.httpServer.Shutdown(ctx)
}
