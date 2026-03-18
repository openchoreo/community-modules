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
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/openobserve"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestHealth(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
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

func TestQueryTraces_NilBody(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryTraces_EmptyNamespace(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{
		Body: &gen.TracesQueryRequest{
			StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: gen.ComponentSearchScope{
				Namespace: "  ",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryTraces_EndTimeBeforeStartTime(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{
		Body: &gen.TracesQueryRequest{
			StartTime: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			SearchScope: gen.ComponentSearchScope{
				Namespace: "test-ns",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQuerySpansForTrace_NilBody(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "abc123",
		Body:    nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QuerySpansForTrace400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQuerySpansForTrace_EmptyNamespace(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "abc123",
		Body: &gen.TracesQueryRequest{
			StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: gen.ComponentSearchScope{
				Namespace: "",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QuerySpansForTrace400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQuerySpansForTrace_EndTimeBeforeStartTime(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "abc123",
		Body: &gen.TracesQueryRequest{
			StartTime: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			SearchScope: gen.ComponentSearchScope{
				Namespace: "test-ns",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QuerySpansForTrace400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestToTracesQueryParams(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	limit := 50
	sortOrder := gen.TracesQueryRequestSortOrder("asc")
	projectID := "proj-1"
	envID := "env-1"
	compID := "comp-1"

	req := &gen.TracesQueryRequest{
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     &limit,
		SortOrder: &sortOrder,
		SearchScope: gen.ComponentSearchScope{
			Namespace:   "test-ns",
			Project:     &projectID,
			Environment: &envID,
			Component:   &compID,
		},
	}

	params := toTracesQueryParams(req)

	if params.Scope.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", params.Scope.Namespace)
	}
	if params.Scope.ProjectID != "proj-1" {
		t.Errorf("expected projectID 'proj-1', got %q", params.Scope.ProjectID)
	}
	if params.Scope.EnvironmentID != "env-1" {
		t.Errorf("expected environmentID 'env-1', got %q", params.Scope.EnvironmentID)
	}
	if params.Scope.ComponentID != "comp-1" {
		t.Errorf("expected componentID 'comp-1', got %q", params.Scope.ComponentID)
	}
	if params.Limit != 50 {
		t.Errorf("expected limit 50, got %d", params.Limit)
	}
	if params.SortOrder != "asc" {
		t.Errorf("expected sortOrder 'asc', got %q", params.SortOrder)
	}
	if !params.StartTime.Equal(startTime) {
		t.Errorf("expected startTime %v, got %v", startTime, params.StartTime)
	}
	if !params.EndTime.Equal(endTime) {
		t.Errorf("expected endTime %v, got %v", endTime, params.EndTime)
	}
}

func TestToTracesQueryParams_Defaults(t *testing.T) {
	req := &gen.TracesQueryRequest{
		StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		SearchScope: gen.ComponentSearchScope{
			Namespace: "test-ns",
		},
	}

	params := toTracesQueryParams(req)

	if params.Limit != 0 {
		t.Errorf("expected default limit 0, got %d", params.Limit)
	}
	if params.SortOrder != "" {
		t.Errorf("expected empty sortOrder, got %q", params.SortOrder)
	}
	if params.Scope.ProjectID != "" {
		t.Errorf("expected empty projectID, got %q", params.Scope.ProjectID)
	}
	if params.Scope.EnvironmentID != "" {
		t.Errorf("expected empty environmentID, got %q", params.Scope.EnvironmentID)
	}
	if params.Scope.ComponentID != "" {
		t.Errorf("expected empty componentID, got %q", params.Scope.ComponentID)
	}
}

func TestToTracesListResponse(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC)

	result := &openobserve.TracesResult{
		Total:  1,
		TookMs: 15,
		Traces: []openobserve.TraceEntry{
			{
				TraceID:      "trace-1",
				TraceName:    "GET /api/v1/users",
				SpanCount:    5,
				RootSpanID:   "span-root",
				RootSpanName: "GET /api/v1/users",
				RootSpanKind: "SERVER",
				StartTime:    startTime,
				EndTime:      endTime,
				DurationNs:   60000000000,
			},
		},
	}

	resp := toTracesListResponse(result)

	if resp.Total == nil || *resp.Total != 1 {
		t.Errorf("expected total 1, got %v", resp.Total)
	}
	if resp.TookMs == nil || *resp.TookMs != 15 {
		t.Errorf("expected tookMs 15, got %v", resp.TookMs)
	}
	if resp.Traces == nil || len(*resp.Traces) != 1 {
		t.Fatalf("expected 1 trace, got %v", resp.Traces)
	}

	trace := (*resp.Traces)[0]
	if trace.TraceId == nil || *trace.TraceId != "trace-1" {
		t.Errorf("expected traceId 'trace-1', got %v", trace.TraceId)
	}
	if trace.TraceName == nil || *trace.TraceName != "GET /api/v1/users" {
		t.Errorf("expected traceName 'GET /api/v1/users', got %v", trace.TraceName)
	}
	if trace.SpanCount == nil || *trace.SpanCount != 5 {
		t.Errorf("expected spanCount 5, got %v", trace.SpanCount)
	}
	if trace.RootSpanId == nil || *trace.RootSpanId != "span-root" {
		t.Errorf("expected rootSpanId 'span-root', got %v", trace.RootSpanId)
	}
	if trace.RootSpanName == nil || *trace.RootSpanName != "GET /api/v1/users" {
		t.Errorf("expected rootSpanName 'GET /api/v1/users', got %v", trace.RootSpanName)
	}
	if trace.RootSpanKind == nil || *trace.RootSpanKind != "SERVER" {
		t.Errorf("expected rootSpanKind 'SERVER', got %v", trace.RootSpanKind)
	}
	if trace.DurationNs == nil || *trace.DurationNs != 60000000000 {
		t.Errorf("expected durationNs 60000000000, got %v", trace.DurationNs)
	}
}

func TestToTracesListResponse_Empty(t *testing.T) {
	result := &openobserve.TracesResult{
		Total:  0,
		TookMs: 5,
		Traces: []openobserve.TraceEntry{},
	}

	resp := toTracesListResponse(result)

	if resp.Total == nil || *resp.Total != 0 {
		t.Errorf("expected total 0, got %v", resp.Total)
	}
	if resp.Traces == nil || len(*resp.Traces) != 0 {
		t.Errorf("expected 0 traces, got %v", resp.Traces)
	}
}

func TestToSpansListResponse(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC)

	result := &openobserve.SpansResult{
		Total:  1,
		TookMs: 10,
		Spans: []openobserve.SpanEntry{
			{
				SpanID:       "span-1",
				SpanName:     "db.query",
				SpanKind:     "CLIENT",
				StartTime:    startTime,
				EndTime:      endTime,
				DurationNs:   1000000000,
				ParentSpanID: "span-root",
			},
		},
	}

	resp := toSpansListResponse(result)

	if resp.Total == nil || *resp.Total != 1 {
		t.Errorf("expected total 1, got %v", resp.Total)
	}
	if resp.TookMs == nil || *resp.TookMs != 10 {
		t.Errorf("expected tookMs 10, got %v", resp.TookMs)
	}
	if resp.Spans == nil || len(*resp.Spans) != 1 {
		t.Fatalf("expected 1 span, got %v", resp.Spans)
	}

	span := (*resp.Spans)[0]
	if span.SpanId == nil || *span.SpanId != "span-1" {
		t.Errorf("expected spanId 'span-1', got %v", span.SpanId)
	}
	if span.SpanName == nil || *span.SpanName != "db.query" {
		t.Errorf("expected spanName 'db.query', got %v", span.SpanName)
	}
	if span.SpanKind == nil || *span.SpanKind != "CLIENT" {
		t.Errorf("expected spanKind 'CLIENT', got %v", span.SpanKind)
	}
	if span.ParentSpanId == nil || *span.ParentSpanId != "span-root" {
		t.Errorf("expected parentSpanId 'span-root', got %v", span.ParentSpanId)
	}
	if span.DurationNs == nil || *span.DurationNs != 1000000000 {
		t.Errorf("expected durationNs 1000000000, got %v", span.DurationNs)
	}
}

func TestToSpanDetailsResponse(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC)

	span := &openobserve.SpanDetail{
		SpanID:       "span-1",
		SpanName:     "db.query",
		SpanKind:     "CLIENT",
		StartTime:    startTime,
		EndTime:      endTime,
		DurationNs:   1000000000,
		ParentSpanID: "span-root",
		Attributes: []openobserve.SpanAttribute{
			{Key: "http.method", Value: "GET"},
			{Key: "http.status_code", Value: "200"},
		},
		ResourceAttributes: []openobserve.SpanAttribute{
			{Key: "service.name", Value: "my-service"},
		},
	}

	resp := toSpanDetailsResponse(span)

	if resp.SpanId == nil || *resp.SpanId != "span-1" {
		t.Errorf("expected spanId 'span-1', got %v", resp.SpanId)
	}
	if resp.SpanName == nil || *resp.SpanName != "db.query" {
		t.Errorf("expected spanName 'db.query', got %v", resp.SpanName)
	}
	if resp.SpanKind == nil || *resp.SpanKind != "CLIENT" {
		t.Errorf("expected spanKind 'CLIENT', got %v", resp.SpanKind)
	}
	if resp.ParentSpanId == nil || *resp.ParentSpanId != "span-root" {
		t.Errorf("expected parentSpanId 'span-root', got %v", resp.ParentSpanId)
	}
	if resp.DurationNs == nil || *resp.DurationNs != 1000000000 {
		t.Errorf("expected durationNs 1000000000, got %v", resp.DurationNs)
	}
	if resp.Attributes == nil || len(*resp.Attributes) != 2 {
		t.Fatalf("expected 2 attributes, got %v", resp.Attributes)
	}
	attr0 := (*resp.Attributes)[0]
	if attr0.Key == nil || *attr0.Key != "http.method" {
		t.Errorf("expected attribute key 'http.method', got %v", attr0.Key)
	}
	if attr0.Value == nil || *attr0.Value != "GET" {
		t.Errorf("expected attribute value 'GET', got %v", attr0.Value)
	}
	if resp.ResourceAttributes == nil || len(*resp.ResourceAttributes) != 1 {
		t.Fatalf("expected 1 resource attribute, got %v", resp.ResourceAttributes)
	}
	resAttr0 := (*resp.ResourceAttributes)[0]
	if resAttr0.Key == nil || *resAttr0.Key != "service.name" {
		t.Errorf("expected resource attribute key 'service.name', got %v", resAttr0.Key)
	}
}

func TestToSpanDetailsResponse_EmptyAttributes(t *testing.T) {
	span := &openobserve.SpanDetail{
		SpanID:             "span-1",
		SpanName:           "test",
		Attributes:         []openobserve.SpanAttribute{},
		ResourceAttributes: []openobserve.SpanAttribute{},
	}

	resp := toSpanDetailsResponse(span)

	if resp.Attributes == nil || len(*resp.Attributes) != 0 {
		t.Errorf("expected 0 attributes, got %v", resp.Attributes)
	}
	if resp.ResourceAttributes == nil || len(*resp.ResourceAttributes) != 0 {
		t.Errorf("expected 0 resource attributes, got %v", resp.ResourceAttributes)
	}
}

func TestPtr(t *testing.T) {
	v := ptr(42)
	if v == nil || *v != 42 {
		t.Errorf("expected pointer to 42, got %v", v)
	}

	s := ptr("hello")
	if s == nil || *s != "hello" {
		t.Errorf("expected pointer to 'hello', got %v", s)
	}
}

func TestQueryTraces_Success(t *testing.T) {
	startNs := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano()
	endNs := time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC).UnixNano()

	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openobserve.OpenObserveResponse{
			Took:  5,
			Total: 2,
			Hits: []map[string]interface{}{
				{
					"trace_id":                    "trace-1",
					"span_id":                     "span-root",
					"operation_name":              "GET /api/v1/users",
					"span_kind":                   "SERVER",
					"start_time":                  json.Number(fmt.Sprintf("%d", startNs)),
					"end_time":                    json.Number(fmt.Sprintf("%d", endNs)),
					"reference_parent_span_id":    "",
					"service_openchoreo_dev_namespace": "test-ns",
				},
				{
					"trace_id":                 "trace-1",
					"span_id":                  "span-child",
					"operation_name":           "db.query",
					"span_kind":                "CLIENT",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs+1000)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs-1000)),
					"reference_parent_span_id": "span-root",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		// Use json.Number-compatible encoding
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewTracingHandler(client, testLogger())

	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{
		Body: &gen.TracesQueryRequest{
			StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: gen.ComponentSearchScope{
				Namespace: "test-ns",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.QueryTraces200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestQuerySpansForTrace_Success(t *testing.T) {
	startNs := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano()
	endNs := time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC).UnixNano()

	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openobserve.OpenObserveResponse{
			Took:  3,
			Total: 1,
			Hits: []map[string]interface{}{
				{
					"span_id":                  "span-1",
					"operation_name":           "db.query",
					"span_kind":                "CLIENT",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs)),
					"duration":                 json.Number(fmt.Sprintf("%d", endNs-startNs)),
					"reference_parent_span_id": "span-root",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewTracingHandler(client, testLogger())

	resp, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "trace-1",
		Body: &gen.TracesQueryRequest{
			StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: gen.ComponentSearchScope{
				Namespace: "test-ns",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.QuerySpansForTrace200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestGetSpanDetailsForTrace_Success(t *testing.T) {
	startNs := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixNano()
	endNs := time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC).UnixNano()

	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openobserve.OpenObserveResponse{
			Took:  2,
			Total: 1,
			Hits: []map[string]interface{}{
				{
					"span_id":                  "span-1",
					"operation_name":           "db.query",
					"span_kind":                "CLIENT",
					"start_time":               json.Number(fmt.Sprintf("%d", startNs)),
					"end_time":                 json.Number(fmt.Sprintf("%d", endNs)),
					"duration":                 json.Number(fmt.Sprintf("%d", endNs-startNs)),
					"reference_parent_span_id": "span-root",
					"http.method":              "GET",
					"service.name":             "my-service",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewTracingHandler(client, testLogger())

	resp, err := handler.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
		TraceId: "trace-1",
		SpanId:  "span-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	detailResp, ok := resp.(gen.GetSpanDetailsForTrace200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if detailResp.SpanId == nil || *detailResp.SpanId != "span-1" {
		t.Errorf("expected spanId 'span-1', got %v", detailResp.SpanId)
	}
	if detailResp.SpanName == nil || *detailResp.SpanName != "db.query" {
		t.Errorf("expected spanName 'db.query', got %v", detailResp.SpanName)
	}
}

func TestGetSpanDetailsForTrace_NotFound(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openobserve.OpenObserveResponse{
			Took:  1,
			Total: 0,
			Hits:  []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewTracingHandler(client, testLogger())

	resp, err := handler.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
		TraceId: "trace-1",
		SpanId:  "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.GetSpanDetailsForTrace500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestQueryTraces_ServerError(t *testing.T) {
	ooServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer ooServer.Close()

	client := openobserve.NewClient(ooServer.URL, "default", "default", "admin", "pass", testLogger())
	handler := NewTracingHandler(client, testLogger())

	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{
		Body: &gen.TracesQueryRequest{
			StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			SearchScope: gen.ComponentSearchScope{
				Namespace: "test-ns",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := resp.(gen.QueryTraces500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}
