// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/api/gen"
)

// Server wraps http.Server with the adapter's logger.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// Middleware is the standard chainable http middleware type.
type Middleware func(http.Handler) http.Handler

// NewServer constructs an HTTP server that mounts the strict server interface
// produced by oapi-codegen. Extra middlewares are applied in order (last wraps
// first) before the access-log middleware.
func NewServer(port string, handler gen.StrictServerInterface, logger *slog.Logger, extraMiddlewares ...Middleware) *Server {
	strictHandler := gen.NewStrictHandler(handler, nil)

	mux := http.NewServeMux()
	gen.HandlerFromMux(strictHandler, mux)

	var h http.Handler = mux
	for _, mw := range extraMiddlewares {
		if mw != nil {
			h = mw(h)
		}
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           accessLogMiddleware(h, logger),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return &Server{httpServer: srv, logger: logger}
}

// Start blocks until the server stops. Returns nil on graceful shutdown, or
// the error that caused the listener to exit.
func (s *Server) Start() error {
	s.logger.Info("server starting", slog.String("addr", s.httpServer.Addr))
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// accessLogMiddleware logs each request with method, path, status, and duration.
func accessLogMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		level := slog.LevelInfo
		if rw.status >= 500 {
			level = slog.LevelError
		} else if rw.status >= 400 {
			level = slog.LevelWarn
		}

		logger.Log(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
