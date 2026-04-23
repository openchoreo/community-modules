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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/observer"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/prometheus"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestHandler(observerURL string) *MetricsHandler {
	var obsClient *observer.Client
	if observerURL != "" {
		obsClient = observer.NewClient(observerURL)
	}
	return NewMetricsHandler(nil, nil, obsClient, "test-ns", testLogger())
}

// newTestHandlerWithPromClient creates a test handler with a non-nil Prometheus client
// backed by a mock HTTP server, similar to handlers_integration_test.go
func newTestHandlerWithPromClient(t *testing.T) (*MetricsHandler, *httptest.Server) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Simple mock response for Prometheus API
		response := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result":     []interface{}{},
			},
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Logf("failed to encode response: %v", err)
		}
	}))

	promClient, err := prometheus.NewClient(mockServer.URL, testLogger())
	if err != nil {
		t.Fatalf("failed to create prometheus client: %v", err)
	}

	handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())
	return handler, mockServer
}

// --- Health tests ---

func TestHealth_NoPromClient(t *testing.T) {
	handler := newTestHandler("")
	resp, err := handler.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.Health503JSONResponse); !ok {
		t.Fatalf("expected 503 response when promClient is nil, got %T", resp)
	}
}

// --- QueryMetrics tests ---

func TestQueryMetrics_NilBody(t *testing.T) {
	handler := newTestHandler("")
	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Returns 500 because promClient is nil in test handler
	if _, ok := resp.(gen.QueryMetrics500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestQueryMetrics_MissingNamespace(t *testing.T) {
	handler, mockServer := newTestHandlerWithPromClient(t)
	defer mockServer.Close()

	body := gen.MetricsQueryRequest{
		Metric:      gen.Resource,
		StartTime:   time.Now().Add(-1 * time.Hour),
		EndTime:     time.Now(),
		SearchScope: gen.ComponentSearchScope{Namespace: ""},
	}
	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return 400 for missing namespace
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400 response for missing namespace, got %T", resp)
	}
}

func TestQueryMetrics_InvalidStep(t *testing.T) {
	handler, mockServer := newTestHandlerWithPromClient(t)
	defer mockServer.Close()

	invalidStep := "invalid"
	body := gen.MetricsQueryRequest{
		Metric:      gen.Resource,
		StartTime:   time.Now().Add(-1 * time.Hour),
		EndTime:     time.Now(),
		Step:        &invalidStep,
		SearchScope: gen.ComponentSearchScope{Namespace: "test-ns"},
	}
	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return 400 for invalid step format
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400 response for invalid step, got %T", resp)
	}
}

func TestQueryMetrics_NegativeStep(t *testing.T) {
	handler, mockServer := newTestHandlerWithPromClient(t)
	defer mockServer.Close()

	negStep := "-5m"
	body := gen.MetricsQueryRequest{
		Metric:      gen.Resource,
		StartTime:   time.Now().Add(-1 * time.Hour),
		EndTime:     time.Now(),
		Step:        &negStep,
		SearchScope: gen.ComponentSearchScope{Namespace: "test-ns"},
	}
	resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return 400 for negative step
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400 response for negative step, got %T", resp)
	}
}

// --- HandleAlertWebhook tests ---

