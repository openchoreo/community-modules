// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/opensearch"
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

func TestToTracesRequestParams(t *testing.T) {
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

	params := toTracesRequestParams(req)

	if params.ProjectUID != "proj-1" {
		t.Errorf("expected projectUID 'proj-1', got %q", params.ProjectUID)
	}
	if params.EnvironmentUID != "env-1" {
		t.Errorf("expected environmentUID 'env-1', got %q", params.EnvironmentUID)
	}
	if len(params.ComponentUIDs) != 1 || params.ComponentUIDs[0] != "comp-1" {
		t.Errorf("expected componentUIDs ['comp-1'], got %v", params.ComponentUIDs)
	}
	if params.Limit != 50 {
		t.Errorf("expected limit 50, got %d", params.Limit)
	}
	if params.SortOrder != "asc" {
		t.Errorf("expected sortOrder 'asc', got %q", params.SortOrder)
	}
	if params.StartTime != "2025-01-01T00:00:00Z" {
		t.Errorf("expected startTime '2025-01-01T00:00:00Z', got %q", params.StartTime)
	}
	if params.EndTime != "2025-01-02T00:00:00Z" {
		t.Errorf("expected endTime '2025-01-02T00:00:00Z', got %q", params.EndTime)
	}
}

func TestToTracesRequestParams_Defaults(t *testing.T) {
	req := &gen.TracesQueryRequest{
		StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		SearchScope: gen.ComponentSearchScope{
			Namespace: "test-ns",
		},
	}

	params := toTracesRequestParams(req)

	if params.Limit != 20 {
		t.Errorf("expected default limit 20, got %d", params.Limit)
	}
	if params.SortOrder != "desc" {
		t.Errorf("expected default sortOrder 'desc', got %q", params.SortOrder)
	}
	if params.ProjectUID != "" {
		t.Errorf("expected empty projectUID, got %q", params.ProjectUID)
	}
	if params.EnvironmentUID != "" {
		t.Errorf("expected empty environmentUID, got %q", params.EnvironmentUID)
	}
	if len(params.ComponentUIDs) != 0 {
		t.Errorf("expected empty componentUIDs, got %v", params.ComponentUIDs)
	}
}

func TestToTracesListResponse(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC)

	traces := []traceEntry{
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
			HasErrors:    false,
		},
	}

	resp := toTracesListResponse(traces, 1, 15)

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
	if trace.HasErrors == nil || *trace.HasErrors != false {
		t.Errorf("expected hasErrors false, got %v", trace.HasErrors)
	}
}

