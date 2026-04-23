// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/prometheus"
)

// createMockPrometheusServer creates a test HTTP server that mocks Prometheus API
func createMockPrometheusServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/api/v1/query" {
			// Health check endpoint
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

		if r.URL.Path == "/api/v1/query_range" {
			// Query range endpoint
			response := map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "matrix",
					"result": []interface{}{
						map[string]interface{}{
							"metric": map[string]interface{}{
								"__name__": "test_metric",
							},
							"values": []interface{}{
								[]interface{}{float64(time.Now().Unix()), "42.5"},
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
}

func TestHealth_WithPrometheusClient(t *testing.T) {
	mockServer := createMockPrometheusServer()
	defer mockServer.Close()

	promClient, err := prometheus.NewClient(mockServer.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create prometheus client: %v", err)
	}

	handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())

	resp, err := handler.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	healthResp, ok := resp.(gen.Health200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}

	if healthResp.Status == nil || *healthResp.Status != "healthy" {
		t.Errorf("expected status healthy, got %v", healthResp.Status)
	}
}

func TestHealth_PrometheusDown(t *testing.T) {
	// Create a server that always returns errors
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer mockServer.Close()

	promClient, err := prometheus.NewClient(mockServer.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create prometheus client: %v", err)
	}

	handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())

	resp, err := handler.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	healthResp, ok := resp.(gen.Health503JSONResponse)
	if !ok {
		t.Fatalf("expected 503 response, got %T", resp)
	}

	if healthResp.Status == nil || *healthResp.Status != "unhealthy" {
		t.Errorf("expected status unhealthy, got %v", healthResp.Status)
	}
}

func TestQueryResourceMetrics_Success(t *testing.T) {
	mockServer := createMockPrometheusServer()
	defer mockServer.Close()

	promClient, err := prometheus.NewClient(mockServer.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create prometheus client: %v", err)
	}

	handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())

	body := gen.MetricsQueryRequest{
		Metric:    gen.Resource,
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		SearchScope: gen.ComponentSearchScope{
			Namespace: "test-namespace",
		},
	}

	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should get a successful response
	if _, ok := resp.(metricsQueryOKResponse); !ok {
		t.Fatalf("expected OK response, got %T", resp)
	}
}

func TestQueryHTTPMetrics_Success(t *testing.T) {
	mockServer := createMockPrometheusServer()
	defer mockServer.Close()

	promClient, err := prometheus.NewClient(mockServer.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create prometheus client: %v", err)
	}

	handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())

	body := gen.MetricsQueryRequest{
		Metric:    gen.Http,
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		SearchScope: gen.ComponentSearchScope{
			Namespace: "test-namespace",
		},
	}

	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should get a successful response
	if _, ok := resp.(metricsQueryOKResponse); !ok {
		t.Fatalf("expected OK response, got %T", resp)
	}
}

func TestQueryMetrics_UnknownMetricType(t *testing.T) {
	mockServer := createMockPrometheusServer()
	defer mockServer.Close()

	promClient, err := prometheus.NewClient(mockServer.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create prometheus client: %v", err)
	}

	handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())

	body := gen.MetricsQueryRequest{
		Metric:    "unknown",
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		SearchScope: gen.ComponentSearchScope{
			Namespace: "test-namespace",
		},
	}

	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should get a bad request response
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400 response for unknown metric type, got %T", resp)
	}
}

func TestQueryMetrics_PrometheusError(t *testing.T) {
	// Create a server that returns errors
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	promClient, err := prometheus.NewClient(mockServer.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create prometheus client: %v", err)
	}

	handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())

	body := gen.MetricsQueryRequest{
		Metric:    gen.Resource,
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		SearchScope: gen.ComponentSearchScope{
			Namespace: "test-namespace",
		},
	}

	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should get a server error response due to Prometheus failure
	if _, ok := resp.(gen.QueryMetrics500JSONResponse); !ok {
		t.Fatalf("expected 500 response for Prometheus error, got %T", resp)
	}
}

func TestQueryMetrics_WithCustomStep(t *testing.T) {
	// Variable to capture the step parameter from the HTTP request
	var capturedStep string

	// Create a custom mock server that captures the step parameter
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/api/v1/query_range" {
			// Parse query parameters from the request
			if err := r.ParseForm(); err != nil {
				t.Fatalf("failed to parse form: %v", err)
			}
			// Capture the step parameter
			capturedStep = r.FormValue("step")

			// Return a valid Prometheus response
			response := map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"resultType": "matrix",
					"result": []interface{}{
						map[string]interface{}{
							"metric": map[string]interface{}{
								"__name__": "test_metric",
							},
							"values": []interface{}{
								[]interface{}{float64(time.Now().Unix()), "42.5"},
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
	defer mockServer.Close()

	promClient, err := prometheus.NewClient(mockServer.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create prometheus client: %v", err)
	}

	handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())

	step := "10m"
	body := gen.MetricsQueryRequest{
		Metric:    gen.Resource,
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		Step:      &step,
		SearchScope: gen.ComponentSearchScope{
			Namespace: "test-namespace",
		},
	}

	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should get a successful response
	if _, ok := resp.(metricsQueryOKResponse); !ok {
		t.Fatalf("expected OK response, got %T", resp)
	}

	// Verify that the step parameter was sent to Prometheus as seconds
	// 10m = 600 seconds
	expectedStep := "600"
	if capturedStep != expectedStep {
		t.Fatalf("expected step parameter to be %q (600 seconds for 10m), got %q", expectedStep, capturedStep)
	}
}

func TestVisitQueryMetricsResponse(t *testing.T) {
	// Create a response wrapper
	response := metricsQueryOKResponse{
		gen.MetricsQueryResponse{},
	}

	// Create a test response writer
	w := httptest.NewRecorder()

	// Call the visit method
	err := response.VisitQueryMetricsResponse(w)
	if err != nil {
		t.Errorf("VisitQueryMetricsResponse() failed: %v", err)
	}

	// Check that the response was written
	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}
