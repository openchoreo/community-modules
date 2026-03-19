// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/observer"
	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/openobserve"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestHealth(t *testing.T) {
	handler := NewLogsHandler(nil, nil, testLogger())
	resp, err := handler.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	healthResp, ok := resp.(gen.Health200JSONResponse)
	if !ok {
		t.Fatalf("unexpected response type: %T", resp)
	}
	if healthResp.Status == nil || *healthResp.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %v", healthResp.Status)
	}
}

func TestQueryLogs_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, testLogger())
	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryLogs400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestCreateAlertRule_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, testLogger())
	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestUpdateAlertRule_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, testLogger())
	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "test",
		Body:     nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertWebhook_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, testLogger())
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	webhookResp, ok := resp.(gen.HandleAlertWebhook200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if webhookResp.Status == nil || *webhookResp.Status != gen.Success {
		t.Error("expected status Success")
	}
}

func TestParseAlertWebhookBody(t *testing.T) {
	t.Run("valid body with float count and microsecond timestamp", func(t *testing.T) {
		ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		body := map[string]interface{}{
			"alertName":                   "test-alert",
			"alertCount":                  float64(5),
			"alertTriggerTimeMicroSeconds": float64(ts.UnixMicro()),
		}

		alertName, ruleName, alertCount, alertTimestamp, err := parseAlertWebhookBody(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if alertName != "test-alert" {
			t.Errorf("expected alertName 'test-alert', got %q", alertName)
		}
		if ruleName != "test-alert" {
			t.Errorf("expected ruleName 'test-alert', got %q", ruleName)
		}
		if alertCount != 5 {
			t.Errorf("expected alertCount 5, got %v", alertCount)
		}
		if !alertTimestamp.Equal(ts) {
			t.Errorf("expected timestamp %v, got %v", ts, alertTimestamp)
		}
	})

	t.Run("string count and string timestamp", func(t *testing.T) {
		ts := time.UnixMicro(1750000200000000)
		body := map[string]interface{}{
			"alertName":                   "test-alert",
			"alertCount":                  "3.14",
			"alertTriggerTimeMicroSeconds": "1750000200000000",
		}

		_, _, alertCount, alertTimestamp, err := parseAlertWebhookBody(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if alertCount != 3.14 {
			t.Errorf("expected alertCount 3.14, got %v", alertCount)
		}
		if !alertTimestamp.Equal(ts) {
			t.Errorf("expected timestamp %v, got %v", ts, alertTimestamp)
		}
	})

	t.Run("missing alertName", func(t *testing.T) {
		body := map[string]interface{}{
			"alertCount": float64(1),
		}
		_, _, _, _, err := parseAlertWebhookBody(body)
		if err == nil {
			t.Fatal("expected error for missing alertName")
		}
	})

	t.Run("alertName not a string", func(t *testing.T) {
		body := map[string]interface{}{
			"alertName": 123,
		}
		_, _, _, _, err := parseAlertWebhookBody(body)
		if err == nil {
			t.Fatal("expected error for non-string alertName")
		}
	})

	t.Run("invalid string count", func(t *testing.T) {
		body := map[string]interface{}{
			"alertName":  "test",
			"alertCount": "not-a-number",
		}
		_, _, _, _, err := parseAlertWebhookBody(body)
		if err == nil {
			t.Fatal("expected error for invalid alertCount")
		}
	})

	t.Run("invalid string timestamp", func(t *testing.T) {
		body := map[string]interface{}{
			"alertName":                   "test",
			"alertTriggerTimeMicroSeconds": "not-a-number",
		}
		_, _, _, _, err := parseAlertWebhookBody(body)
		if err == nil {
			t.Fatal("expected error for invalid timestamp")
		}
	})

	t.Run("missing timestamp defaults to now", func(t *testing.T) {
		before := time.Now()
		body := map[string]interface{}{
			"alertName": "test",
		}
		_, _, _, ts, err := parseAlertWebhookBody(body)
		after := time.Now()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ts.Before(before) || ts.After(after) {
			t.Errorf("expected timestamp near now, got %v", ts)
		}
	})
}

func TestToComponentLogsParams(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	limit := 50
	sortOrder := gen.LogsQueryRequestSortOrder("ASC")
	search := "error"
	projectUID := "proj-1"
	envUID := "env-1"
	compUID := "comp-1"
	logLevels := []gen.LogsQueryRequestLogLevels{"ERROR", "WARN"}

	req := &gen.LogsQueryRequest{
		StartTime:    startTime,
		EndTime:      endTime,
		Limit:        &limit,
		SortOrder:    &sortOrder,
		SearchPhrase: &search,
		LogLevels:    &logLevels,
	}
	scope := &gen.ComponentSearchScope{
		Namespace:      "test-ns",
		ProjectUid:     &projectUID,
		EnvironmentUid: &envUID,
		ComponentUid:   &compUID,
	}

	params := toComponentLogsParams(req, scope)

	if params.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", params.Namespace)
	}
	if params.ProjectID != "proj-1" {
		t.Errorf("expected projectID 'proj-1', got %q", params.ProjectID)
	}
	if params.EnvironmentID != "env-1" {
		t.Errorf("expected environmentID 'env-1', got %q", params.EnvironmentID)
	}
	if len(params.ComponentIDs) != 1 || params.ComponentIDs[0] != "comp-1" {
		t.Errorf("expected componentIDs ['comp-1'], got %v", params.ComponentIDs)
	}
	if params.Limit != 50 {
		t.Errorf("expected limit 50, got %d", params.Limit)
	}
	if params.SortOrder != "ASC" {
		t.Errorf("expected sortOrder 'ASC', got %q", params.SortOrder)
	}
	if params.SearchPhrase != "error" {
		t.Errorf("expected searchPhrase 'error', got %q", params.SearchPhrase)
	}
	if len(params.LogLevels) != 2 {
		t.Errorf("expected 2 logLevels, got %d", len(params.LogLevels))
	}
}

func TestToWorkflowLogsParams(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	workflowRunName := "run-1"

	req := &gen.LogsQueryRequest{
		StartTime: startTime,
		EndTime:   endTime,
	}
	scope := &gen.WorkflowSearchScope{
		Namespace:       "test-ns",
		WorkflowRunName: &workflowRunName,
	}

	params := toWorkflowLogsParams(req, scope)

	if params.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", params.Namespace)
	}
	if params.WorkflowRunName != "run-1" {
		t.Errorf("expected workflowRunName 'run-1', got %q", params.WorkflowRunName)
	}
	if !params.StartTime.Equal(startTime) {
		t.Errorf("expected startTime %v, got %v", startTime, params.StartTime)
	}
}

