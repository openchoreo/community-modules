// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/api/gen"
)

type Server struct {
	port       string
	httpServer *http.Server
	logger     *slog.Logger
}

// maxRequestBytes bounds every request body before the generated handlers read
// it. The Observer already limits raw queries (1 MiB by default), but the
// adapter is reachable from inside the cluster, so it doesn't rely on that.
// It is set well above any trace query, only to keep an oversized body from
// exhausting the adapter's memory.
const maxRequestBytes = 10 << 20

func NewServer(port string, tracingHandler *TracingHandler, logger *slog.Logger) *Server {
	strictHandler := gen.NewStrictHandlerWithOptions(tracingHandler, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: requestError,
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		},
	})

	mux := http.NewServeMux()
	handler := limitRequestBody(gen.HandlerFromMux(strictHandler, mux), maxRequestBytes)

	// A raw query may run up to its own timeout, so the write timeout leaves
	// room for it.
	writeTimeout := 15 * time.Second
	if rawWrite := tracingHandler.rawQuery.Timeout + rawQueryGrace + 5*time.Second; rawWrite > writeTimeout {
		writeTimeout = rawWrite
	}

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
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

// limitRequestBody answers 413 for a body declared larger than limit, and caps
// the rest, so a body that turns out larger fails while it is read.
func limitRequestBody(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limit {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// requestError answers a request the generated handlers could not read: 413
// when its body is over the limit, otherwise 400, as the generated default
// does.
func requestError(w http.ResponseWriter, _ *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
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
