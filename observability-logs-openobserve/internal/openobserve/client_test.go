// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestClient(serverURL string) *Client {
	return NewClient(serverURL, "default", "default", "admin", "token", testLogger())
}

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost:5080/", "myorg", "mystream", "user", "pass", testLogger())

	if c.baseURL != "http://localhost:5080" {
		t.Errorf("expected trailing slash removed, got %q", c.baseURL)
	}
	if c.org != "myorg" {
		t.Errorf("unexpected org: %q", c.org)
	}
	if c.stream != "mystream" {
		t.Errorf("unexpected stream: %q", c.stream)
	}
	if c.user != "user" {
		t.Errorf("unexpected user: %q", c.user)
	}
	if c.token != "pass" {
		t.Errorf("unexpected token: %q", c.token)
	}
}

func TestGetComponentLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/default/_search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}

		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "token" {
			t.Error("missing or incorrect basic auth")
		}

		resp := OpenObserveResponse{
			Took:  42,
			Total: 2,
			Hits: []map[string]interface{}{
				{
					"_timestamp":     float64(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixMicro()),
					"log":            "ERROR: something failed",
					"logLevel":       "ERROR",
					"kubernetes_labels_openchoreo_dev_component_uid": "comp-1",
					"kubernetes_labels_openchoreo_dev_component":     "my-comp",
					"kubernetes_labels_openchoreo_dev_namespace":     "test-ns",
					"kubernetes_pod_name":                            "pod-1",
					"kubernetes_namespace_name":                      "k8s-ns",
					"kubernetes_container_name":                      "main",
				},
				{
					"_timestamp": float64(time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC).UnixMicro()),
					"log":        "Info message",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "test-ns",
		StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalCount != 2 {
		t.Errorf("expected total 2, got %d", result.TotalCount)
	}
	if result.Took != 42 {
		t.Errorf("expected took 42, got %d", result.Took)
	}
	if len(result.Logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(result.Logs))
	}

	log0 := result.Logs[0]
	if log0.Log != "ERROR: something failed" {
		t.Errorf("unexpected log: %q", log0.Log)
	}
	if log0.LogLevel != "ERROR" {
		t.Errorf("expected logLevel ERROR, got %q", log0.LogLevel)
	}
	if log0.ComponentUID != "comp-1" {
		t.Errorf("unexpected componentUID: %q", log0.ComponentUID)
	}
	if log0.PodName != "pod-1" {
		t.Errorf("unexpected podName: %q", log0.PodName)
	}

	// Second log has no explicit logLevel, should be extracted from content
	log1 := result.Logs[1]
	if log1.LogLevel != "INFO" {
		t.Errorf("expected extracted logLevel INFO, got %q", log1.LogLevel)
	}
}