func TestToLogAlertParams(t *testing.T) {
	enabled := true
	projUID, _ := parseUUID("550e8400-e29b-41d4-a716-446655440000")
	envUID, _ := parseUUID("550e8400-e29b-41d4-a716-446655440001")
	compUID, _ := parseUUID("550e8400-e29b-41d4-a716-446655440002")
	operator := gen.AlertRuleRequestConditionOperator("gt")
	threshold := float32(5)
	window := "5m"
	interval := "1m"
	query := "error pattern"

	req := &gen.AlertRuleRequest{
		Metadata: struct {
			ComponentUid   openapi_types.UUID `json:"componentUid"`
			EnvironmentUid openapi_types.UUID `json:"environmentUid"`
			Name           string             `json:"name"`
			Namespace      string             `json:"namespace"`
			ProjectUid     openapi_types.UUID `json:"projectUid"`
		}{
			Name:           "my-alert",
			Namespace:      "ns-1",
			ProjectUid:     projUID,
			EnvironmentUid: envUID,
			ComponentUid:   compUID,
		},
		Source: struct {
			Query string `json:"query"`
		}{
			Query: query,
		},
		Condition: struct {
			Enabled   bool                                 `json:"enabled"`
			Interval  string                               `json:"interval"`
			Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
			Threshold float32                              `json:"threshold"`
			Window    string                               `json:"window"`
		}{
			Enabled:   enabled,
			Operator:  operator,
			Threshold: threshold,
			Window:    window,
			Interval:  interval,
		},
	}

	params := toLogAlertParams(req)

	if params.Name == nil || *params.Name != "my-alert" {
		t.Errorf("expected name 'my-alert', got %v", params.Name)
	}
	if params.Namespace != "ns-1" {
		t.Errorf("expected namespace 'ns-1', got %q", params.Namespace)
	}
	if params.SearchPattern != "error pattern" {
		t.Errorf("expected searchPattern 'error pattern', got %q", params.SearchPattern)
	}
	if params.Operator != "gt" {
		t.Errorf("expected operator 'gt', got %q", params.Operator)
	}
	if params.ThresholdValue != 5 {
		t.Errorf("expected threshold 5, got %v", params.ThresholdValue)
	}
	if params.Window != "5m" {
		t.Errorf("expected window '5m', got %q", params.Window)
	}
	if params.Interval != "1m" {
		t.Errorf("expected interval '1m', got %q", params.Interval)
	}
}