func TestToTracesListResponse_Empty(t *testing.T) {
	resp := toTracesListResponse([]traceEntry{}, 0, 5)

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

	spans := []opensearch.Span{
		{
			SpanID:              "span-1",
			Name:                "db.query",
			SpanKind:            "CLIENT",
			StartTime:           startTime,
			EndTime:             endTime,
			DurationNanoseconds: 1000000000,
			ParentSpanID:        "span-root",
			Status:              "ok",
		},
	}

	resp := toSpansListResponse(spans, 1, 10)

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

	span := &opensearch.Span{
		SpanID:              "span-1",
		Name:                "db.query",
		SpanKind:            "CLIENT",
		StartTime:           startTime,
		EndTime:             endTime,
		DurationNanoseconds: 1000000000,
		ParentSpanID:        "span-root",
		Status:              "ok",
		Attributes: map[string]interface{}{
			"http.method": "GET",
		},
		ResourceAttributes: map[string]interface{}{
			"service.name": "my-service",
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
	if resp.Attributes == nil || len(*resp.Attributes) != 1 {
		t.Fatalf("expected 1 attribute, got %v", resp.Attributes)
	}
	if resp.ResourceAttributes == nil || len(*resp.ResourceAttributes) != 1 {
		t.Fatalf("expected 1 resource attribute, got %v", resp.ResourceAttributes)
	}
}

func TestToSpanDetailsResponse_EmptyAttributes(t *testing.T) {
	span := &opensearch.Span{
		SpanID: "span-1",
		Name:   "test",
	}

	resp := toSpanDetailsResponse(span)

	if resp.Attributes == nil || len(*resp.Attributes) != 0 {
		t.Errorf("expected 0 attributes, got %v", resp.Attributes)
	}
	if resp.ResourceAttributes == nil || len(*resp.ResourceAttributes) != 0 {
		t.Errorf("expected 0 resource attributes, got %v", resp.ResourceAttributes)
	}
}

func TestBuildTraceFromBucket(t *testing.T) {
	bucket := opensearch.TraceBucket{
		Key:      "trace-abc",
		DocCount: 5,
		EarliestSpan: opensearch.AggTopHitsValue{
			Hits: struct {
				Hits []opensearch.Hit `json:"hits"`
			}{
				Hits: []opensearch.Hit{
					{Source: map[string]interface{}{
						"spanId":    "span-1",
						"name":      "GET /api",
						"kind":      "SERVER",
						"startTime": "2025-06-01T12:00:00.000000000Z",
					}},
				},
			},
		},
		RootSpan: opensearch.AggFilteredTopHits{
			DocCount: 1,
			Hit: opensearch.AggTopHitsValue{
				Hits: struct {
					Hits []opensearch.Hit `json:"hits"`
				}{
					Hits: []opensearch.Hit{
						{Source: map[string]interface{}{
							"spanId": "span-root",
							"name":   "root-operation",
							"kind":   "SERVER",
						}},
					},
				},
			},
		},
		LatestSpan: opensearch.AggTopHitsValue{
			Hits: struct {
				Hits []opensearch.Hit `json:"hits"`
			}{
				Hits: []opensearch.Hit{
					{Source: map[string]interface{}{
						"endTime": "2025-06-01T12:00:05.000000000Z",
					}},
				},
			},
		},
		ErrorSpanCount: opensearch.AggFilteredTopHits{
			DocCount: 2,
		},
	}

	trace := buildTraceFromBucket(bucket)

	if trace.TraceID != "trace-abc" {
		t.Errorf("expected traceID 'trace-abc', got %q", trace.TraceID)
	}
	if trace.SpanCount != 5 {
		t.Errorf("expected spanCount 5, got %d", trace.SpanCount)
	}
	if trace.RootSpanID != "span-root" {
		t.Errorf("expected rootSpanID 'span-root', got %q", trace.RootSpanID)
	}
	if trace.RootSpanName != "root-operation" {
		t.Errorf("expected rootSpanName 'root-operation', got %q", trace.RootSpanName)
	}
	if trace.TraceName != "root-operation" {
		t.Errorf("expected traceName 'root-operation', got %q", trace.TraceName)
	}
	if !trace.HasErrors {
		t.Error("expected hasErrors true (error_span_count=2)")
	}
	if trace.DurationNs <= 0 {
		t.Errorf("expected positive durationNs, got %d", trace.DurationNs)
	}
}

func TestBuildTraceFromBucket_NoRootSpan(t *testing.T) {
	bucket := opensearch.TraceBucket{
		Key:      "trace-abc",
		DocCount: 3,
		EarliestSpan: opensearch.AggTopHitsValue{
			Hits: struct {
				Hits []opensearch.Hit `json:"hits"`
			}{
				Hits: []opensearch.Hit{
					{Source: map[string]interface{}{
						"spanId":    "span-earliest",
						"name":      "earliest-op",
						"kind":      "CLIENT",
						"startTime": "2025-06-01T12:00:00Z",
					}},
				},
			},
		},
		RootSpan: opensearch.AggFilteredTopHits{
			DocCount: 0,
		},
		LatestSpan: opensearch.AggTopHitsValue{
			Hits: struct {
				Hits []opensearch.Hit `json:"hits"`
			}{
				Hits: []opensearch.Hit{
					{Source: map[string]interface{}{
						"endTime": "2025-06-01T12:00:03Z",
					}},
				},
			},
		},
		ErrorSpanCount: opensearch.AggFilteredTopHits{DocCount: 0},
	}

	trace := buildTraceFromBucket(bucket)

	// Should fall back to earliest span when no root span
	if trace.RootSpanID != "span-earliest" {
		t.Errorf("expected rootSpanID to fall back to earliest span, got %q", trace.RootSpanID)
	}
	if trace.TraceName != "earliest-op" {
		t.Errorf("expected traceName to fall back to earliest span name, got %q", trace.TraceName)
	}
	if trace.HasErrors {
		t.Error("expected hasErrors false")
	}
}

func TestParseTracesAggregation(t *testing.T) {
	raw := json.RawMessage(`{
		"trace_count": {"value": 42},
		"traces": {
			"buckets": [
				{
					"key": "trace-1",
					"doc_count": 3,
					"earliest_span": {"hits": {"hits": []}},
					"root_span": {"doc_count": 0, "hit": {"hits": {"hits": []}}},
					"latest_span": {"hits": {"hits": []}},
					"error_span_count": {"doc_count": 0}
				}
			]
		}
	}`)

	result, err := parseTracesAggregation(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TraceCount.Value != 42 {
		t.Errorf("expected trace count 42, got %d", result.TraceCount.Value)
	}
	if len(result.Traces.Buckets) != 1 {
		t.Errorf("expected 1 bucket, got %d", len(result.Traces.Buckets))
	}
	if result.Traces.Buckets[0].Key != "trace-1" {
		t.Errorf("expected bucket key 'trace-1', got %q", result.Traces.Buckets[0].Key)
	}
}

func TestParseTracesAggregation_Empty(t *testing.T) {
	result, err := parseTracesAggregation(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TraceCount.Value != 0 {
		t.Errorf("expected trace count 0, got %d", result.TraceCount.Value)
	}
}

func TestParseTracesAggregation_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{invalid json`)

	_, err := parseTracesAggregation(raw)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBuildTraceFromBucket_EmptyBucket(t *testing.T) {
	bucket := opensearch.TraceBucket{
		Key:      "4bf92f3577b34da6a3ce929d0e0e4736",
		DocCount: 0,
	}

	trace := buildTraceFromBucket(bucket)

	if trace.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected traceID '4bf92f3577b34da6a3ce929d0e0e4736', got %q", trace.TraceID)
	}
	if trace.SpanCount != 0 {
		t.Errorf("expected spanCount 0, got %d", trace.SpanCount)
	}
	if !trace.StartTime.IsZero() {
		t.Error("expected zero startTime for empty bucket")
	}
	if !trace.EndTime.IsZero() {
		t.Error("expected zero endTime for empty bucket")
	}
	if trace.DurationNs != 0 {
		t.Errorf("expected durationNs 0, got %d", trace.DurationNs)
	}
	if trace.RootSpanID != "" {
		t.Errorf("expected empty rootSpanID, got %q", trace.RootSpanID)
	}
	if trace.HasErrors {
		t.Error("expected hasErrors false for empty bucket")
	}
}

func TestBuildTraceFromBucket_NilSourceInHits(t *testing.T) {
	bucket := opensearch.TraceBucket{
		Key:      "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6",
		DocCount: 1,
		EarliestSpan: opensearch.AggTopHitsValue{
			Hits: struct {
				Hits []opensearch.Hit `json:"hits"`
			}{
				Hits: []opensearch.Hit{
					{Source: nil},
				},
			},
		},
		RootSpan: opensearch.AggFilteredTopHits{
			DocCount: 1,
			Hit: opensearch.AggTopHitsValue{
				Hits: struct {
					Hits []opensearch.Hit `json:"hits"`
				}{
					Hits: []opensearch.Hit{
						{Source: nil},
					},
				},
			},
		},
		LatestSpan: opensearch.AggTopHitsValue{
			Hits: struct {
				Hits []opensearch.Hit `json:"hits"`
			}{
				Hits: []opensearch.Hit{
					{Source: nil},
				},
			},
		},
	}

	trace := buildTraceFromBucket(bucket)

	if trace.TraceID != "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6" {
		t.Errorf("expected traceID 'a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6', got %q", trace.TraceID)
	}
	if trace.RootSpanID != "" {
		t.Errorf("expected empty rootSpanID when source is nil, got %q", trace.RootSpanID)
	}
	if !trace.StartTime.IsZero() {
		t.Error("expected zero startTime when source is nil")
	}
}

func TestBuildTraceFromBucket_InvalidTimestamps(t *testing.T) {
	bucket := opensearch.TraceBucket{
		Key:      "e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2",
		DocCount: 1,
		EarliestSpan: opensearch.AggTopHitsValue{
			Hits: struct {
				Hits []opensearch.Hit `json:"hits"`
			}{
				Hits: []opensearch.Hit{
					{Source: map[string]interface{}{
						"startTime": "not-a-timestamp",
					}},
				},
			},
		},
		LatestSpan: opensearch.AggTopHitsValue{
			Hits: struct {
				Hits []opensearch.Hit `json:"hits"`
			}{
				Hits: []opensearch.Hit{
					{Source: map[string]interface{}{
						"endTime": "also-not-a-timestamp",
					}},
				},
			},
		},
	}

	trace := buildTraceFromBucket(bucket)

	if !trace.StartTime.IsZero() {
		t.Error("expected zero startTime for invalid timestamp")
	}
	if !trace.EndTime.IsZero() {
		t.Error("expected zero endTime for invalid timestamp")
	}
	if trace.DurationNs != 0 {
		t.Errorf("expected durationNs 0 for invalid timestamps, got %d", trace.DurationNs)
	}
}

func TestToSpansListResponse_Empty(t *testing.T) {
	resp := toSpansListResponse([]opensearch.Span{}, 0, 3)

	if resp.Total == nil || *resp.Total != 0 {
		t.Errorf("expected total 0, got %v", resp.Total)
	}
	if resp.TookMs == nil || *resp.TookMs != 3 {
		t.Errorf("expected tookMs 3, got %v", resp.TookMs)
	}
	if resp.Spans == nil || len(*resp.Spans) != 0 {
		t.Errorf("expected 0 spans, got %v", resp.Spans)
	}
}

func TestToSpanDetailsResponse_MultipleAttributes(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 1, 12, 0, 1, 0, time.UTC)

	span := &opensearch.Span{
		SpanID:              "a1b2c3d4e5f6a7b8",
		Name:                "POST /api/v1/data",
		SpanKind:            "SERVER",
		StartTime:           startTime,
		EndTime:             endTime,
		DurationNanoseconds: 1000000000,
		ParentSpanID:        "",
		Status:              "ok",
		Attributes: map[string]interface{}{
			"http.method":      "POST",
			"http.status_code": 200,
			"http.url":         "/api/v1/data",
		},
		ResourceAttributes: map[string]interface{}{
			"service.name":      "my-service",
			"service.namespace": "production",
		},
	}

	resp := toSpanDetailsResponse(span)

	if resp.Attributes == nil || len(*resp.Attributes) != 3 {
		t.Fatalf("expected 3 attributes, got %d", len(*resp.Attributes))
	}
	if resp.ResourceAttributes == nil || len(*resp.ResourceAttributes) != 2 {
		t.Fatalf("expected 2 resource attributes, got %d", len(*resp.ResourceAttributes))
	}

	// Verify non-string attribute values are converted to string representation
	found := false
	for _, attr := range *resp.Attributes {
		if attr.Key != nil && *attr.Key == "http.status_code" {
			if attr.Value == nil || *attr.Value != "200" {
				t.Errorf("expected http.status_code value '200', got %v", attr.Value)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected to find http.status_code attribute")
	}
}

func TestToTracesRequestParams_ZeroLimit(t *testing.T) {
	zeroLimit := 0
	req := &gen.TracesQueryRequest{
		StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Limit:     &zeroLimit,
		SearchScope: gen.ComponentSearchScope{
			Namespace: "test-ns",
		},
	}

	params := toTracesRequestParams(req)

	// Explicit zero should be treated as default (20)
	if params.Limit != 20 {
		t.Errorf("expected limit 20 for explicit zero, got %d", params.Limit)
	}
}

func TestParseTracesAggregation_MultipleBuckets(t *testing.T) {
	raw := json.RawMessage(`{
		"trace_count": {"value": 3},
		"traces": {
			"buckets": [
				{
					"key": "4bf92f3577b34da6a3ce929d0e0e4736",
					"doc_count": 5,
					"earliest_span": {"hits": {"hits": []}},
					"root_span": {"doc_count": 0, "hit": {"hits": {"hits": []}}},
					"latest_span": {"hits": {"hits": []}},
					"error_span_count": {"doc_count": 0}
				},
				{
					"key": "f47ac10b58cc4372a5670e8b8e7d97c4",
					"doc_count": 2,
					"earliest_span": {"hits": {"hits": []}},
					"root_span": {"doc_count": 0, "hit": {"hits": {"hits": []}}},
					"latest_span": {"hits": {"hits": []}},
					"error_span_count": {"doc_count": 1}
				},
				{
					"key": "c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4",
					"doc_count": 10,
					"earliest_span": {"hits": {"hits": []}},
					"root_span": {"doc_count": 0, "hit": {"hits": {"hits": []}}},
					"latest_span": {"hits": {"hits": []}},
					"error_span_count": {"doc_count": 0}
				}
			]
		}
	}`)

	result, err := parseTracesAggregation(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TraceCount.Value != 3 {
		t.Errorf("expected trace count 3, got %d", result.TraceCount.Value)
	}
	if len(result.Traces.Buckets) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(result.Traces.Buckets))
	}

	// Verify second bucket has errors
	if result.Traces.Buckets[1].ErrorSpanCount.DocCount != 1 {
		t.Errorf("expected error_span_count 1 for trace-2, got %d", result.Traces.Buckets[1].ErrorSpanCount.DocCount)
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