func TestGetComponentLogs_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "test-ns",
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestGetWorkflowLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := OpenObserveResponse{
			Took:  10,
			Total: 1,
			Hits: []map[string]interface{}{
				{
					"_timestamp":      float64(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixMicro()),
					"log":             "workflow step completed",
					"kubernetes_node": "node-1",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetWorkflowLogs(context.Background(), WorkflowLogsParams{
		Namespace:       "test-ns",
		WorkflowRunName: "run-1",
		StartTime:       time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:         time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalCount != 1 {
		t.Errorf("expected total 1, got %d", result.TotalCount)
	}
	if len(result.Logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(result.Logs))
	}
	if result.Logs[0].Log != "workflow step completed" {
		t.Errorf("unexpected log: %q", result.Logs[0].Log)
	}
	if result.Logs[0].Metadata["kubernetes_node"] != "node-1" {
		t.Errorf("expected metadata kubernetes_node=node-1, got %v", result.Logs[0].Metadata)
	}
}

func TestCreateAlert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/default/alerts" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "alert-123"})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	enabled := true
	name := "test-alert"
	alertID, err := client.CreateAlert(context.Background(), LogAlertParams{
		Name:           &name,
		Operator:       "gt",
		ThresholdValue: 5,
		Window:         "5m",
		Interval:       "1m",
		Enabled:        &enabled,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alertID != "alert-123" {
		t.Errorf("expected alertID 'alert-123', got %q", alertID)
	}
}

func TestCreateAlert_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	enabled := true
	name := "test-alert"
	_, err := client.CreateAlert(context.Background(), LogAlertParams{
		Name:     &name,
		Operator: "gt",
		Window:   "5m",
		Interval: "1m",
		Enabled:  &enabled,
	})
	if err == nil {
		t.Fatal("expected error for bad request response")
	}
}

func TestDeleteAlert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-456", "name": "my-alert"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.Method == "DELETE" && r.URL.Path == "/api/v2/default/alerts/alert-456":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	alertID, err := client.DeleteAlert(context.Background(), "my-alert")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alertID != "alert-456" {
		t.Errorf("expected alertID 'alert-456', got %q", alertID)
	}
}

func TestDeleteAlert_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"list": []map[string]string{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.DeleteAlert(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent alert")
	}
}

func TestUpdateAlert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-789", "name": "my-alert"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.Method == "PUT" && r.URL.Path == "/api/v2/default/alerts/alert-789":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	enabled := true
	alertID, err := client.UpdateAlert(context.Background(), "my-alert", LogAlertParams{
		Operator: "gt",
		Window:   "5m",
		Interval: "1m",
		Enabled:  &enabled,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alertID != "alert-789" {
		t.Errorf("expected alertID 'alert-789', got %q", alertID)
	}
}

func TestGetAlert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-abc", "name": "my-alert"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts/alert-abc":
			resp := map[string]interface{}{
				"name":    "my-alert",
				"enabled": true,
				"query_condition": map[string]interface{}{
					"sql": "SELECT count(*) as match_count FROM \"default\" WHERE str_match(log, 'error')",
				},
				"trigger_condition": map[string]interface{}{
					"operator":       ">",
					"threshold":      float64(10),
					"period":         float64(5),
					"frequency":      float64(1),
					"frequency_type": "minutes",
				},
				"context_attributes": map[string]interface{}{
					"namespace":      "test-ns",
					"projectUid":     "proj-1",
					"environmentUid": "env-1",
					"componentUid":   "comp-1",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	detail, err := client.GetAlert(context.Background(), "my-alert")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if detail.Name != "my-alert" {
		t.Errorf("expected name 'my-alert', got %q", detail.Name)
	}
	if !detail.Enabled {
		t.Error("expected enabled true")
	}
	if detail.Operator != ">" {
		t.Errorf("expected operator '>', got %q", detail.Operator)
	}
	if detail.Threshold != 10 {
		t.Errorf("expected threshold 10, got %v", detail.Threshold)
	}
	if detail.Period != 5 {
		t.Errorf("expected period 5, got %d", detail.Period)
	}
	if detail.Frequency != 1 {
		t.Errorf("expected frequency 1, got %d", detail.Frequency)
	}
	if detail.FrequencyType != "minutes" {
		t.Errorf("expected frequencyType 'minutes', got %q", detail.FrequencyType)
	}
	if detail.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", detail.Namespace)
	}
	if detail.ProjectUID != "proj-1" {
		t.Errorf("expected projectUID 'proj-1', got %q", detail.ProjectUID)
	}
	if detail.EnvironmentUID != "env-1" {
		t.Errorf("expected environmentUID 'env-1', got %q", detail.EnvironmentUID)
	}
	if detail.ComponentUID != "comp-1" {
		t.Errorf("expected componentUID 'comp-1', got %q", detail.ComponentUID)
	}
}

func TestUpdateAlert_LookupFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"list": []map[string]string{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	enabled := true
	_, err := client.UpdateAlert(context.Background(), "nonexistent", LogAlertParams{
		Operator: "gt",
		Window:   "5m",
		Interval: "1m",
		Enabled:  &enabled,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent alert")
	}
}

func TestUpdateAlert_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-upd-err", "name": "my-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.Method == "PUT":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	enabled := true
	_, err := client.UpdateAlert(context.Background(), "my-alert", LogAlertParams{
		Operator: "gt",
		Window:   "5m",
		Interval: "1m",
		Enabled:  &enabled,
	})
	if err == nil {
		t.Fatal("expected error for bad request response")
	}
}

func TestUpdateAlert_ConfigError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"list": []map[string]string{
				{"alert_id": "alert-cfg", "name": "my-alert"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	enabled := true
	// Invalid operator will cause generateAlertConfig to fail
	_, err := client.UpdateAlert(context.Background(), "my-alert", LogAlertParams{
		Operator: "invalid_op",
		Window:   "5m",
		Interval: "1m",
		Enabled:  &enabled,
	})
	if err == nil {
		t.Fatal("expected error for invalid alert config")
	}
}

func TestDeleteAlert_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-del-err", "name": "my-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.Method == "DELETE":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.DeleteAlert(context.Background(), "my-alert")
	if err == nil {
		t.Fatal("expected error for delete failure")
	}
}

func TestCreateAlert_ConfigError(t *testing.T) {
	client := newTestClient("http://localhost:1")
	enabled := true
	name := "test"
	// Invalid operator causes config generation to fail
	_, err := client.CreateAlert(context.Background(), LogAlertParams{
		Name:     &name,
		Operator: "invalid_op",
		Window:   "5m",
		Interval: "1m",
		Enabled:  &enabled,
	})
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestCreateAlert_MissingResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// Response without "id" field
		w.Write([]byte(`{"status": "ok"}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	enabled := true
	name := "test-alert"
	_, err := client.CreateAlert(context.Background(), LogAlertParams{
		Name:     &name,
		Operator: "gt",
		Window:   "5m",
		Interval: "1m",
		Enabled:  &enabled,
	})
	if err == nil {
		t.Fatal("expected error for missing response ID")
	}
}

func TestCreateAlert_ConnectionError(t *testing.T) {
	client := newTestClient("http://localhost:1")
	enabled := true
	name := "test-alert"
	_, err := client.CreateAlert(context.Background(), LogAlertParams{
		Name:     &name,
		Operator: "gt",
		Window:   "5m",
		Interval: "1m",
		Enabled:  &enabled,
	})
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestExecuteSearchQuery_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad query"))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetWorkflowLogs(context.Background(), WorkflowLogsParams{
		Namespace:       "test-ns",
		WorkflowRunName: "run-1",
		StartTime:       time.Now().Add(-time.Hour),
		EndTime:         time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for non-OK status")
	}
}

func TestExecuteSearchQuery_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not valid json{{{"))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "test-ns",
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON response")
	}
}

func TestGetAlert_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/default/alerts" && r.Method == "GET":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-err", "name": "my-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/v2/default/alerts/alert-err":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetAlert(context.Background(), "my-alert")
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestExtractLogLevel(t *testing.T) {
	tests := []struct {
		log      string
		expected string
	}{
		{"2025-01-01 ERROR: something failed", "ERROR"},
		{"[WARN] potential issue", "WARN"},
		{"WARNING: deprecated function", "WARN"},
		{"DEBUG: verbose output", "DEBUG"},
		{"INFO: service started", "INFO"},
		{"FATAL: cannot continue", "FATAL"},
		{"SEVERE: critical problem", "SEVERE"},
		{"just a regular log message", "INFO"},
		{"UNDEFINED log level", "INFO"},
		{"", "INFO"},
	}
	for _, tt := range tests {
		t.Run(tt.log, func(t *testing.T) {
			got := extractLogLevel(tt.log)
			if got != tt.expected {
				t.Errorf("extractLogLevel(%q) = %q, want %q", tt.log, got, tt.expected)
			}
		})
	}
}

func TestParseWorkflowLogEntry(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC).UnixMicro()
	source := map[string]interface{}{
		"_timestamp":      float64(ts),
		"log":             "step completed",
		"kubernetes_node": "node-1",
		"extra_field":     "extra_value",
	}

	entry := parseWorkflowLogEntry(ts, source)

	if entry.Log != "step completed" {
		t.Errorf("unexpected log: %q", entry.Log)
	}
	if entry.Timestamp != time.UnixMicro(ts) {
		t.Errorf("unexpected timestamp: %v", entry.Timestamp)
	}
	if entry.Metadata["kubernetes_node"] != "node-1" {
		t.Error("expected kubernetes_node in metadata")
	}
	if entry.Metadata["extra_field"] != "extra_value" {
		t.Error("expected extra_field in metadata")
	}
	if _, exists := entry.Metadata["log"]; exists {
		t.Error("log should not be in metadata")
	}
	if _, exists := entry.Metadata["_timestamp"]; exists {
		t.Error("_timestamp should not be in metadata")
	}
}