func TestToLogsQueryResponse(t *testing.T) {
	result := &openobserve.ComponentLogsResult{
		TotalCount: 1,
		Took:       15,
		Logs: []openobserve.ComponentLogsEntry{
			{
				Timestamp:     time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
				Log:           "test log",
				LogLevel:      "INFO",
				Namespace:     "ns-1",
				PodName:       "pod-1",
				ContainerName: "main",
			},
		},
	}

	resp := toLogsQueryResponse(result)

	if resp.Total == nil || *resp.Total != 1 {
		t.Errorf("expected total 1, got %v", resp.Total)
	}
	if resp.TookMs == nil || *resp.TookMs != 15 {
		t.Errorf("expected tookMs 15, got %v", resp.TookMs)
	}
}

func TestToWorkflowLogsQueryResponse(t *testing.T) {
	result := &openobserve.WorkflowLogsResult{
		TotalCount: 1,
		Took:       10,
		Logs: []openobserve.WorkflowLogsEntry{
			{
				Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
				Log:       "workflow log",
			},
		},
	}

	resp := toWorkflowLogsQueryResponse(result)

	if resp.Total == nil || *resp.Total != 1 {
		t.Errorf("expected total 1, got %v", resp.Total)
	}
	if resp.TookMs == nil || *resp.TookMs != 10 {
		t.Errorf("expected tookMs 10, got %v", resp.TookMs)
	}
}

func TestToComponentLogEntry(t *testing.T) {
	entry := openobserve.ComponentLogsEntry{
		Timestamp:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Log:             "error message",
		LogLevel:        "ERROR",
		ComponentUID:    "550e8400-e29b-41d4-a716-446655440000",
		ComponentName:   "my-comp",
		EnvironmentUID:  "550e8400-e29b-41d4-a716-446655440001",
		EnvironmentName: "dev",
		ProjectUID:      "550e8400-e29b-41d4-a716-446655440002",
		ProjectName:     "my-project",
		Namespace:       "test-ns",
		PodName:         "pod-1",
		PodNamespace:    "k8s-ns",
		ContainerName:   "main",
	}

	result := toComponentLogEntry(&entry)

	if result.Log == nil || *result.Log != "error message" {
		t.Errorf("unexpected log: %v", result.Log)
	}
	if result.Level == nil || *result.Level != "ERROR" {
		t.Errorf("unexpected level: %v", result.Level)
	}
	if result.Metadata == nil {
		t.Fatal("expected metadata to not be nil")
	}
	if result.Metadata.ComponentName == nil || *result.Metadata.ComponentName != "my-comp" {
		t.Errorf("unexpected componentName: %v", result.Metadata.ComponentName)
	}
	if result.Metadata.ComponentUid == nil {
		t.Error("expected componentUid to be set")
	}
	if result.Metadata.PodName == nil || *result.Metadata.PodName != "pod-1" {
		t.Errorf("unexpected podName: %v", result.Metadata.PodName)
	}
}

func TestToComponentLogEntry_EmptyUID(t *testing.T) {
	entry := openobserve.ComponentLogsEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Log:       "test",
		LogLevel:  "INFO",
	}

	result := toComponentLogEntry(&entry)

	if result.Metadata.ComponentUid != nil {
		t.Error("expected componentUid to be nil for empty UID")
	}
	if result.Metadata.ProjectUid != nil {
		t.Error("expected projectUid to be nil for empty UID")
	}
	if result.Metadata.EnvironmentUid != nil {
		t.Error("expected environmentUid to be nil for empty UID")
	}
}

