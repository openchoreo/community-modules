// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewServerExposesHealthAndLivezProbes(t *testing.T) {
	handler := NewLogsHandlerWithOptions(&stubLogsClient{}, HandlerOptions{}, discardLogger())
	srv := NewServer("0", handler, "", false, discardLogger())

	rec := newTestResponseRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, mustRequest(http.MethodGet, "/livez", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("/livez status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body, `"status":"alive"`) {
		t.Fatalf("/livez body = %q", rec.Body)
	}

	rec = newTestResponseRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, mustRequest(http.MethodGet, "/healthz", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body, "healthy") {
		t.Fatalf("/healthz body = %q", rec.Body)
	}
}

func TestServerStartAndShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	handler := NewLogsHandlerWithOptions(&stubLogsClient{}, HandlerOptions{}, discardLogger())
	srv := NewServer(itoa(port), handler, "", false, discardLogger())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Poll until the server starts accepting connections.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+itoa(port), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := http.Get("http://127.0.0.1:" + itoa(port) + "/livez")
	if err != nil {
		t.Fatalf("GET /livez: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/livez status = %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Shutdown")
	}
}

// --- minimal helpers to avoid pulling httptest into our package -----------

type testRecorder struct {
	Code int
	Body string
}

func newTestResponseRecorder() *testRecorder {
	return &testRecorder{Code: http.StatusOK}
}

func (r *testRecorder) Header() http.Header { return http.Header{} }

func (r *testRecorder) Write(p []byte) (int, error) {
	r.Body += string(p)
	return len(p), nil
}

func (r *testRecorder) WriteHeader(code int) { r.Code = code }

func mustRequest(method, target, body string) *http.Request {
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	return req
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
