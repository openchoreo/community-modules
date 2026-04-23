// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestNewServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := newTestHandler("")

	server := NewServer("8080", handler, logger)
	if server == nil {
		t.Fatal("expected non-nil server")
	}
	if server.port != "8080" {
		t.Errorf("expected port 8080, got %s", server.port)
	}
	if server.httpServer == nil {
		t.Fatal("expected non-nil httpServer")
	}
	if server.httpServer.Addr != ":8080" {
		t.Errorf("expected addr :8080, got %s", server.httpServer.Addr)
	}
	if server.httpServer.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("expected ReadHeaderTimeout 10s, got %v", server.httpServer.ReadHeaderTimeout)
	}
	if server.httpServer.ReadTimeout != 15*time.Second {
		t.Errorf("expected ReadTimeout 15s, got %v", server.httpServer.ReadTimeout)
	}
	if server.httpServer.WriteTimeout != 15*time.Second {
		t.Errorf("expected WriteTimeout 15s, got %v", server.httpServer.WriteTimeout)
	}
	if server.httpServer.IdleTimeout != 60*time.Second {
		t.Errorf("expected IdleTimeout 60s, got %v", server.httpServer.IdleTimeout)
	}
}

func TestServerStartAndShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := newTestHandler("")

	// Use port "0" to let OS assign a free port
	server := NewServer("0", handler, logger)

	// Start the server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()

	// Poll the health endpoint with a deadline-based retry loop
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		addr := server.Addr()
		if addr == "" {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("failed to connect to server after retries: %v", lastErr)
	}

	// Shutdown the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("shutdown error: %v", err)
	}

	// Wait for Start() to return
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("Start() returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Start() did not return after shutdown")
	}
}

func TestServerShutdownWithoutStart(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := newTestHandler("")

	server := NewServer("8081", handler, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown should work even if server was never started
	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("shutdown error: %v", err)
	}
}
