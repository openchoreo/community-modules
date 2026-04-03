// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/observer"
	osearch "github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestOSClient creates an opensearch.Client pointing at the given test server URL.
func newTestOSClient(t *testing.T, serverURL string) *osearch.Client {
	t.Helper()
	client, err := osearch.NewClient(serverURL, "", "", true, testLogger())
	if err != nil {
		t.Fatalf("failed to create test opensearch client: %v", err)
	}
	return client
}

func TestHealth(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, testLogger())
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
	handler := NewLogsHandler(nil, nil, nil, testLogger())
	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryLogs400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryLogs_ComponentScope_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"took":      3,
			"timed_out": false,
			"hits": map[string]interface{}{
				"total": map[string]interface{}{
					"value":    1,
					"relation": "eq",
				},
				"hits": []map[string]interface{}{
					{
						"_id":    "hit-1",
						"_score": 1.0,
						"_source": map[string]interface{}{
							"log":        "INFO application started",
							"@timestamp": "2025-06-15T10:00:00Z",
							"kubernetes": map[string]interface{}{
								"namespace_name": "test-ns",
								"pod_name":       "my-pod",
								"container_name": "main",
								"labels":         map[string]interface{}{},
							},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	qb := osearch.NewQueryBuilder("logs-")
	handler := NewLogsHandler(osClient, qb, nil, testLogger())

	startTime := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 6, 15, 23, 59, 59, 0, time.UTC)

	searchScope := gen.LogsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{
		Namespace: "test-ns",
	})

	body := gen.LogsQueryRequest{
		StartTime:   startTime,
		EndTime:     endTime,
		SearchScope: searchScope,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queryResp, ok := resp.(gen.QueryLogs200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if queryResp.Total == nil || *queryResp.Total != 1 {
		t.Errorf("expected total=1, got %v", queryResp.Total)
	}
}

func TestQueryLogs_ComponentScope_EmptyNamespace(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, testLogger())

	searchScope := gen.LogsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{
		Namespace: "",
	})

	body := gen.LogsQueryRequest{
		StartTime:   time.Now(),
		EndTime:     time.Now(),
		SearchScope: searchScope,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryLogs400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryLogs_ComponentScope_SearchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"search failed"}`)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	qb := osearch.NewQueryBuilder("logs-")
	handler := NewLogsHandler(osClient, qb, nil, testLogger())

	searchScope := gen.LogsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{
		Namespace: "test-ns",
	})

	body := gen.LogsQueryRequest{
		StartTime:   time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2025, 6, 15, 23, 59, 59, 0, time.UTC),
		SearchScope: searchScope,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryLogs500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestQueryLogs_WorkflowScope_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"took":      2,
			"timed_out": false,
			"hits": map[string]interface{}{
				"total": map[string]interface{}{
					"value":    1,
					"relation": "eq",
				},
				"hits": []map[string]interface{}{
					{
						"_id":    "wf-hit-1",
						"_score": 1.0,
						"_source": map[string]interface{}{
							"log":        "step completed",
							"@timestamp": "2025-06-15T10:00:00Z",
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	qb := osearch.NewQueryBuilder("logs-")
	handler := NewLogsHandler(osClient, qb, nil, testLogger())

	workflowRunName := "run-123"
	searchScope := gen.LogsQueryRequest_SearchScope{}
	_ = searchScope.FromWorkflowSearchScope(gen.WorkflowSearchScope{
		Namespace:       "test-ns",
		WorkflowRunName: &workflowRunName,
	})

	body := gen.LogsQueryRequest{
		StartTime:   time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2025, 6, 15, 23, 59, 59, 0, time.UTC),
		SearchScope: searchScope,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queryResp, ok := resp.(gen.QueryLogs200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if queryResp.Total == nil || *queryResp.Total != 1 {
		t.Errorf("expected total=1, got %v", queryResp.Total)
	}
}

func TestQueryLogs_WorkflowScope_EmptyNamespace(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, testLogger())

	workflowRunName := "run-123"
	searchScope := gen.LogsQueryRequest_SearchScope{}
	_ = searchScope.FromWorkflowSearchScope(gen.WorkflowSearchScope{
		Namespace:       "",
		WorkflowRunName: &workflowRunName,
	})

	body := gen.LogsQueryRequest{
		StartTime:   time.Now(),
		EndTime:     time.Now(),
		SearchScope: searchScope,
	}

	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryLogs400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestCreateAlertRule_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, testLogger())
	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestCreateAlertRule_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_plugins/_alerting/monitors" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"_id": "b2c3d4e5-f6a7-8901-bcde-f12345678901", "monitor": {"last_update_time": 1718444400000}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	qb := osearch.NewQueryBuilder("logs-")
	handler := NewLogsHandler(osClient, qb, nil, testLogger())

	body := gen.AlertRuleRequest{
		Metadata: struct {
			ComponentUid   openapi_types.UUID `json:"componentUid"`
			EnvironmentUid openapi_types.UUID `json:"environmentUid"`
			Name           string             `json:"name"`
			Namespace      string             `json:"namespace"`
			ProjectUid     openapi_types.UUID `json:"projectUid"`
		}{
			Name:      "test-rule",
			Namespace: "test-ns",
		},
		Source: struct {
			Query string `json:"query"`
		}{
			Query: "error",
		},
		Condition: struct {
			Enabled   bool                                  `json:"enabled"`
			Interval  string                                `json:"interval"`
			Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
			Threshold float32                               `json:"threshold"`
			Window    string                                `json:"window"`
		}{
			Enabled:   true,
			Window:    "1h",
			Interval:  "5m",
			Operator:  gen.AlertRuleRequestConditionOperatorGt,
			Threshold: 10,
		},
	}

	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	createResp, ok := resp.(gen.CreateAlertRule201JSONResponse)
	if !ok {
		t.Fatalf("expected 201 response, got %T", resp)
	}
	if createResp.RuleBackendId == nil || *createResp.RuleBackendId != "b2c3d4e5-f6a7-8901-bcde-f12345678901" {
		t.Errorf("expected ruleBackendId 'b2c3d4e5-f6a7-8901-bcde-f12345678901', got %v", createResp.RuleBackendId)
	}
}

func TestCreateAlertRule_CreateError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"bad request"}`)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	qb := osearch.NewQueryBuilder("logs-")
	handler := NewLogsHandler(osClient, qb, nil, testLogger())

	body := gen.AlertRuleRequest{
		Metadata: struct {
			ComponentUid   openapi_types.UUID `json:"componentUid"`
			EnvironmentUid openapi_types.UUID `json:"environmentUid"`
			Name           string             `json:"name"`
			Namespace      string             `json:"namespace"`
			ProjectUid     openapi_types.UUID `json:"projectUid"`
		}{Name: "test-rule", Namespace: "test-ns"},
		Source: struct {
			Query string `json:"query"`
		}{Query: "error"},
		Condition: struct {
			Enabled   bool                                  `json:"enabled"`
			Interval  string                                `json:"interval"`
			Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
			Threshold float32                               `json:"threshold"`
			Window    string                                `json:"window"`
		}{Window: "1h", Interval: "5m", Operator: gen.AlertRuleRequestConditionOperatorGt, Threshold: 10},
	}

	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

// deleteAlertRuleServer returns an httptest.Server that handles monitor search and delete.
func deleteAlertRuleServer(monitorFound bool, deleteErr bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_plugins/_alerting/monitors/_search" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if monitorFound {
				fmt.Fprint(w, `{"hits":{"total":{"value":1},"hits":[{"_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","_source":{}}]}}`)
			} else {
				fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") && r.Method == "DELETE" {
			if deleteErr {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func TestDeleteAlertRule_Success(t *testing.T) {
	server := deleteAlertRuleServer(true, false)
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	handler := NewLogsHandler(osClient, nil, nil, testLogger())

	resp, err := handler.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "test-rule"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestDeleteAlertRule_NotFound(t *testing.T) {
	server := deleteAlertRuleServer(false, false)
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	handler := NewLogsHandler(osClient, nil, nil, testLogger())

	resp, err := handler.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 response for not found, got %T", resp)
	}
}

func TestDeleteAlertRule_SearchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	handler := NewLogsHandler(osClient, nil, nil, testLogger())

	resp, err := handler.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "test-rule"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

// getAlertRuleServer returns an httptest.Server for GetAlertRule tests.
func getAlertRuleServer(monitorFound bool, monitorData map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_plugins/_alerting/monitors/_search" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if monitorFound {
				fmt.Fprint(w, `{"hits":{"total":{"value":1},"hits":[{"_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","_source":{}}]}}`)
			} else {
				fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
			}
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := map[string]interface{}{"monitor": monitorData}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func TestGetAlertRule_Success(t *testing.T) {
	monitorData := map[string]interface{}{
		"name":    "test-rule",
		"enabled": true,
		"schedule": map[string]interface{}{
			"period": map[string]interface{}{
				"interval": float64(5),
				"unit":     "MINUTES",
			},
		},
		"inputs": []interface{}{
			map[string]interface{}{
				"search": map[string]interface{}{
					"indices": []interface{}{"logs-*"},
					"query": map[string]interface{}{
						"size": 0,
						"query": map[string]interface{}{
							"bool": map[string]interface{}{
								"filter": []interface{}{
									map[string]interface{}{
										"range": map[string]interface{}{
											"@timestamp": map[string]interface{}{
												"from": "{{period_end}}||-1h",
												"to":   "{{period_end}}",
											},
										},
									},
									map[string]interface{}{
										"wildcard": map[string]interface{}{
											"log": map[string]interface{}{
												"wildcard": "*error*",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"triggers": []interface{}{
			map[string]interface{}{
				"query_level_trigger": map[string]interface{}{
					"name":     "trigger-test-rule",
					"severity": "1",
					"condition": map[string]interface{}{
						"script": map[string]interface{}{
							"source": "ctx.results[0].hits.total.value > 10",
							"lang":   "painless",
						},
					},
				},
			},
		},
	}

	server := getAlertRuleServer(true, monitorData)
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	handler := NewLogsHandler(osClient, nil, nil, testLogger())

	resp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "test-rule"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	getResp, ok := resp.(gen.GetAlertRule200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if getResp.Metadata == nil || getResp.Metadata.Name == nil || *getResp.Metadata.Name != "test-rule" {
		t.Error("expected metadata name to be 'test-rule'")
	}
}

func TestGetAlertRule_NotFound(t *testing.T) {
	server := getAlertRuleServer(false, nil)
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	handler := NewLogsHandler(osClient, nil, nil, testLogger())

	resp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 response, got %T", resp)
	}
}

func TestGetAlertRule_GetError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_plugins/_alerting/monitors/_search" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"hits":{"total":{"value":1},"hits":[{"_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","_source":{}}]}}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") && r.Method == "GET" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"internal error"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	handler := NewLogsHandler(osClient, nil, nil, testLogger())

	resp, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "test-rule"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.GetAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestUpdateAlertRule_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, testLogger())
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

func TestUpdateAlertRule_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_plugins/_alerting/monitors/_search" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"hits":{"total":{"value":1},"hits":[{"_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","_source":{}}]}}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_plugins/_alerting/monitors/") && r.Method == "PUT" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890", "monitor": {"last_update_time": 1718444400001}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	qb := osearch.NewQueryBuilder("logs-")
	handler := NewLogsHandler(osClient, qb, nil, testLogger())

	body := gen.AlertRuleRequest{
		Metadata: struct {
			ComponentUid   openapi_types.UUID `json:"componentUid"`
			EnvironmentUid openapi_types.UUID `json:"environmentUid"`
			Name           string             `json:"name"`
			Namespace      string             `json:"namespace"`
			ProjectUid     openapi_types.UUID `json:"projectUid"`
		}{Name: "test-rule", Namespace: "test-ns"},
		Source: struct {
			Query string `json:"query"`
		}{Query: "error"},
		Condition: struct {
			Enabled   bool                                  `json:"enabled"`
			Interval  string                                `json:"interval"`
			Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
			Threshold float32                               `json:"threshold"`
			Window    string                                `json:"window"`
		}{Window: "1h", Interval: "5m", Operator: gen.AlertRuleRequestConditionOperatorGt, Threshold: 10},
	}

	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "test-rule",
		Body:     &body,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestUpdateAlertRule_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_plugins/_alerting/monitors/_search" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"hits":{"total":{"value":0},"hits":[]}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	qb := osearch.NewQueryBuilder("logs-")
	handler := NewLogsHandler(osClient, qb, nil, testLogger())

	body := gen.AlertRuleRequest{
		Metadata: struct {
			ComponentUid   openapi_types.UUID `json:"componentUid"`
			EnvironmentUid openapi_types.UUID `json:"environmentUid"`
			Name           string             `json:"name"`
			Namespace      string             `json:"namespace"`
			ProjectUid     openapi_types.UUID `json:"projectUid"`
		}{Name: "test-rule", Namespace: "test-ns"},
		Source: struct {
			Query string `json:"query"`
		}{Query: "error"},
		Condition: struct {
			Enabled   bool                                  `json:"enabled"`
			Interval  string                                `json:"interval"`
			Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
			Threshold float32                               `json:"threshold"`
			Window    string                                `json:"window"`
		}{Window: "1h", Interval: "5m", Operator: gen.AlertRuleRequestConditionOperatorGt, Threshold: 10},
	}

	resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "test-rule",
		Body:     &body,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 response, got %T", resp)
	}
}

func TestHandleAlertWebhook_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, testLogger())
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

func TestHandleAlertWebhook_ValidBody(t *testing.T) {
	forwardCh := make(chan bool, 1)
	observerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		forwardCh <- true
	}))
	defer observerServer.Close()

	observerClient := observer.NewClient(observerServer.URL)
	handler := NewLogsHandler(nil, nil, observerClient, testLogger())

	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	body := map[string]interface{}{
		"ruleName":       "test-rule",
		"ruleNamespace":  "test-ns",
		"alertValue":     float64(5),
		"alertTimestamp": ts.Format(time.RFC3339),
	}

	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}

	// Wait for the goroutine to forward the alert
	select {
	case <-forwardCh:
		// success
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for alert forwarding")
	}
}

func TestParseAlertWebhookBody(t *testing.T) {
	t.Run("valid body", func(t *testing.T) {
		ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		body := map[string]interface{}{
			"ruleName":       "test-rule",
			"ruleNamespace":  "test-ns",
			"alertValue":     float64(5),
			"alertTimestamp": ts.Format(time.RFC3339),
		}

		ruleName, ruleNamespace, alertValue, alertTimestamp, err := parseAlertWebhookBody(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ruleName != "test-rule" {
			t.Errorf("expected ruleName 'test-rule', got %q", ruleName)
		}
		if ruleNamespace != "test-ns" {
			t.Errorf("expected ruleNamespace 'test-ns', got %q", ruleNamespace)
		}
		if alertValue != 5 {
			t.Errorf("expected alertValue 5, got %v", alertValue)
		}
		if !alertTimestamp.Equal(ts) {
			t.Errorf("expected alertTimestamp %v, got %v", ts, alertTimestamp)
		}
	})

	t.Run("missing ruleName", func(t *testing.T) {
		body := map[string]interface{}{
			"alertValue": float64(1),
		}
		_, _, _, _, err := parseAlertWebhookBody(body)
		if err == nil {
			t.Error("expected error for missing ruleName")
		}
	})

	t.Run("alertValue as string", func(t *testing.T) {
		body := map[string]interface{}{
			"ruleName":   "test",
			"alertValue": "42.5",
		}
		_, _, alertValue, _, err := parseAlertWebhookBody(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if alertValue != 42.5 {
			t.Errorf("expected alertValue 42.5, got %v", alertValue)
		}
	})

	t.Run("non-RFC3339 timestamp uses current time", func(t *testing.T) {
		body := map[string]interface{}{
			"ruleName":       "test",
			"alertTimestamp": "not-a-timestamp",
		}
		before := time.Now().Add(-time.Second)
		_, _, _, alertTimestamp, err := parseAlertWebhookBody(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if alertTimestamp.Before(before) {
			t.Errorf("expected alertTimestamp to be recent, got %v", alertTimestamp)
		}
	})
}

func TestParseConditionScript(t *testing.T) {
	t.Run("valid script", func(t *testing.T) {
		operator, threshold := parseConditionScript("ctx.results[0].hits.total.value > 10")
		if operator != ">" {
			t.Errorf("expected operator '>', got %q", operator)
		}
		if threshold != 10 {
			t.Errorf("expected threshold 10, got %v", threshold)
		}
	})

	t.Run("short input", func(t *testing.T) {
		operator, threshold := parseConditionScript("ab")
		if operator != "" {
			t.Errorf("expected empty operator, got %q", operator)
		}
		if threshold != 0 {
			t.Errorf("expected threshold 0, got %v", threshold)
		}
	})
}

func TestFormatScheduleToInterval(t *testing.T) {
	tests := []struct {
		interval float64
		unit     string
		expected string
	}{
		{5, "MINUTES", "5m"},
		{2, "HOURS", "2h"},
		{10, "UNKNOWN", "10m"},
	}

	for _, tt := range tests {
		t.Run(tt.unit, func(t *testing.T) {
			result := formatScheduleToInterval(tt.interval, tt.unit)
			if result != tt.expected {
				t.Errorf("formatScheduleToInterval(%v, %q) = %q, want %q", tt.interval, tt.unit, result, tt.expected)
			}
		})
	}
}

func TestExtractWindowFromQuery(t *testing.T) {
	t.Run("valid monitor", func(t *testing.T) {
		monitor := map[string]interface{}{
			"inputs": []interface{}{
				map[string]interface{}{
					"search": map[string]interface{}{
						"query": map[string]interface{}{
							"query": map[string]interface{}{
								"bool": map[string]interface{}{
									"filter": []interface{}{
										map[string]interface{}{
											"range": map[string]interface{}{
												"@timestamp": map[string]interface{}{
													"from": "{{period_end}}||-1h",
													"to":   "{{period_end}}",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		window := extractWindowFromQuery(monitor)
		if window != "1h" {
			t.Errorf("expected window '1h', got %q", window)
		}
	})

	t.Run("missing fields", func(t *testing.T) {
		monitor := map[string]interface{}{}
		window := extractWindowFromQuery(monitor)
		if window != "" {
			t.Errorf("expected empty window, got %q", window)
		}
	})
}

func TestParseMonitorToAlertRuleResponse(t *testing.T) {
	monitor := map[string]interface{}{
		"name":    "test-rule",
		"enabled": true,
		"schedule": map[string]interface{}{
			"period": map[string]interface{}{
				"interval": float64(5),
				"unit":     "MINUTES",
			},
		},
		"inputs": []interface{}{
			map[string]interface{}{
				"search": map[string]interface{}{
					"indices": []interface{}{"logs-*"},
					"query": map[string]interface{}{
						"size": 0,
						"query": map[string]interface{}{
							"bool": map[string]interface{}{
								"filter": []interface{}{
									map[string]interface{}{
										"range": map[string]interface{}{
											"@timestamp": map[string]interface{}{
												"from": "{{period_end}}||-1h",
												"to":   "{{period_end}}",
											},
										},
									},
									map[string]interface{}{
										"term": map[string]interface{}{
											osearch.OSComponentID: map[string]interface{}{
												"value": "550e8400-e29b-41d4-a716-446655440000",
											},
										},
									},
									map[string]interface{}{
										"wildcard": map[string]interface{}{
											"log": map[string]interface{}{
												"wildcard": "*error*",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"triggers": []interface{}{
			map[string]interface{}{
				"query_level_trigger": map[string]interface{}{
					"name":     "trigger-test-rule",
					"severity": "1",
					"condition": map[string]interface{}{
						"script": map[string]interface{}{
							"source": "ctx.results[0].hits.total.value > 10",
							"lang":   "painless",
						},
					},
				},
			},
		},
	}

	resp, err := parseMonitorToAlertRuleResponse(monitor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Metadata == nil || resp.Metadata.Name == nil || *resp.Metadata.Name != "test-rule" {
		t.Error("expected metadata name 'test-rule'")
	}
	if resp.Condition == nil || resp.Condition.Enabled == nil || *resp.Condition.Enabled != true {
		t.Error("expected condition enabled=true")
	}
	if resp.Condition.Window == nil || *resp.Condition.Window != "1h" {
		t.Errorf("expected window '1h', got %v", resp.Condition.Window)
	}
	if resp.Condition.Interval == nil || *resp.Condition.Interval != "5m" {
		t.Errorf("expected interval '5m', got %v", resp.Condition.Interval)
	}
	if resp.Source == nil || resp.Source.Query == nil || *resp.Source.Query != "error" {
		t.Errorf("expected source query 'error', got %v", resp.Source)
	}
}

func TestToAlertingRuleRequest(t *testing.T) {
	body := &gen.AlertRuleRequest{
		Metadata: struct {
			ComponentUid   openapi_types.UUID `json:"componentUid"`
			EnvironmentUid openapi_types.UUID `json:"environmentUid"`
			Name           string             `json:"name"`
			Namespace      string             `json:"namespace"`
			ProjectUid     openapi_types.UUID `json:"projectUid"`
		}{
			Name:      "test-rule",
			Namespace: "test-ns",
		},
		Source: struct {
			Query string `json:"query"`
		}{
			Query: "error",
		},
		Condition: struct {
			Enabled   bool                                  `json:"enabled"`
			Interval  string                                `json:"interval"`
			Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
			Threshold float32                               `json:"threshold"`
			Window    string                                `json:"window"`
		}{
			Enabled:   true,
			Window:    "1h",
			Interval:  "5m",
			Operator:  gen.AlertRuleRequestConditionOperatorGt,
			Threshold: 10,
		},
	}

	result := toAlertingRuleRequest(body)
	if result.Metadata.Name != "test-rule" {
		t.Errorf("expected name 'test-rule', got %q", result.Metadata.Name)
	}
	if result.Metadata.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", result.Metadata.Namespace)
	}
	if result.Source.Query != "error" {
		t.Errorf("expected query 'error', got %q", result.Source.Query)
	}
	if result.Condition.Operator != "gt" {
		t.Errorf("expected operator 'gt', got %q", result.Condition.Operator)
	}
	if result.Condition.Threshold != 10 {
		t.Errorf("expected threshold 10, got %v", result.Condition.Threshold)
	}
}

func TestToComponentLogEntry(t *testing.T) {
	t.Run("with UIDs", func(t *testing.T) {
		entry := &osearch.LogEntry{
			Timestamp:       time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			Log:             "test log",
			LogLevel:        "INFO",
			ComponentID:     "550e8400-e29b-41d4-a716-446655440000",
			EnvironmentID:   "550e8400-e29b-41d4-a716-446655440001",
			ProjectID:       "550e8400-e29b-41d4-a716-446655440002",
			ComponentName:   "my-comp",
			EnvironmentName: "dev",
			ProjectName:     "my-proj",
			NamespaceName:   "test-ns",
			PodName:         "my-pod",
			PodNamespace:    "ns-1",
			ContainerName:   "main",
		}

		result := toComponentLogEntry(entry)
		if result.Log == nil || *result.Log != "test log" {
			t.Error("expected log 'test log'")
		}
		if result.Level == nil || *result.Level != "INFO" {
			t.Error("expected level 'INFO'")
		}
		if result.Metadata == nil {
			t.Fatal("expected metadata to be set")
		}
		if result.Metadata.ComponentUid == nil {
			t.Error("expected componentUid to be set")
		}
		if result.Metadata.ProjectUid == nil {
			t.Error("expected projectUid to be set")
		}
		if result.Metadata.EnvironmentUid == nil {
			t.Error("expected environmentUid to be set")
		}
	})

	t.Run("without UIDs", func(t *testing.T) {
		entry := &osearch.LogEntry{
			Log:      "test log",
			LogLevel: "ERROR",
		}

		result := toComponentLogEntry(entry)
		if result.Metadata.ComponentUid != nil {
			t.Error("expected componentUid to be nil")
		}
		if result.Metadata.ProjectUid != nil {
			t.Error("expected projectUid to be nil")
		}
	})
}

func TestParseUUID(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		uid, ok := parseUUID("550e8400-e29b-41d4-a716-446655440000")
		if !ok {
			t.Fatal("expected successful parse")
		}
		if uid.String() != "550e8400-e29b-41d4-a716-446655440000" {
			t.Errorf("unexpected UUID: %s", uid.String())
		}
	})

	t.Run("invalid", func(t *testing.T) {
		_, ok := parseUUID("not-a-uuid")
		if ok {
			t.Error("expected parse to fail")
		}
	})

	t.Run("empty", func(t *testing.T) {
		_, ok := parseUUID("")
		if ok {
			t.Error("expected parse to fail for empty string")
		}
	})
}

func TestStrPtr(t *testing.T) {
	result := strPtr("hello")
	if result == nil || *result != "hello" {
		t.Error("expected non-nil pointer to 'hello'")
	}

	result = strPtr("")
	if result != nil {
		t.Error("expected nil for empty string")
	}
}

func TestPtr(t *testing.T) {
	intVal := ptr(42)
	if intVal == nil || *intVal != 42 {
		t.Error("expected pointer to 42")
	}

	strVal := ptr("test")
	if strVal == nil || *strVal != "test" {
		t.Error("expected pointer to 'test'")
	}
}

// Verify the unused imports are actually used - this is a compile check.
var _ = opensearchapi.Config{}
var _ = opensearch.Config{}
