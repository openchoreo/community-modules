// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal/api/gen"
)

// TestQueryRawTraces_NotSupported sends raw queries through the server's routes
// and expects a 501 for each, whatever the body.
func TestQueryRawTraces_NotSupported(t *testing.T) {
	srv := NewServer("0", NewTracingHandler(nil, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler))

	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"JSON body", "application/json", `{"query":"anything"}`},
		{"text body", "text/plain", "anything"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1alpha1/traces/raw-query", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; body: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.Bytes()
			var got gen.ErrorResponse
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("the 501 body is not an error response: %v; body: %s", err, body)
			}
			if got.Title == nil || *got.Title != gen.NotImplemented {
				t.Errorf("title = %v, want %s", got.Title, gen.NotImplemented)
			}
			if got.Detail == nil || *got.Detail != rawTracesNotSupported {
				t.Errorf("detail = %v, want %q", got.Detail, rawTracesNotSupported)
			}
		})
	}
}