func TestHandleAlertWebhook_NilBody(t *testing.T) {
	handler := newTestHandler("")
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertWebhook_MissingAlerts(t *testing.T) {
	handler := newTestHandler("")
	body := map[string]interface{}{}
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertWebhook_EmptyAlerts(t *testing.T) {
	handler := newTestHandler("")
	body := map[string]interface{}{
		"alerts": []interface{}{},
	}
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestHandleAlertWebhook_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	handler := newTestHandler(server.URL)

	body := map[string]interface{}{
		"alerts": []interface{}{
			map[string]interface{}{
				"status": "firing",
				"annotations": map[string]interface{}{
					"rule_name":      "high-cpu",
					"rule_namespace": "production",
					"alert_value":    "95.5",
				},
				"startsAt": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	successResp, ok := resp.(gen.HandleAlertWebhook200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if successResp.Status == nil || *successResp.Status != gen.Success {
		t.Errorf("expected status success")
	}
}

func TestHandleAlertWebhook_NonFiringSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	handler := newTestHandler(server.URL)

	body := map[string]interface{}{
		"alerts": []interface{}{
			map[string]interface{}{
				"status": "resolved",
				"annotations": map[string]interface{}{
					"rule_name":      "rule1",
					"rule_namespace": "ns1",
					"alert_value":    "1.0",
				},
			},
		},
	}
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	successResp, ok := resp.(gen.HandleAlertWebhook200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	// Should report 0 forwarded
	if successResp.Message == nil || *successResp.Message != "processed webhook successfully; forwarded 0 firing alert(s)" {
		t.Errorf("unexpected message: %v", successResp.Message)
	}
}

func TestHandleAlertWebhook_FiringCaseInsensitive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	handler := newTestHandler(server.URL)

	body := map[string]interface{}{
		"alerts": []interface{}{
			map[string]interface{}{
				"status": "FIRING",
				"annotations": map[string]interface{}{
					"rule_name":      "rule1",
					"rule_namespace": "ns1",
					"alert_value":    "1.0",
				},
			},
		},
	}
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestHandleAlertWebhook_ForwardError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	handler := newTestHandler(server.URL)

	body := map[string]interface{}{
		"alerts": []interface{}{
			map[string]interface{}{
				"status": "firing",
				"annotations": map[string]interface{}{
					"rule_name":      "rule1",
					"rule_namespace": "ns1",
					"alert_value":    "1.0",
				},
			},
		},
	}
	resp, err := handler.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return 500 when forwarding fails
	errorResp, ok := resp.(gen.HandleAlertWebhook500JSONResponse)
	if !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
	if errorResp.Title == nil || *errorResp.Title != gen.InternalServerError {
		t.Errorf("unexpected error title: %v", errorResp.Title)
	}
	if errorResp.Detail == nil || !strings.Contains(*errorResp.Detail, "failed to forward alert") {
		t.Errorf("unexpected error detail: %v", errorResp.Detail)
	}
}

// --- extractAlertFields tests ---

func TestExtractAlertFields(t *testing.T) {
	tests := []struct {
		name      string
		alert     map[string]interface{}
		wantName  string
		wantNS    string
		wantValue float32
		wantErr   bool
	}{
		{
			name: "valid alert",
			alert: map[string]interface{}{
				"annotations": map[string]interface{}{
					"rule_name":      "high-cpu",
					"rule_namespace": "production",
					"alert_value":    "95.5",
				},
				"startsAt": "2025-06-15T10:30:00Z",
			},
			wantName:  "high-cpu",
			wantNS:    "production",
			wantValue: 95.5,
		},
		{
			name:    "missing annotations",
			alert:   map[string]interface{}{},
			wantErr: true,
		},
		{
			name: "missing rule_name",
			alert: map[string]interface{}{
				"annotations": map[string]interface{}{
					"rule_namespace": "ns1",
					"alert_value":    "1.0",
				},
			},
			wantErr: true,
		},
		{
			name: "missing rule_namespace",
			alert: map[string]interface{}{
				"annotations": map[string]interface{}{
					"rule_name":   "rule1",
					"alert_value": "1.0",
				},
			},
			wantErr: true,
		},
		{
			name: "missing alert_value",
			alert: map[string]interface{}{
				"annotations": map[string]interface{}{
					"rule_name":      "rule1",
					"rule_namespace": "ns1",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid alert_value",
			alert: map[string]interface{}{
				"annotations": map[string]interface{}{
					"rule_name":      "rule1",
					"rule_namespace": "ns1",
					"alert_value":    "not-a-number",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, ns, value, _, err := extractAlertFields(tt.alert)
			if (err != nil) != tt.wantErr {
				t.Fatalf("extractAlertFields() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if name != tt.wantName {
					t.Errorf("name = %q, want %q", name, tt.wantName)
				}
				if ns != tt.wantNS {
					t.Errorf("namespace = %q, want %q", ns, tt.wantNS)
				}
				if value != tt.wantValue {
					t.Errorf("value = %v, want %v", value, tt.wantValue)
				}
			}
		})
	}
}

// --- Helper function tests ---

func TestExtractPromLabelValue(t *testing.T) {
	tests := []struct {
		name      string
		expr      string
		labelName string
		want      string
	}{
		{
			name:      "found",
			expr:      `kube_pod_labels{label_openchoreo_dev_component_uid="a3d8e7f2-4c9b-4a1e-8d3f-5b7c9e2a4d6f",label_openchoreo_dev_project_uid="b4e9f8a3-5d0c-4b2f-9e4a-6c8d0f3b5e7a"}`,
			labelName: "label_openchoreo_dev_component_uid",
			want:      "a3d8e7f2-4c9b-4a1e-8d3f-5b7c9e2a4d6f",
		},
		{
			name:      "not found",
			expr:      `kube_pod_labels{label_openchoreo_dev_namespace="test"}`,
			labelName: "label_openchoreo_dev_component_uid",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPromLabelValue(tt.expr, tt.labelName)
			if got != tt.want {
				t.Errorf("extractPromLabelValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectMetricType(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want gen.AlertRuleResponseSourceMetric
	}{
		{"cpu", `sum(rate(container_cpu_usage_seconds_total{container!=""}[2m]))`, gen.AlertRuleResponseSourceMetricCpuUsage},
		{"memory", `sum(container_memory_working_set_bytes{container!=""})`, gen.AlertRuleResponseSourceMetricMemoryUsage},
		{"unknown", `some_other_metric`, gen.AlertRuleResponseSourceMetricCpuUsage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectMetricType(tt.expr)
			if got != tt.want {
				t.Errorf("detectMetricType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractPromOperatorAndThreshold(t *testing.T) {
	tests := []struct {
		name          string
		expr          string
		wantOperator  string
		wantThreshold *float32
	}{
		{
			name:          "greater than",
			expr:          `(sum(...) / sum(...)) * 100 > 80`,
			wantOperator:  "gt",
			wantThreshold: float32Ptr(80),
		},
		{
			name:          "greater than or equal",
			expr:          `(sum(...) / sum(...)) * 100 >= 90`,
			wantOperator:  "gte",
			wantThreshold: float32Ptr(90),
		},
		{
			name:          "less than",
			expr:          `(sum(...) / sum(...)) * 100 < 50`,
			wantOperator:  "lt",
			wantThreshold: float32Ptr(50),
		},
		{
			name:          "no match",
			expr:          `some random expression`,
			wantOperator:  "",
			wantThreshold: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOp, gotThreshold := extractPromOperatorAndThreshold(tt.expr)
			if gotOp != tt.wantOperator {
				t.Errorf("operator = %q, want %q", gotOp, tt.wantOperator)
			}
			if tt.wantThreshold == nil {
				if gotThreshold != nil {
					t.Errorf("threshold = %v, want nil", *gotThreshold)
				}
			} else if gotThreshold == nil {
				t.Errorf("threshold = nil, want %v", *tt.wantThreshold)
			} else if *gotThreshold != *tt.wantThreshold {
				t.Errorf("threshold = %v, want %v", *gotThreshold, *tt.wantThreshold)
			}
		})
	}
}

func float32Ptr(v float32) *float32 {
	return &v
}

func TestConvertToMetricsTimeSeriesItems(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	points := []prometheus.TimeValuePoint{
		{Time: now.Format(time.RFC3339), Value: 1.5},
		{Time: now.Add(5 * time.Minute).Format(time.RFC3339), Value: 2.5},
	}

	items := convertToMetricsTimeSeriesItems(points)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Value == nil || *items[0].Value != 1.5 {
		t.Errorf("expected first value 1.5, got %v", items[0].Value)
	}
	if items[1].Value == nil || *items[1].Value != 2.5 {
		t.Errorf("expected second value 2.5, got %v", items[1].Value)
	}

	// Test empty input
	emptyItems := convertToMetricsTimeSeriesItems(nil)
	if len(emptyItems) != 0 {
		t.Errorf("expected 0 items for nil input, got %d", len(emptyItems))
	}
}

func TestDerefString(t *testing.T) {
	s := "hello"
	if got := derefString(&s); got != "hello" {
		t.Errorf("derefString(&s) = %q, want %q", got, "hello")
	}
	if got := derefString(nil); got != "" {
		t.Errorf("derefString(nil) = %q, want %q", got, "")
	}
}

// --- CreateAlertRule tests ---

func TestCreateAlertRule_NilBody(t *testing.T) {
	handler := newTestHandler("")
	resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

// --- UpdateAlertRule tests ---

func TestUpdateAlertRule_NilBody(t *testing.T) {
	handler := newTestHandler("")
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

// --- Helper function tests ---

func TestParseUUID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool // true if should return non-nil UUID
	}{
		{"valid UUID", "123e4567-e89b-12d3-a456-426614174000", true},
		{"empty string", "", false},
		{"invalid UUID", "not-a-uuid", false},
		{"partial UUID", "123e4567", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseUUID(tt.input)
			if tt.want && result == nil {
				t.Errorf("expected non-nil UUID for input %q", tt.input)
			}
			if !tt.want && result != nil {
				t.Errorf("expected nil UUID for input %q, got %v", tt.input, result)
			}
		})
	}
}

func TestAlertRuleParamsFromRequest(t *testing.T) {
	componentUID, _ := uuid.Parse("a7f3c9e2-4b1d-4a8e-9c3f-2d5e7b8a1c4f")
	projectUID, _ := uuid.Parse("b8e4d0f3-5c2e-4b9f-0d4a-3e6f8c9b2d5e")
	envUID, _ := uuid.Parse("c9f5e1a4-6d3f-4c0a-1e5b-4f7a9d0c3e6f")

	req := gen.AlertRuleRequest{
		Metadata: struct {
			ComponentUid   openapi_types.UUID `json:"componentUid"`
			EnvironmentUid openapi_types.UUID `json:"environmentUid"`
			Name           string             `json:"name"`
			Namespace      string             `json:"namespace"`
			ProjectUid     openapi_types.UUID `json:"projectUid"`
		}{
			Name:           "test-rule",
			Namespace:      "test-ns",
			ComponentUid:   componentUID,
			ProjectUid:     projectUID,
			EnvironmentUid: envUID,
		},
		Source: struct {
			Metric gen.AlertRuleRequestSourceMetric `json:"metric"`
		}{
			Metric: gen.AlertRuleRequestSourceMetricCpuUsage,
		},
		Condition: struct {
			Enabled   bool                                 `json:"enabled"`
			Interval  string                               `json:"interval"`
			Operator  gen.AlertRuleRequestConditionOperator `json:"operator"`
			Threshold float32                              `json:"threshold"`
			Window    string                               `json:"window"`
		}{
			Enabled:   true,
			Window:    "5m",
			Interval:  "1m",
			Operator:  gen.AlertRuleRequestConditionOperatorGt,
			Threshold: 80.0,
		},
	}

	params := alertRuleParamsFromRequest(req)

	if params.Name != "test-rule" {
		t.Errorf("expected name test-rule, got %s", params.Name)
	}
	if params.Namespace != "test-ns" {
		t.Errorf("expected namespace test-ns, got %s", params.Namespace)
	}
	if params.ComponentUID != componentUID.String() {
		t.Errorf("expected componentUID %s, got %s", componentUID.String(), params.ComponentUID)
	}
	if params.ProjectUID != projectUID.String() {
		t.Errorf("expected projectUID %s, got %s", projectUID.String(), params.ProjectUID)
	}
	if params.EnvironmentUID != envUID.String() {
		t.Errorf("expected environmentUID %s, got %s", envUID.String(), params.EnvironmentUID)
	}
	if params.Metric != "cpu_usage" {
		t.Errorf("expected metric cpu_usage, got %s", params.Metric)
	}
	if !params.Enabled {
		t.Error("expected enabled true")
	}
	if params.Window != "5m" {
		t.Errorf("expected window 5m, got %s", params.Window)
	}
	if params.Interval != "1m" {
		t.Errorf("expected interval 1m, got %s", params.Interval)
	}
	if params.Operator != "gt" {
		t.Errorf("expected operator gt, got %s", params.Operator)
	}
	if params.Threshold != 80.0 {
		t.Errorf("expected threshold 80.0, got %f", params.Threshold)
	}
}

func TestBuildSyncResponse(t *testing.T) {
	resp := buildSyncResponse(gen.Created, "test-rule", "backend-123", gen.Synced)

	if resp.Action == nil || *resp.Action != gen.Created {
		t.Errorf("expected action Created")
	}
	if resp.Status == nil || *resp.Status != gen.Synced {
		t.Errorf("expected status Synced")
	}
	if resp.RuleLogicalId == nil || *resp.RuleLogicalId != "test-rule" {
		t.Errorf("expected ruleLogicalId test-rule, got %v", resp.RuleLogicalId)
	}
	if resp.RuleBackendId == nil || *resp.RuleBackendId != "backend-123" {
		t.Errorf("expected ruleBackendId backend-123, got %v", resp.RuleBackendId)
	}
	if resp.LastSyncedAt == nil || *resp.LastSyncedAt == "" {
		t.Error("expected non-empty lastSyncedAt")
	}
}

func TestPrometheusSpecsAreEqual(t *testing.T) {
	rule1 := &monitoringv1.PrometheusRule{
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name:     "test-group",
					Interval: (*monitoringv1.Duration)(strPtr("1m")),
					Rules: []monitoringv1.Rule{
						{
							Alert: "TestAlert",
							Expr:  intstr.FromString("up == 0"),
						},
					},
				},
			},
		},
	}

	rule2 := &monitoringv1.PrometheusRule{
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name:     "test-group",
					Interval: (*monitoringv1.Duration)(strPtr("1m")),
					Rules: []monitoringv1.Rule{
						{
							Alert: "TestAlert",
							Expr:  intstr.FromString("up == 0"),
						},
					},
				},
			},
		},
	}

	rule3 := &monitoringv1.PrometheusRule{
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name:     "different-group",
					Interval: (*monitoringv1.Duration)(strPtr("2m")),
					Rules: []monitoringv1.Rule{
						{
							Alert: "DifferentAlert",
							Expr:  intstr.FromString("up == 1"),
						},
					},
				},
			},
		},
	}

	if !prometheusSpecsAreEqual(rule1, rule2) {
		t.Error("expected equal specs to return true")
	}

	if prometheusSpecsAreEqual(rule1, rule3) {
		t.Error("expected different specs to return false")
	}
}

func TestBadRequestMetrics(t *testing.T) {
	resp := badRequestMetrics("test error")

	if resp.Title == nil || *resp.Title != gen.BadRequest {
		t.Error("expected title BadRequest")
	}
	if resp.Detail == nil || *resp.Detail != "test error" {
		t.Errorf("expected detail 'test error', got %v", resp.Detail)
	}
}

func TestServerErrorMetrics(t *testing.T) {
	resp := serverErrorMetrics("test error")

	if resp.Title == nil || *resp.Title != gen.InternalServerError {
		t.Error("expected title InternalServerError")
	}
	if resp.Detail == nil || *resp.Detail != "test error" {
		t.Errorf("expected detail 'test error', got %v", resp.Detail)
	}
}

func TestMapPrometheusRuleToAlertRuleResponse(t *testing.T) {
	componentUID := "d0a6f2b5-7e4a-4d1b-2f6c-5a8e0b3d7c9f"
	projectUID := "e1b7a3c6-8f5b-4e2c-3a7d-6b9f1c4e8d0a"
	envUID := "f2c8b4d7-9a6c-4f3d-4b8e-7c0a2d5f9e1b"

	interval := monitoringv1.Duration("1m")
	forDuration := monitoringv1.Duration("5m")

	pr := &monitoringv1.PrometheusRule{
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name:     "test-group",
					Interval: &interval,
					Rules: []monitoringv1.Rule{
						{
							Alert: "TestAlert",
							Expr: intstr.FromString(
								`(sum(rate(container_cpu_usage_seconds_total{` +
									`label_openchoreo_dev_component_uid="` + componentUID + `",` +
									`label_openchoreo_dev_project_uid="` + projectUID + `",` +
									`label_openchoreo_dev_environment_uid="` + envUID + `"` +
									`}[2m])) / sum(...)) * 100 > 80`,
							),
							For: &forDuration,
							Annotations: map[string]string{
								"rule_namespace": "test-namespace",
							},
						},
					},
				},
			},
		},
	}

	resp, err := mapPrometheusRuleToAlertRuleResponse(pr, "test-rule")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}
	if resp.Metadata.Name == nil || *resp.Metadata.Name != "test-rule" {
		t.Errorf("expected name test-rule, got %v", resp.Metadata.Name)
	}
	if resp.Metadata.Namespace == nil || *resp.Metadata.Namespace != "test-namespace" {
		t.Errorf("expected namespace test-namespace, got %v", resp.Metadata.Namespace)
	}
	if resp.Metadata.ComponentUid == nil || resp.Metadata.ComponentUid.String() != componentUID {
		t.Errorf("expected componentUID %s, got %v", componentUID, resp.Metadata.ComponentUid)
	}
	if resp.Metadata.ProjectUid == nil || resp.Metadata.ProjectUid.String() != projectUID {
		t.Errorf("expected projectUID %s, got %v", projectUID, resp.Metadata.ProjectUid)
	}
	if resp.Metadata.EnvironmentUid == nil || resp.Metadata.EnvironmentUid.String() != envUID {
		t.Errorf("expected environmentUID %s, got %v", envUID, resp.Metadata.EnvironmentUid)
	}

	if resp.Source == nil || resp.Source.Metric == nil {
		t.Fatal("expected non-nil source metric")
	}
	if *resp.Source.Metric != gen.AlertRuleResponseSourceMetricCpuUsage {
		t.Errorf("expected metric cpu_usage, got %v", *resp.Source.Metric)
	}

	if resp.Condition == nil {
		t.Fatal("expected non-nil condition")
	}
	if resp.Condition.Enabled == nil || !*resp.Condition.Enabled {
		t.Error("expected enabled true")
	}
	if resp.Condition.Interval == nil || *resp.Condition.Interval != "1m" {
		t.Errorf("expected interval 1m, got %v", resp.Condition.Interval)
	}
	if resp.Condition.Window == nil || *resp.Condition.Window != "5m" {
		t.Errorf("expected window 5m, got %v", resp.Condition.Window)
	}
	if resp.Condition.Operator == nil || *resp.Condition.Operator != "gt" {
		t.Errorf("expected operator gt, got %v", resp.Condition.Operator)
	}
	if resp.Condition.Threshold == nil || *resp.Condition.Threshold != 80.0 {
		t.Errorf("expected threshold 80.0, got %v", resp.Condition.Threshold)
	}
}

func TestMapPrometheusRuleToAlertRuleResponse_NoGroups(t *testing.T) {
	pr := &monitoringv1.PrometheusRule{
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{},
		},
	}

	_, err := mapPrometheusRuleToAlertRuleResponse(pr, "test-rule")
	if err == nil {
		t.Error("expected error for PrometheusRule with no groups")
	}
}

func TestMapPrometheusRuleToAlertRuleResponse_NoRules(t *testing.T) {
	pr := &monitoringv1.PrometheusRule{
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name:  "test-group",
					Rules: []monitoringv1.Rule{},
				},
			},
		},
	}

	_, err := mapPrometheusRuleToAlertRuleResponse(pr, "test-rule")
	if err == nil {
		t.Error("expected error for PrometheusRule with no rules")
	}
}
