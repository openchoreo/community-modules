// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const rawQueryPath = "/api/v1alpha1/traces/raw-query"

func serve(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	// Raw queries are off (no read-only user), so a request that is read
	// in full gets 501; one over the body limit never gets that far.
	server := NewServer("0", NewTracingHandler(nil, testLogger()), testLogger())
	rec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func TestServer_RejectsAnOversizedBody(t *testing.T) {
	oversizedJSON := `"` + strings.Repeat("x", maxRequestBytes) + `"`
	tests := []struct {
		name        string
		contentType string
		body        string
		chunked     bool // no Content-Length, so the limit applies while reading
	}{
		{"JSON with its length declared", "application/json", oversizedJSON, false},
		{"JSON sent chunked", "application/json", oversizedJSON, true},
		{"text sent chunked", "text/plain", strings.Repeat("x", maxRequestBytes+1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, rawQueryPath, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			if tt.chunked {
				req.ContentLength = -1
			}
			if rec := serve(t, req); rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, want 413: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestServer_ReadsABodyWithinTheLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, rawQueryPath, strings.NewReader(`{"size":0}`))
	req.Header.Set("Content-Type", "application/json")
	if rec := serve(t, req); rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501 from the raw query handler: %s", rec.Code, rec.Body)
	}
}

func TestServer_AnUnreadableBodyIsStillABadRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, rawQueryPath, strings.NewReader(`{"size":`))
	req.Header.Set("Content-Type", "application/json")
	if rec := serve(t, req); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}
