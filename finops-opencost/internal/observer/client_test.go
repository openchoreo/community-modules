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

func TestQueryResourceMetrics(t *testing.T) {
	var gotAuth string
	var gotReq MetricsQueryRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v1/metrics/query" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		_, _ = io.WriteString(w, `{
		  "cpuUsage": [{"timestamp": "2026-07-01T00:00:00Z", "value": 0.1}],
		  "cpuRequests": [{"timestamp": "2026-07-01T00:00:00Z", "value": 0.5}],
		  "memoryUsage": [{"timestamp": "2026-07-01T00:00:00Z", "value": 1000}],
		  "memoryRequests": [{"timestamp": "2026-07-01T00:00:00Z", "value": 2000}]
		}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	metrics, err := c.QueryResourceMetrics(
		context.Background(),
		"test-token",
		ComponentSearchScope{Namespace: "default", Environment: "development", Component: "checkout"},
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		"5m",
	)
	if err != nil {
		t.Fatalf("QueryResourceMetrics error: %v", err)
	}

	if gotAuth != "Bearer test-token" {
		t.Errorf("expected Bearer token forwarded, got %q", gotAuth)
	}
	if gotReq.Metric != "resource" {
		t.Errorf("expected metric=resource, got %q", gotReq.Metric)
	}
	if gotReq.SearchScope.Component != "checkout" {
		t.Errorf("expected component in scope, got %q", gotReq.SearchScope.Component)
	}
	if len(metrics.CPUUsage) != 1 || metrics.CPUUsage[0].Value != 0.1 {
		t.Errorf("unexpected cpuUsage: %+v", metrics.CPUUsage)
	}
	if len(metrics.CPURequests) != 1 || metrics.CPURequests[0].Value != 0.5 {
		t.Errorf("unexpected cpuRequests: %+v", metrics.CPURequests)
	}
}

func TestQueryResourceMetricsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "unauthorized")
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.QueryResourceMetrics(context.Background(), "tok", ComponentSearchScope{},
		time.Now(), time.Now().Add(time.Hour), "5m")
	if err == nil {
		t.Fatalf("expected error for non-2xx status")
	}
}

func TestQueryResourceMetricsBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{not json")
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.QueryResourceMetrics(context.Background(), "tok", ComponentSearchScope{},
		time.Now(), time.Now().Add(time.Hour), "5m")
	if err == nil {
		t.Fatalf("expected error for bad JSON")
	}
}

func TestQueryResourceMetricsRequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := NewClient(url)
	_, err := c.QueryResourceMetrics(context.Background(), "tok", ComponentSearchScope{},
		time.Now(), time.Now().Add(time.Hour), "5m")
	if err == nil {
		t.Fatalf("expected transport error against closed server")
	}
}

func TestNewClientTrimsTrailingSlash(t *testing.T) {
	c := NewClient("http://observer:8080/")
	if c.baseURL != "http://observer:8080" {
		t.Errorf("baseURL = %q, want trimmed", c.baseURL)
	}
}
