// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer() *Server {
	h := newTestHandler("http://unused", "http://unused")
	return NewServer("0", h, discardLogger())
}

func TestHealthEndpoints(t *testing.T) {
	srv := newTestServer()
	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	for _, path := range []string{"/liveness", "/readiness"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s error: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestWithBearerToken(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"valid bearer", "Bearer secret-token", "secret-token"},
		{"no header", "", ""},
		{"non-bearer scheme", "Basic abc", ""},
		{"empty token", "Bearer ", ""},
		{"bearer with spaces trimmed", "Bearer   tok  ", "tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			handler := withBearerToken(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = bearerTokenFromContext(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Errorf("token = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBearerTokenFromContextEmpty(t *testing.T) {
	if got := bearerTokenFromContext(context.Background()); got != "" {
		t.Errorf("expected empty token, got %q", got)
	}
}

func TestServerStartAndShutdown(t *testing.T) {
	// Bind to an ephemeral port and rewrite the server address so Start does not
	// collide with a fixed port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	h := newTestHandler("http://unused", "http://unused")
	srv := NewServer("0", h, discardLogger())
	srv.httpServer.Addr = addr

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Poll until the server accepts connections.
	var up bool
	for i := 0; i < 100; i++ {
		conn, derr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if derr == nil {
			conn.Close()
			up = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !up {
		t.Fatalf("server did not come up on %s", addr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown error: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}
