// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/openchoreo/community-modules/finops-opencost/internal/api/gen"
)

type contextKey string

const bearerTokenKey contextKey = "bearerToken"

// writeTimeout bounds a single request. Upstream OpenCost/Observer calls for
// wide, finely-bucketed windows can take well over a minute.
const writeTimeout = 150 * time.Second

// ShutdownTimeout is the graceful-shutdown grace period. It gives an in-flight
// request a full writeTimeout to drain during a rolling restart, plus a small
// buffer, so long-running upstream calls are not cut off mid-flight.
const ShutdownTimeout = writeTimeout + 15*time.Second

type Server struct {
	port       string
	httpServer *http.Server
	logger     *slog.Logger
}

func NewServer(port string, handler *CostHandler, logger *slog.Logger) *Server {
	strictHandler := gen.NewStrictHandler(handler, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /liveness", healthOK)
	mux.HandleFunc("GET /readiness", healthOK)
	apiHandler := gen.HandlerFromMux(strictHandler, mux)

	httpServer := &http.Server{
		Addr:        ":" + port,
		Handler:     withBearerToken(apiHandler),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: writeTimeout,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		port:       port,
		httpServer: httpServer,
		logger:     logger,
	}
}

// withBearerToken adds the Authorization bearer token into the request context
// so handlers can forward it to the Observer. The generated strict server does
// not surface request headers to handlers otherwise.
func withBearerToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			if token != "" {
				r = r.WithContext(context.WithValue(r.Context(), bearerTokenKey, token))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func healthOK(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func bearerTokenFromContext(ctx context.Context) string {
	if token, ok := ctx.Value(bearerTokenKey).(string); ok {
		return token
	}
	return ""
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
