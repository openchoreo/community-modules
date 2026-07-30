// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-aws-xray/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-aws-xray/internal/xray"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestHealth(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.Health200JSONResponse); !ok {
		t.Errorf("expected Health200JSONResponse, got %T", resp)
	}
}

func TestQueryTraces_NilBody(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Errorf("expected 400 response, got %T", resp)
	}
}

func TestQueryTraces_EmptyNamespace(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{
		Body: &gen.TracesQueryRequest{
			StartTime:   time.Now().Add(-1 * time.Hour),
			EndTime:     time.Now(),
			SearchScope: gen.ComponentSearchScope{Namespace: ""},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Errorf("expected 400 response, got %T", resp)
	}
}

func TestQueryTraces_EndTimeBeforeStartTime(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	now := time.Now()
	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{
		Body: &gen.TracesQueryRequest{
			StartTime:   now,
			EndTime:     now.Add(-1 * time.Hour),
			SearchScope: gen.ComponentSearchScope{Namespace: "test"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Errorf("expected 400 response, got %T", resp)
	}
}

func TestQuerySpansForTrace_NilBody(t *testing.T) {
	handler := NewTracingHandler(nil, testLogger())
	resp, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "abc",
		Body:    nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QuerySpansForTrace400JSONResponse); !ok {
		t.Errorf("expected 400 response, got %T", resp)
	}
}

func TestToTracesQueryParams(t *testing.T) {
	now := time.Now()
	limit := 50
	sortOrder := gen.Asc
	project := "proj-1"
	component := "comp-1"
	environment := "env-1"
	includeAttrs := true

	req := &gen.TracesQueryRequest{
		StartTime: now.Add(-1 * time.Hour),
		EndTime:   now,
		Limit:     &limit,
		SortOrder: &sortOrder,
		SearchScope: gen.ComponentSearchScope{
			Namespace:   "test-ns",
			Project:     &project,
			Component:   &component,
			Environment: &environment,
		},
		IncludeAttributes: &includeAttrs,
	}

	params := toTracesQueryParams(req)

	if params.Scope.Namespace != "test-ns" {
		t.Errorf("expected namespace test-ns, got %s", params.Scope.Namespace)
	}
	if params.Scope.ProjectID != "proj-1" {
		t.Errorf("expected projectID proj-1, got %s", params.Scope.ProjectID)
	}
	if params.Scope.ComponentID != "comp-1" {
		t.Errorf("expected componentID comp-1, got %s", params.Scope.ComponentID)
	}
	if params.Scope.EnvironmentID != "env-1" {
		t.Errorf("expected environmentID env-1, got %s", params.Scope.EnvironmentID)
	}
	if params.Limit != 50 {
		t.Errorf("expected limit 50, got %d", params.Limit)
	}
	if params.SortOrder != "asc" {
		t.Errorf("expected sortOrder asc, got %s", params.SortOrder)
	}
	if !params.IncludeAttributes {
		t.Error("expected includeAttributes true")
	}
}

func TestToTracesListResponse(t *testing.T) {
	result := &xray.TracesResult{
		Traces: []xray.TraceEntry{
			{
				TraceID:      "abc123",
				TraceName:    "GET /api",
				SpanCount:    5,
				RootSpanID:   "root-1",
				RootSpanName: "GET /api",
				RootSpanKind: "SERVER",
				StartTime:    time.Now().Add(-1 * time.Minute),
				EndTime:      time.Now(),
				DurationNs:   60000000000,
				HasErrors:    false,
			},
		},
		Total:  1,
		TookMs: 42,
	}

	resp := toTracesListResponse(result)

	if resp.Traces == nil || len(*resp.Traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(*resp.Traces))
	}
	if *resp.Total != 1 {
		t.Errorf("expected total 1, got %d", *resp.Total)
	}
	if *resp.TookMs != 42 {
		t.Errorf("expected tookMs 42, got %d", *resp.TookMs)
	}

	trace := (*resp.Traces)[0]
	if *trace.TraceId != "abc123" {
		t.Errorf("expected traceId abc123, got %s", *trace.TraceId)
	}
	if *trace.TraceName != "GET /api" {
		t.Errorf("expected traceName GET /api, got %s", *trace.TraceName)
	}
}

func TestToSpansListResponse_Status(t *testing.T) {
	result := &xray.SpansResult{
		Spans: []xray.SpanEntry{
			{
				SpanID:        "span-1",
				SpanName:      "example.com",
				SpanKind:      "CLIENT",
				Status:        "error",
				StatusMessage: "404 Not Found",
				Attributes:    map[string]interface{}{"http.response.status": 404},
			},
			{
				SpanID:   "span-2",
				SpanName: "frontend",
				SpanKind: "SERVER",
				Status:   "ok",
			},
		},
		Total:  2,
		TookMs: 7,
	}

	resp := toSpansListResponse(result)

	if resp.Spans == nil || len(*resp.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %v", resp.Spans)
	}

	failed := (*resp.Spans)[0]
	if failed.Status == nil || failed.Status.Code == nil || *failed.Status.Code != gen.Error {
		t.Fatalf("expected status code error, got %+v", failed.Status)
	}
	if failed.Status.Message == nil || *failed.Status.Message != "404 Not Found" {
		t.Errorf("expected status message '404 Not Found', got %+v", failed.Status.Message)
	}
	if failed.Attributes == nil {
		t.Error("expected attributes to be populated")
	}

	succeeded := (*resp.Spans)[1]
	if succeeded.Status == nil || succeeded.Status.Code == nil || *succeeded.Status.Code != gen.Ok {
		t.Errorf("expected status code ok, got %+v", succeeded.Status)
	}
	if succeeded.Status.Message != nil {
		t.Errorf("expected no status message, got %q", *succeeded.Status.Message)
	}
	if succeeded.Attributes != nil {
		t.Errorf("expected attributes to be omitted, got %v", *succeeded.Attributes)
	}
}

func TestToSpanDetailsResponse(t *testing.T) {
	startTime := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	endTime := startTime.Add(time.Second)

	span := &xray.SpanDetail{
		SpanID:        "span-1",
		SpanName:      "example.com",
		SpanKind:      "CLIENT",
		StartTime:     startTime,
		EndTime:       endTime,
		DurationNs:    int64(time.Second),
		ParentSpanID:  "seg-1",
		Status:        "error",
		StatusMessage: "connection refused",
		Attributes: map[string]interface{}{
			"http.response.status": 404,
			"xray.namespace":       "remote",
		},
		ResourceAttributes: map[string]interface{}{
			"cloud.platform": "AWS::EKS::Container",
		},
	}

	resp := toSpanDetailsResponse(span)

	if resp.Status == nil || resp.Status.Code == nil || *resp.Status.Code != gen.Error {
		t.Fatalf("expected status code error, got %+v", resp.Status)
	}
	if resp.Status.Message == nil || *resp.Status.Message != "connection refused" {
		t.Errorf("expected status message 'connection refused', got %+v", resp.Status.Message)
	}
	if resp.Attributes == nil {
		t.Fatal("expected attributes to be populated")
	}
	if v := (*resp.Attributes)["http.response.status"]; v != 404 {
		t.Errorf("expected native int attribute value 404, got %#v", v)
	}
	if resp.ResourceAttributes == nil {
		t.Fatal("expected resource attributes to be populated")
	}
	if v := (*resp.ResourceAttributes)["cloud.platform"]; v != "AWS::EKS::Container" {
		t.Errorf("expected cloud.platform resource attribute, got %#v", v)
	}
	if *resp.DurationNs != int64(time.Second) {
		t.Errorf("expected durationNs 1s, got %d", *resp.DurationNs)
	}
}
