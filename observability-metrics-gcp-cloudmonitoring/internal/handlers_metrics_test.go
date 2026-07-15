// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/cloudmonitoring"
)

type fakeMetricsClient struct {
	params cloudmonitoring.MetricsQueryParams
	result *cloudmonitoring.ResourceMetricsResult
	err    error
}

func (f *fakeMetricsClient) GetResourceMetrics(_ context.Context, p cloudmonitoring.MetricsQueryParams) (*cloudmonitoring.ResourceMetricsResult, error) {
	f.params = p
	return f.result, f.err
}

func newTestHandler(client metricsClient) *MetricsHandler {
	return NewMetricsHandler(client, slog.New(slog.DiscardHandler))
}

func validQueryRequest() gen.QueryMetricsRequestObject {
	start := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	return gen.QueryMetricsRequestObject{
		Body: &gen.MetricsQueryRequest{
			Metric:      gen.MetricsQueryRequestMetricResource,
			SearchScope: gen.ComponentSearchScope{Namespace: "default"},
			StartTime:   start,
			EndTime:     end,
		},
	}
}

func TestQueryMetricsValidation(t *testing.T) {
	h := newTestHandler(&fakeMetricsClient{})

	tests := []struct {
		name   string
		mutate func(*gen.QueryMetricsRequestObject)
	}{
		{"nil body", func(r *gen.QueryMetricsRequestObject) { r.Body = nil }},
		{"missing namespace", func(r *gen.QueryMetricsRequestObject) { r.Body.SearchScope.Namespace = "" }},
		{"zero start time", func(r *gen.QueryMetricsRequestObject) { r.Body.StartTime = time.Time{} }},
		{"end before start", func(r *gen.QueryMetricsRequestObject) { r.Body.EndTime = r.Body.StartTime.Add(-time.Minute) }},
		{"bad step", func(r *gen.QueryMetricsRequestObject) { s := "5x"; r.Body.Step = &s }},
		{"negative step", func(r *gen.QueryMetricsRequestObject) { s := "-5m"; r.Body.Step = &s }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validQueryRequest()
			tt.mutate(&req)
			resp, err := h.QueryMetrics(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
				t.Fatalf("response = %T, want 400", resp)
			}
		})
	}
}

func TestQueryMetricsResourceHappyPath(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 11, 5, 0, 0, time.UTC)
	client := &fakeMetricsClient{result: &cloudmonitoring.ResourceMetricsResult{
		CPUUsage:    []cloudmonitoring.TimeValuePoint{{Timestamp: t0, Value: 0.25}},
		MemoryUsage: []cloudmonitoring.TimeValuePoint{{Timestamp: t0, Value: 1024}},
	}}
	h := newTestHandler(client)

	req := validQueryRequest()
	uid := "c-uid"
	req.Body.SearchScope.ComponentUid = &uid
	step := "1m"
	req.Body.Step = &step

	resp, err := h.QueryMetrics(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.params.ComponentUID != "c-uid" || client.params.Namespace != "default" {
		t.Errorf("scope not forwarded: %+v", client.params)
	}
	if client.params.Step != time.Minute {
		t.Errorf("step = %v, want 1m", client.params.Step)
	}

	rec := httptest.NewRecorder()
	if err := resp.VisitQueryMetricsResponse(rec); err != nil {
		t.Fatalf("visit: %v", err)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"cpuUsage"`, `"memoryUsage"`, `0.25`, `1024`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestQueryMetricsBackendError(t *testing.T) {
	h := newTestHandler(&fakeMetricsClient{err: errors.New("boom")})
	resp, err := h.QueryMetrics(context.Background(), validQueryRequest())
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if _, ok := resp.(gen.QueryMetrics500JSONResponse); !ok {
		t.Fatalf("response = %T, want 500", resp)
	}
}

func TestQueryMetricsHTTPNotImplementedNotice(t *testing.T) {
	h := newTestHandler(&fakeMetricsClient{})
	req := validQueryRequest()
	req.Body.Metric = gen.MetricsQueryRequestMetricHttp

	resp, err := h.QueryMetrics(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec := httptest.NewRecorder()
	if err := resp.VisitQueryMetricsResponse(rec); err != nil {
		t.Fatalf("visit: %v", err)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-OpenChoreo-Adapter-Notice"); got != "http-metrics-not-implemented" {
		t.Errorf("notice header = %q", got)
	}
}

func TestQueryRuntimeTopologyNotSupported(t *testing.T) {
	h := newTestHandler(&fakeMetricsClient{})
	resp, err := h.QueryRuntimeTopology(context.Background(), gen.QueryRuntimeTopologyRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e, ok := resp.(gen.QueryRuntimeTopology501JSONResponse)
	if !ok {
		t.Fatalf("response = %T, want 501", resp)
	}
	if e.ErrorCode == nil || *e.ErrorCode != errCodeNotImplemented {
		t.Errorf("errorCode = %v", e.ErrorCode)
	}
}

func TestAlertEndpointsNotImplemented(t *testing.T) {
	h := newTestHandler(&fakeMetricsClient{})
	ctx := context.Background()

	if resp, _ := h.CreateAlertRule(ctx, gen.CreateAlertRuleRequestObject{}); !isNotImplemented500(t, resp) {
		t.Errorf("CreateAlertRule = %T", resp)
	}
	if resp, _ := h.GetAlertRule(ctx, gen.GetAlertRuleRequestObject{}); !isNotImplemented500(t, resp) {
		t.Errorf("GetAlertRule = %T", resp)
	}
	if resp, _ := h.UpdateAlertRule(ctx, gen.UpdateAlertRuleRequestObject{}); !isNotImplemented500(t, resp) {
		t.Errorf("UpdateAlertRule = %T", resp)
	}
	if resp, _ := h.DeleteAlertRule(ctx, gen.DeleteAlertRuleRequestObject{}); !isNotImplemented500(t, resp) {
		t.Errorf("DeleteAlertRule = %T", resp)
	}
	if resp, _ := h.HandleAlertWebhook(ctx, gen.HandleAlertWebhookRequestObject{}); !isNotImplemented500(t, resp) {
		t.Errorf("HandleAlertWebhook = %T", resp)
	}
}

func isNotImplemented500(t *testing.T, resp any) bool {
	t.Helper()
	var e gen.ErrorResponse
	switch r := resp.(type) {
	case gen.CreateAlertRule500JSONResponse:
		e = gen.ErrorResponse(r)
	case gen.GetAlertRule500JSONResponse:
		e = gen.ErrorResponse(r)
	case gen.UpdateAlertRule500JSONResponse:
		e = gen.ErrorResponse(r)
	case gen.DeleteAlertRule500JSONResponse:
		e = gen.ErrorResponse(r)
	case gen.HandleAlertWebhook500JSONResponse:
		e = gen.ErrorResponse(r)
	default:
		return false
	}
	return e.ErrorCode != nil && *e.ErrorCode == errCodeNotImplemented
}
