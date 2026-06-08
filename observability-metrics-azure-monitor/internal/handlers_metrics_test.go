// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/perfmetrics"
)

type fakeClient struct {
	result *perfmetrics.ResourceMetricsResult
	err    error
	params perfmetrics.MetricsQueryParams
}

func (f *fakeClient) GetResourceMetrics(_ context.Context, p perfmetrics.MetricsQueryParams) (*perfmetrics.ResourceMetricsResult, error) {
	f.params = p
	return f.result, f.err
}

func newHandler(c metricsClient) *MetricsHandler {
	return NewMetricsHandler(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHealth(t *testing.T) {
	h := newHandler(&fakeClient{})
	resp, err := h.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, isType := resp.(gen.Health200JSONResponse)
	if !isType {
		t.Fatalf("expected Health200JSONResponse, got %T", resp)
	}
	if ok.Status == nil || *ok.Status != "healthy" {
		t.Errorf("expected status healthy, got %v", ok.Status)
	}
}

func TestQueryMetrics_MissingBody(t *testing.T) {
	h := newHandler(&fakeClient{})
	resp, _ := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: nil})
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryMetrics_MissingNamespace(t *testing.T) {
	h := newHandler(&fakeClient{})
	body := &gen.MetricsQueryRequest{
		Metric:      gen.MetricsQueryRequestMetricResource,
		SearchScope: gen.ComponentSearchScope{Namespace: ""},
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
	}
	resp, _ := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400 for missing namespace, got %T", resp)
	}
}

func TestQueryMetrics_InvalidStep(t *testing.T) {
	h := newHandler(&fakeClient{})
	bad := "notaduration"
	body := &gen.MetricsQueryRequest{
		Metric:      gen.MetricsQueryRequestMetricResource,
		SearchScope: gen.ComponentSearchScope{Namespace: "ns"},
		Step:        &bad,
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
	}
	resp, _ := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400 for invalid step, got %T", resp)
	}
}

func TestQueryMetrics_Resource_PassesScopeAndStep(t *testing.T) {
	fc := &fakeClient{result: &perfmetrics.ResourceMetricsResult{
		CPUUsage: []perfmetrics.TimeValuePoint{{Timestamp: time.Now(), Value: 0.5}},
	}}
	h := newHandler(fc)
	step := "1m"
	compUID := "comp-1"
	body := &gen.MetricsQueryRequest{
		Metric: gen.MetricsQueryRequestMetricResource,
		SearchScope: gen.ComponentSearchScope{
			Namespace:    "dp-acme-dev",
			ComponentUid: &compUID,
		},
		Step:      &step,
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	}
	resp, err := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fc.params.Namespace != "dp-acme-dev" {
		t.Errorf("namespace not passed: %q", fc.params.Namespace)
	}
	if fc.params.ComponentUID != "comp-1" {
		t.Errorf("componentUID not passed: %q", fc.params.ComponentUID)
	}
	if fc.params.Step != time.Minute {
		t.Errorf("step not parsed: %v", fc.params.Step)
	}

	// Serialize the 200 response and confirm cpuUsage is present.
	rec := visitQueryMetrics(t, resp)
	if !strings.Contains(rec.Body.String(), "cpuUsage") {
		t.Errorf("expected cpuUsage in response body: %s", rec.Body.String())
	}
}

func TestQueryMetrics_Resource_ClientError(t *testing.T) {
	fc := &fakeClient{err: context.DeadlineExceeded}
	h := newHandler(fc)
	body := &gen.MetricsQueryRequest{
		Metric:      gen.MetricsQueryRequestMetricResource,
		SearchScope: gen.ComponentSearchScope{Namespace: "ns"},
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
	}
	resp, _ := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if _, ok := resp.(gen.QueryMetrics500JSONResponse); !ok {
		t.Fatalf("expected 500 on client error, got %T", resp)
	}
}

func TestQueryMetrics_HTTP_EmptyWithHeader(t *testing.T) {
	h := newHandler(&fakeClient{})
	body := &gen.MetricsQueryRequest{
		Metric:      gen.MetricsQueryRequestMetricHttp,
		SearchScope: gen.ComponentSearchScope{Namespace: "ns"},
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
	}
	resp, _ := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})

	rec := visitQueryMetrics(t, resp)
	if got := rec.Header().Get("X-OpenChoreo-Adapter-Notice"); got != "http-metrics-not-implemented" {
		t.Errorf("expected not-implemented header, got %q", got)
	}
	if !strings.Contains(rec.Body.String(), "requestCount") {
		t.Errorf("expected http series keys, got: %s", rec.Body.String())
	}
}

func TestQueryRuntimeTopology_EmptyGraph(t *testing.T) {
	h := newHandler(&fakeClient{})
	start := time.Now().Add(-2 * time.Hour)
	end := time.Now()
	body := &gen.RuntimeTopologyRequest{
		SearchScope: gen.ComponentSearchScope{Namespace: "ns"},
		StartTime:   start,
		EndTime:     end,
	}
	resp, err := h.QueryRuntimeTopology(context.Background(), gen.QueryRuntimeTopologyRequestObject{Body: body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wrapped, isType := resp.(runtimeTopologyNotSupportedResponse)
	if !isType {
		t.Fatalf("expected runtimeTopologyNotSupportedResponse, got %T", resp)
	}
	ok := wrapped.QueryRuntimeTopology200JSONResponse
	if ok.Nodes == nil || len(*ok.Nodes) != 0 {
		t.Errorf("expected empty nodes")
	}
	if ok.Edges == nil || len(*ok.Edges) != 0 {
		t.Errorf("expected empty edges")
	}
	if !ok.Summary.StartTime.Equal(start) || !ok.Summary.EndTime.Equal(end) {
		t.Errorf("summary window mismatch")
	}

	// The response must carry the not-supported notice header.
	rec := httptest.NewRecorder()
	if err := resp.VisitQueryRuntimeTopologyResponse(rec); err != nil {
		t.Fatalf("visit error: %v", err)
	}
	if got := rec.Header().Get("X-OpenChoreo-Adapter-Notice"); got != "runtime-topology-not-supported" {
		t.Errorf("expected not-supported header, got %q", got)
	}
}

func TestAlertEndpoints_NotImplemented(t *testing.T) {
	h := newHandler(&fakeClient{})

	cr, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{})
	if _, ok := cr.(gen.CreateAlertRule500JSONResponse); !ok {
		t.Errorf("CreateAlertRule: expected 500, got %T", cr)
	}
	gr, _ := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{})
	if _, ok := gr.(gen.GetAlertRule500JSONResponse); !ok {
		t.Errorf("GetAlertRule: expected 500, got %T", gr)
	}
	ur, _ := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{})
	if _, ok := ur.(gen.UpdateAlertRule500JSONResponse); !ok {
		t.Errorf("UpdateAlertRule: expected 500, got %T", ur)
	}
	dr, _ := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{})
	if _, ok := dr.(gen.DeleteAlertRule500JSONResponse); !ok {
		t.Errorf("DeleteAlertRule: expected 500, got %T", dr)
	}
	wr, _ := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{})
	if _, ok := wr.(gen.HandleAlertWebhook500JSONResponse); !ok {
		t.Errorf("HandleAlertWebhook: expected 500, got %T", wr)
	}
}

// visitQueryMetrics runs the response's Visit method against a recorder.
func visitQueryMetrics(t *testing.T, resp gen.QueryMetricsResponseObject) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := resp.VisitQueryMetricsResponse(rec); err != nil {
		t.Fatalf("visit error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// Confirm the body is valid JSON.
	var v any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	return rec
}
