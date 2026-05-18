// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	h := newTestHandler(&stubMetricsClient{}, nil)
	srv := NewServer("0", h, "", false, discardLogger())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve() returned %v", err)
		}
	})
	return srv, "http://" + listener.Addr().String()
}

func TestServerLivezReturns200(t *testing.T) {
	_, base := newTestServer(t)

	resp, err := http.Get(base + "/livez")
	if err != nil {
		t.Fatalf("GET /livez error = %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"alive"`) {
		t.Fatalf("body = %s, want alive marker", body)
	}
}

func TestServerHealthEndpointWired(t *testing.T) {
	_, base := newTestServer(t)

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestServerShutdownStopsListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	addr := listener.Addr().String()
	srv := NewServer("0", newTestHandler(&stubMetricsClient{}, nil), "", false, discardLogger())

	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()

	// Wait until the server is actually accepting before shutting it down.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not return after Shutdown")
	}
	if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		t.Fatalf("expected listener at %s to be closed", addr)
	}
}

func TestServerStartFailsOnInvalidPort(t *testing.T) {
	srv := NewServer("not-a-port", newTestHandler(&stubMetricsClient{}, nil), "", false, discardLogger())
	if err := srv.Start(); err == nil {
		t.Fatal("expected Start() to fail on invalid port")
	}
}
