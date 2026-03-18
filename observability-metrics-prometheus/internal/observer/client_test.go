// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package observer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost:8080/")
	if c.baseURL != "http://localhost:8080" {
		t.Errorf("expected trailing slash removed, got %q", c.baseURL)
	}
}

func TestNewClient_NoTrailingSlash(t *testing.T) {
	c := NewClient("http://localhost:8080")
	if c.baseURL != "http://localhost:8080" {
		t.Errorf("expected baseURL unchanged, got %q", c.baseURL)
	}
}

func TestForwardAlert_Success(t *testing.T) {
	alertTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1alpha1/alerts/webhook" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("failed to read body"))
			return
		}

		var payload alertWebhookRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("failed to unmarshal body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("failed to unmarshal body"))
			return
		}

		if payload.RuleName != "my-rule" {
			t.Errorf("expected ruleName 'my-rule', got %q", payload.RuleName)
		}
		if payload.RuleNamespace != "test-ns" {
			t.Errorf("expected ruleNamespace 'test-ns', got %q", payload.RuleNamespace)
		}
		if payload.AlertValue != 42.5 {
			t.Errorf("expected alertValue 42.5, got %v", payload.AlertValue)
		}
		if !payload.AlertTimestamp.Equal(alertTime) {
			t.Errorf("expected alertTimestamp %v, got %v", alertTime, payload.AlertTimestamp)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.ForwardAlert(context.Background(), "my-rule", "test-ns", 42.5, alertTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestForwardAlert_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.ForwardAlert(context.Background(), "my-rule", "test-ns", 1, time.Now())
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestForwardAlert_ConnectionError(t *testing.T) {
	client := NewClient("http://localhost:1") // unreachable port
	err := client.ForwardAlert(context.Background(), "my-rule", "test-ns", 1, time.Now())
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestForwardAlert_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.ForwardAlert(ctx, "my-rule", "test-ns", 1, time.Now())
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestForwardAlert_NonSuccessStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"bad request", http.StatusBadRequest, "bad request"},
		{"not found", http.StatusNotFound, "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := NewClient(server.URL)
			err := client.ForwardAlert(context.Background(), "my-rule", "test-ns", 1, time.Now())
			if err == nil {
				t.Fatalf("expected error for status %d", tt.statusCode)
			}
		})
	}
}

func TestForwardAlert_EmptyResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.ForwardAlert(context.Background(), "my-rule", "test-ns", 1, time.Now())
	if err == nil {
		t.Fatal("expected error for 500 with empty body")
	}
}