func TestParseUUID(t *testing.T) {
	t.Run("valid UUID", func(t *testing.T) {
		uid, ok := parseUUID("550e8400-e29b-41d4-a716-446655440000")
		if !ok {
			t.Fatal("expected ok for valid UUID")
		}
		if uid.String() != "550e8400-e29b-41d4-a716-446655440000" {
			t.Errorf("unexpected UUID: %s", uid.String())
		}
	})

	t.Run("invalid UUID", func(t *testing.T) {
		_, ok := parseUUID("not-a-uuid")
		if ok {
			t.Fatal("expected !ok for invalid UUID")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		_, ok := parseUUID("")
		if ok {
			t.Fatal("expected !ok for empty string")
		}
	})
}

func TestStrPtr(t *testing.T) {
	if got := strPtr(""); got != nil {
		t.Error("expected nil for empty string")
	}
	if got := strPtr("hello"); got == nil || *got != "hello" {
		t.Errorf("expected pointer to 'hello', got %v", got)
	}
}

func TestPtr(t *testing.T) {
	v := ptr(42)
	if v == nil || *v != 42 {
		t.Errorf("expected pointer to 42, got %v", v)
	}
}

func TestQueryLogs_ComponentScope(t *testing.T) {
	// Create a mock OpenObserve server
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openobserve.OpenObserveResponse{
			Took:  5,
			Total: 1,
			Hits: []map[string]interface{}{
				{
					"_timestamp": float64(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixMicro()),
					"log":        "test log message",
					"logLevel":   "INFO",
					"kubernetes_labels_openchoreo_dev_namespace": "test-ns",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	scope := gen.LogsQueryRequest_SearchScope{}
	_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
		Namespace: "test-ns",
	})

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{
		Body: &gen.LogsQueryRequest{
			StartTime:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: scope,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.QueryLogs200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestCreateAlertRule_Success(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "alert-123"})
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	enabled := true
	operator := gen.AlertRuleRequestConditionOperator("gt")
	threshold := float32(5)
	window := "5m"
	interval := "1m"

	envUID, _ := parseUUID("550e8400-e29b-41d4-a716-446655440001")
	compUID, _ := parseUUID("550e8400-e29b-41d4-a716-446655440002")

	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{
		Body: &gen.AlertRuleRequest{
			Metadata: struct {
				ComponentUid   openapi_types.UUID `json:"componentUid"`
				EnvironmentUid openapi_types.UUID `json:"environmentUid"`
				Name           string             `json:"name"`
				Namespace      string             `json:"namespace"`
				ProjectUid     openapi_types.UUID `json:"projectUid"`
			}{
				Name:           "test-alert",
				Namespace:      "ns-1",
				EnvironmentUid: envUID,
				ComponentUid:   compUID,
			},
			Source: struct {
				Query string `json:"query"`
			}{
				Query: "error",
			},
			Condition: struct {
				Enabled   bool                                 `json:"enabled"`
				Interval  string                               `json:"interval"`
				Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
				Threshold float32                              `json:"threshold"`
				Window    string                               `json:"window"`
			}{
				Enabled:   enabled,
				Operator:  operator,
				Threshold: threshold,
				Window:    window,
				Interval:  interval,
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	createResp, ok := resp.(gen.CreateAlertRule201JSONResponse)
	if !ok {
		t.Fatalf("expected 201 response, got %T", resp)
	}
	if createResp.RuleBackendId == nil || *createResp.RuleBackendId != "alert-123" {
		t.Errorf("expected ruleBackendId 'alert-123', got %v", createResp.RuleBackendId)
	}
}

func TestDeleteAlertRule_Success(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-456", "name": "test-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.Method == "DELETE":
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	resp, err := handler.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{
		RuleName: "test-alert",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deleteResp, ok := resp.(gen.DeleteAlertRule200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if deleteResp.RuleBackendId == nil || *deleteResp.RuleBackendId != "alert-456" {
		t.Errorf("expected ruleBackendId 'alert-456', got %v", deleteResp.RuleBackendId)
	}
}

func TestGetAlertRule_Success(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-789", "name": "test-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/v2/default/alerts/alert-789":
			resp := map[string]interface{}{
				"name":    "test-alert",
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
					"namespace": "test-ns",
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	resp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{
		RuleName: "test-alert",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	getResp, ok := resp.(gen.GetAlertRule200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if getResp.Source == nil || getResp.Source.Query == nil || *getResp.Source.Query != "error" {
		t.Errorf("expected search pattern 'error', got %v", getResp.Source)
	}
	if getResp.Condition == nil || getResp.Condition.Operator == nil || string(*getResp.Condition.Operator) != "gt" {
		t.Errorf("expected operator 'gt', got %v", getResp.Condition)
	}
}

func TestGetAlertRule_NotFound(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"list": []map[string]string{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	resp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{
		RuleName: "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 response, got %T", resp)
	}
}

func TestUpdateAlertRule_Success(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-upd-1", "name": "test-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.Method == "PUT" && r.URL.Path == "/api/v2/default/alerts/alert-upd-1":
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	enabled := true
	operator := gen.AlertRuleRequestConditionOperator("gt")
	threshold := float32(5)
	window := "5m"
	interval := "1m"

	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "test-alert",
		Body: &gen.AlertRuleRequest{
			Metadata: struct {
				ComponentUid   openapi_types.UUID `json:"componentUid"`
				EnvironmentUid openapi_types.UUID `json:"environmentUid"`
				Name           string             `json:"name"`
				Namespace      string             `json:"namespace"`
				ProjectUid     openapi_types.UUID `json:"projectUid"`
			}{
				Name:      "test-alert",
				Namespace: "ns-1",
			},
			Source: struct {
				Query string `json:"query"`
			}{
				Query: "error",
			},
			Condition: struct {
				Enabled   bool                                 `json:"enabled"`
				Interval  string                               `json:"interval"`
				Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
				Threshold float32                              `json:"threshold"`
				Window    string                               `json:"window"`
			}{
				Enabled:   enabled,
				Operator:  operator,
				Threshold: threshold,
				Window:    window,
				Interval:  interval,
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updateResp, ok := resp.(gen.UpdateAlertRule200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if updateResp.RuleBackendId == nil || *updateResp.RuleBackendId != "alert-upd-1" {
		t.Errorf("expected ruleBackendId 'alert-upd-1', got %v", updateResp.RuleBackendId)
	}
}

func TestUpdateAlertRule_NotFound(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"list": []map[string]string{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	enabled := true
	operator := gen.AlertRuleRequestConditionOperator("gt")
	threshold := float32(5)

	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "nonexistent",
		Body: &gen.AlertRuleRequest{
			Condition: struct {
				Enabled   bool                                 `json:"enabled"`
				Interval  string                               `json:"interval"`
				Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
				Threshold float32                              `json:"threshold"`
				Window    string                               `json:"window"`
			}{
				Enabled:   enabled,
				Operator:  operator,
				Threshold: threshold,
				Window:    "5m",
				Interval:  "1m",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 response for not found, got %T", resp)
	}
}

func TestUpdateAlertRule_InvalidError(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-inv-1", "name": "test-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.Method == "PUT":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("invalid alert config"))
		}
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	enabled := true
	operator := gen.AlertRuleRequestConditionOperator("gt")
	threshold := float32(5)

	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "test-alert",
		Body: &gen.AlertRuleRequest{
			Condition: struct {
				Enabled   bool                                 `json:"enabled"`
				Interval  string                               `json:"interval"`
				Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
				Threshold float32                              `json:"threshold"`
				Window    string                               `json:"window"`
			}{
				Enabled:   enabled,
				Operator:  operator,
				Threshold: threshold,
				Window:    "5m",
				Interval:  "1m",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 response for invalid error, got %T", resp)
	}
}

func TestUpdateAlertRule_GenericError(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-gen-1", "name": "test-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case r.Method == "PUT":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("something went wrong"))
		}
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	enabled := true
	operator := gen.AlertRuleRequestConditionOperator("gt")
	threshold := float32(5)

	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "test-alert",
		Body: &gen.AlertRuleRequest{
			Condition: struct {
				Enabled   bool                                 `json:"enabled"`
				Interval  string                               `json:"interval"`
				Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
				Threshold float32                              `json:"threshold"`
				Window    string                               `json:"window"`
			}{
				Enabled:   enabled,
				Operator:  operator,
				Threshold: threshold,
				Window:    "5m",
				Interval:  "1m",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.UpdateAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500 response for generic error, got %T", resp)
	}
}

func TestQueryLogs_WorkflowScope_Success(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openobserve.OpenObserveResponse{
			Took:  3,
			Total: 1,
			Hits: []map[string]interface{}{
				{
					"_timestamp": float64(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixMicro()),
					"log":        "workflow step done",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	scope := gen.LogsQueryRequest_SearchScope{}
	workflowRunName := "run-1"
	_ = scope.FromWorkflowSearchScope(gen.WorkflowSearchScope{
		Namespace:       "test-ns",
		WorkflowRunName: &workflowRunName,
	})

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{
		Body: &gen.LogsQueryRequest{
			StartTime:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: scope,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.QueryLogs200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestQueryLogs_WorkflowScope_Error(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	scope := gen.LogsQueryRequest_SearchScope{}
	workflowRunName := "run-1"
	_ = scope.FromWorkflowSearchScope(gen.WorkflowSearchScope{
		Namespace:       "test-ns",
		WorkflowRunName: &workflowRunName,
	})

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{
		Body: &gen.LogsQueryRequest{
			StartTime:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: scope,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.QueryLogs500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestQueryLogs_WorkflowScope_EmptyNamespace(t *testing.T) {
	handler := NewLogsHandler(nil, nil, testLogger())

	scope := gen.LogsQueryRequest_SearchScope{}
	workflowRunName := "run-1"
	_ = scope.FromWorkflowSearchScope(gen.WorkflowSearchScope{
		Namespace:       "  ",
		WorkflowRunName: &workflowRunName,
	})

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{
		Body: &gen.LogsQueryRequest{
			StartTime:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: scope,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.QueryLogs400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryLogs_ComponentScope_Error(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	scope := gen.LogsQueryRequest_SearchScope{}
	_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
		Namespace: "test-ns",
	})

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{
		Body: &gen.LogsQueryRequest{
			StartTime:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: scope,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.QueryLogs500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestDeleteAlertRule_Error(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"list": []map[string]string{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	resp, err := handler.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{
		RuleName: "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.DeleteAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestCreateAlertRule_Error(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	enabled := true
	operator := gen.AlertRuleRequestConditionOperator("gt")
	threshold := float32(5)
	envUID, _ := parseUUID("550e8400-e29b-41d4-a716-446655440001")
	compUID, _ := parseUUID("550e8400-e29b-41d4-a716-446655440002")

	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{
		Body: &gen.AlertRuleRequest{
			Metadata: struct {
				ComponentUid   openapi_types.UUID `json:"componentUid"`
				EnvironmentUid openapi_types.UUID `json:"environmentUid"`
				Name           string             `json:"name"`
				Namespace      string             `json:"namespace"`
				ProjectUid     openapi_types.UUID `json:"projectUid"`
			}{
				Name:           "test-alert",
				Namespace:      "ns-1",
				EnvironmentUid: envUID,
				ComponentUid:   compUID,
			},
			Source: struct {
				Query string `json:"query"`
			}{
				Query: "error",
			},
			Condition: struct {
				Enabled   bool                                 `json:"enabled"`
				Interval  string                               `json:"interval"`
				Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
				Threshold float32                              `json:"threshold"`
				Window    string                               `json:"window"`
			}{
				Enabled:   enabled,
				Operator:  operator,
				Threshold: threshold,
				Window:    "5m",
				Interval:  "1m",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.CreateAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestGetAlertRule_InvalidUUIDs(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-uuid-test", "name": "test-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/v2/default/alerts/alert-uuid-test":
			resp := map[string]interface{}{
				"name":    "test-alert",
				"enabled": true,
				"query_condition": map[string]interface{}{
					"sql": "SELECT count(*) FROM \"default\" WHERE str_match(log, 'error')",
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
					"projectUid":     "not-a-valid-uuid",
					"environmentUid": "also-not-valid",
					"componentUid":   "invalid-too",
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	resp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{
		RuleName: "test-alert",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	getResp, ok := resp.(gen.GetAlertRule200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	// Invalid UUIDs should result in nil UID fields
	if getResp.Metadata.ProjectUid != nil {
		t.Error("expected projectUid to be nil for invalid UUID")
	}
	if getResp.Metadata.EnvironmentUid != nil {
		t.Error("expected environmentUid to be nil for invalid UUID")
	}
	if getResp.Metadata.ComponentUid != nil {
		t.Error("expected componentUid to be nil for invalid UUID")
	}
}

func TestGetAlertRule_ServerError(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-err", "name": "test-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/v2/default/alerts/alert-err":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	resp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{
		RuleName: "test-alert",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.GetAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestToWorkflowLogsParams_AllOptions(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	workflowRunName := "run-1"
	limit := 50
	sortOrder := gen.LogsQueryRequestSortOrder("ASC")
	search := "error"
	logLevels := []gen.LogsQueryRequestLogLevels{"ERROR", "WARN"}

	req := &gen.LogsQueryRequest{
		StartTime:    startTime,
		EndTime:      endTime,
		Limit:        &limit,
		SortOrder:    &sortOrder,
		SearchPhrase: &search,
		LogLevels:    &logLevels,
	}
	scope := &gen.WorkflowSearchScope{
		Namespace:       "test-ns",
		WorkflowRunName: &workflowRunName,
	}

	params := toWorkflowLogsParams(req, scope)

	if params.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", params.Namespace)
	}
	if params.WorkflowRunName != "run-1" {
		t.Errorf("expected workflowRunName 'run-1', got %q", params.WorkflowRunName)
	}
	if params.Limit != 50 {
		t.Errorf("expected limit 50, got %d", params.Limit)
	}
	if params.SortOrder != "ASC" {
		t.Errorf("expected sortOrder 'ASC', got %q", params.SortOrder)
	}
	if params.SearchPhrase != "error" {
		t.Errorf("expected searchPhrase 'error', got %q", params.SearchPhrase)
	}
	if len(params.LogLevels) != 2 {
		t.Errorf("expected 2 logLevels, got %d", len(params.LogLevels))
	}
}

func TestToWorkflowLogsParams_NilOptionals(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	req := &gen.LogsQueryRequest{
		StartTime: startTime,
		EndTime:   endTime,
	}
	scope := &gen.WorkflowSearchScope{
		Namespace: "test-ns",
	}

	params := toWorkflowLogsParams(req, scope)

	if params.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", params.Namespace)
	}
	if params.WorkflowRunName != "" {
		t.Errorf("expected empty workflowRunName, got %q", params.WorkflowRunName)
	}
	if params.Limit != 0 {
		t.Errorf("expected limit 0, got %d", params.Limit)
	}
	if params.SortOrder != "" {
		t.Errorf("expected empty sortOrder, got %q", params.SortOrder)
	}
	if params.SearchPhrase != "" {
		t.Errorf("expected empty searchPhrase, got %q", params.SearchPhrase)
	}
	if params.LogLevels != nil {
		t.Errorf("expected nil logLevels, got %v", params.LogLevels)
	}
}

func TestHandleAlertWebhook_ValidBody(t *testing.T) {
	// Mock OpenObserve for GetAlert call
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/default/alerts":
			resp := map[string]interface{}{
				"list": []map[string]string{
					{"alert_id": "alert-1", "name": "test-alert"},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case "/api/v2/default/alerts/alert-1":
			resp := map[string]interface{}{
				"name":    "test-alert",
				"enabled": true,
				"context_attributes": map[string]interface{}{
					"namespace": "test-ns",
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ooServer.Close()

	// Channel to capture the forwarded request from the background goroutine
	type capturedRequest struct {
		method string
		path   string
		body   map[string]interface{}
	}
	captured := make(chan capturedRequest, 1)

	// Mock observer that captures the incoming POST
	observerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)
		captured <- capturedRequest{
			method: r.Method,
			path:   r.URL.Path,
			body:   reqBody,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer observerServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	obsClient := observer.NewClient(observerServer.URL)
	handler := NewLogsHandler(client, obsClient, testLogger())

	alertTime := float64(time.Now().UnixMicro())
	body := &map[string]interface{}{
		"alertName":                   "test-alert",
		"alertCount":                  float64(3),
		"alertTriggerTimeMicroSeconds": alertTime,
	}

	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{
		Body: body,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	webhookResp, ok := resp.(gen.HandleAlertWebhook200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if webhookResp.Status == nil || *webhookResp.Status != gen.Success {
		t.Error("expected status Success")
	}

	// Wait for the background goroutine to forward the alert to the observer
	select {
	case req := <-captured:
		if req.method != http.MethodPost {
			t.Errorf("expected POST method, got %s", req.method)
		}
		if req.path != "/api/v1alpha1/alerts/webhook" {
			t.Errorf("expected path /api/v1alpha1/alerts/webhook, got %s", req.path)
		}
		if req.body["ruleName"] != "test-alert" {
			t.Errorf("expected ruleName 'test-alert', got %v", req.body["ruleName"])
		}
		if req.body["ruleNamespace"] != "test-ns" {
			t.Errorf("expected ruleNamespace 'test-ns', got %v", req.body["ruleNamespace"])
		}
		if req.body["alertValue"] != float64(3) {
			t.Errorf("expected alertValue 3, got %v", req.body["alertValue"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for observer webhook to be called")
	}
}
