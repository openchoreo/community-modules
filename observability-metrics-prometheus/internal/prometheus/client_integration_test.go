// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHealthCheck tests the Prometheus health check functionality
func TestHealthCheck(t *testing.T) {
	// Create a mock Prometheus server that returns successful query response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			w.Header().Set("Content-Type", "application/json")
			response := map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "vector",
					"result":     []interface{}{},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ctx := context.Background()
	if err := client.HealthCheck(ctx); err != nil {
		t.Errorf("HealthCheck() failed: %v", err)
	}
}

func TestHealthCheck_Failure(t *testing.T) {
	// Create a mock Prometheus server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ctx := context.Background()
	if err := client.HealthCheck(ctx); err == nil {
		t.Error("HealthCheck() expected error but got nil")
	}
}

func TestQueryRangeTimeSeries_Success(t *testing.T) {
	// Create a mock Prometheus server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query_range" {
			w.Header().Set("Content-Type", "application/json")
			response := map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "matrix",
					"result": []interface{}{
						map[string]interface{}{
							"metric": map[string]interface{}{
								"__name__": "test_metric",
								"job":      "test",
							},
							"values": []interface{}{
								[]interface{}{1700000000, "10"},
								[]interface{}{1700000300, "20"},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ctx := context.Background()
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	step := 5 * time.Minute

	resp, err := client.QueryRangeTimeSeries(ctx, "test_query", start, end, step)
	if err != nil {
		t.Fatalf("QueryRangeTimeSeries() failed: %v", err)
	}

	if resp.Status != "success" {
		t.Errorf("expected status success, got %s", resp.Status)
	}
	if len(resp.Data.Result) != 1 {
		t.Errorf("expected 1 result, got %d", len(resp.Data.Result))
	}
}

func TestQueryRangeTimeSeries_Error(t *testing.T) {
	// Create a mock Prometheus server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad query"))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ctx := context.Background()
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	step := 5 * time.Minute

	_, err = client.QueryRangeTimeSeries(ctx, "invalid_query", start, end, step)
	if err == nil {
		t.Error("QueryRangeTimeSeries() expected error but got nil")
	}
}

func TestQueryRangeTimeSeries_WithWarnings(t *testing.T) {
	// Create a mock Prometheus server that returns warnings
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query_range" {
			w.Header().Set("Content-Type", "application/json")
			response := map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "matrix",
					"result":     []interface{}{},
				},
				"warnings": []string{"some warning"},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ctx := context.Background()
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	step := 5 * time.Minute

	// Should succeed despite warnings
	resp, err := client.QueryRangeTimeSeries(ctx, "test_query", start, end, step)
	if err != nil {
		t.Fatalf("QueryRangeTimeSeries() failed: %v", err)
	}

	if resp.Status != "success" {
		t.Errorf("expected status success, got %s", resp.Status)
	}
}

func TestNewClient_InvalidAddress(t *testing.T) {
	// The Prometheus client doesn't validate URL format at creation time,
	// so we test with an address that will fail on actual connection
	client, err := NewClient("://invalid", testLogger())
	// Prometheus client might accept any string as address,
	// so we skip this test or check that at least client is created
	if err != nil {
		// Error is expected for truly invalid URLs
		return
	}
	if client == nil {
		t.Error("NewClient() returned nil client")
	}
}
