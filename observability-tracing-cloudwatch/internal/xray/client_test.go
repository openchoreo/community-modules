// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/xray"
	xraytypes "github.com/aws/aws-sdk-go-v2/service/xray/types"
)

type mockXRayClient struct {
	getSummariesFn   func(context.Context, *xray.GetTraceSummariesInput, ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error)
	batchGetTracesFn func(context.Context, *xray.BatchGetTracesInput, ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error)
}

func (m *mockXRayClient) GetTraceSummaries(ctx context.Context, input *xray.GetTraceSummariesInput, opts ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
	if m.getSummariesFn != nil {
		return m.getSummariesFn(ctx, input, opts...)
	}
	return &xray.GetTraceSummariesOutput{}, nil
}

func (m *mockXRayClient) BatchGetTraces(ctx context.Context, input *xray.BatchGetTracesInput, opts ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error) {
	if m.batchGetTracesFn != nil {
		return m.batchGetTracesFn(ctx, input, opts...)
	}
	return &xray.BatchGetTracesOutput{}, nil
}

type mockSTSClient struct{}

func (m *mockSTSClient) GetCallerIdentity(ctx context.Context, input *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
		Arn:     aws.String("arn:aws:iam::123456789012:user/test"),
		UserId:  aws.String("AIDEXAMPLE"),
	}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestPing(t *testing.T) {
	client := NewClientWithAWS(&mockXRayClient{}, &mockSTSClient{}, testLogger())
	err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("unexpected ping error: %v", err)
	}
}

func TestGetTraces_Empty(t *testing.T) {
	mock := &mockXRayClient{
		getSummariesFn: func(ctx context.Context, input *xray.GetTraceSummariesInput, opts ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
			return &xray.GetTraceSummariesOutput{
				TraceSummaries: []xraytypes.TraceSummary{},
			}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	result, err := client.GetTraces(context.Background(), TracesQueryParams{
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		Scope:     Scope{Namespace: "test"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Traces) != 0 {
		t.Errorf("expected 0 traces, got %d", len(result.Traces))
	}
}

func TestGetTraces_WithSummaries(t *testing.T) {
	dur := float64(0.5)
	startTime := time.Date(2026, 5, 8, 6, 18, 0, 0, time.UTC)
	mock := &mockXRayClient{
		getSummariesFn: func(ctx context.Context, input *xray.GetTraceSummariesInput, opts ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
			return &xray.GetTraceSummariesOutput{
				TraceSummaries: []xraytypes.TraceSummary{
					{
						Id:          aws.String("1-5759e988-bd862e3fe1be46a994272793"),
						Duration:    &dur,
						StartTime:   &startTime,
						HasFault:    aws.Bool(false),
						HasError:    aws.Bool(false),
						HasThrottle: aws.Bool(false),
					},
				},
			}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	result, err := client.GetTraces(context.Background(), TracesQueryParams{
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		Limit:     10,
		Scope:     Scope{Namespace: "test"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(result.Traces))
	}
	if result.Traces[0].TraceID != "5759e988bd862e3fe1be46a994272793" {
		t.Errorf("expected converted trace ID, got %s", result.Traces[0].TraceID)
	}
	if result.Traces[0].HasErrors {
		t.Error("expected no errors")
	}
	if !result.Traces[0].StartTime.Equal(startTime) {
		t.Errorf("expected start time %s, got %s", startTime, result.Traces[0].StartTime)
	}
	if !result.Traces[0].EndTime.Equal(startTime.Add(500 * time.Millisecond)) {
		t.Errorf("expected end time %s, got %s", startTime.Add(500*time.Millisecond), result.Traces[0].EndTime)
	}
}

func TestGetTraces_EnrichesPreciseTimeRange(t *testing.T) {
	summaryDuration := float64(0.001)
	summaryStart := time.Date(2026, 5, 11, 5, 41, 16, 0, time.UTC)
	segDoc := `{
		"id": "seg1",
		"name": "frontend",
		"trace_id": "1-6a1900fc-6d76f4e75b8a88c881035831",
		"start_time": 1.7784780768055322e+09,
		"end_time": 1.7784780784616914e+09,
		"subsegments": [
			{
				"id": "sub1",
				"name": "fetchMetadata",
				"start_time": 1.7784780768077307e+09,
				"end_time": 1.778478079549484e+09,
				"subsegments": [
					{
						"id": "sub2",
						"name": "example.com",
						"start_time": 1.7784780768077502e+09,
						"end_time": 1.7784780784616914e+09,
						"error": true
					}
				]
			}
		]
	}`

	mock := &mockXRayClient{
		getSummariesFn: func(ctx context.Context, input *xray.GetTraceSummariesInput, opts ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
			return &xray.GetTraceSummariesOutput{
				TraceSummaries: []xraytypes.TraceSummary{
					{
						Id:        aws.String("1-6a1900fc-6d76f4e75b8a88c881035831"),
						Duration:  &summaryDuration,
						StartTime: &summaryStart,
					},
				},
			}, nil
		},
		batchGetTracesFn: func(ctx context.Context, input *xray.BatchGetTracesInput, opts ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error) {
			return &xray.BatchGetTracesOutput{
				Traces: []xraytypes.Trace{
					{
						Id: aws.String("1-6a1900fc-6d76f4e75b8a88c881035831"),
						Segments: []xraytypes.Segment{
							{Document: aws.String(segDoc)},
						},
					},
				},
			}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	result, err := client.GetTraces(context.Background(), TracesQueryParams{
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		Limit:     10,
		Scope:     Scope{Namespace: "test"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(result.Traces))
	}
	wantStart := time.Unix(0, jsonNumberToNanos(json.Number("1.7784780768055322e+09")))
	wantEnd := time.Unix(0, jsonNumberToNanos(json.Number("1.778478079549484e+09")))
	if !result.Traces[0].StartTime.Equal(wantStart) {
		t.Errorf("expected precise start time %s, got %s", wantStart, result.Traces[0].StartTime)
	}
	if !result.Traces[0].EndTime.Equal(wantEnd) {
		t.Errorf("expected precise end time %s, got %s", wantEnd, result.Traces[0].EndTime)
	}
	if result.Traces[0].DurationNs != 2743951800 {
		t.Errorf("expected duration 2743951800 ns, got %d", result.Traces[0].DurationNs)
	}
	if result.Traces[0].SpanCount != 3 {
		t.Errorf("expected span count 3, got %d", result.Traces[0].SpanCount)
	}
	if !result.Traces[0].HasErrors {
		t.Error("expected trace to be marked as having errors")
	}
}

func TestGetTraces_WithFilterExpression(t *testing.T) {
	var capturedInput *xray.GetTraceSummariesInput

	mock := &mockXRayClient{
		getSummariesFn: func(ctx context.Context, input *xray.GetTraceSummariesInput, opts ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
			capturedInput = input
			return &xray.GetTraceSummariesOutput{}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	_, _ = client.GetTraces(context.Background(), TracesQueryParams{
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		Scope: Scope{
			Namespace:   "my-ns",
			ComponentID: "comp-123",
		},
	})

	if capturedInput == nil || capturedInput.FilterExpression == nil {
		t.Fatal("expected filter expression to be set")
	}

	expr := *capturedInput.FilterExpression
	if !searchSubstring(expr, "annotation[openchoreo.dev_namespace]") {
		t.Errorf("expected namespace in filter, got %q", expr)
	}
	if !searchSubstring(expr, "annotation[openchoreo.dev_component_uid]") {
		t.Errorf("expected component in filter, got %q", expr)
	}
}

func TestGetSpans_Empty(t *testing.T) {
	mock := &mockXRayClient{
		batchGetTracesFn: func(ctx context.Context, input *xray.BatchGetTracesInput, opts ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error) {
			return &xray.BatchGetTracesOutput{
				Traces: []xraytypes.Trace{},
			}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	result, err := client.GetSpans(context.Background(), TracesQueryParams{
		TraceID: "5759e988bd862e3fe1be46a994272793",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Spans) != 0 {
		t.Errorf("expected 0 spans, got %d", len(result.Spans))
	}
}

func TestGetSpans_WithSegments(t *testing.T) {
	segDoc := `{
		"id": "seg1",
		"name": "GET /api",
		"trace_id": "1-5759e988-bd862e3fe1be46a994272793",
		"start_time": 1465510988.0,
		"end_time": 1465510988.5,
		"subsegments": [
			{
				"id": "sub1",
				"name": "DynamoDB",
				"start_time": 1465510988.1,
				"end_time": 1465510988.3,
				"namespace": "aws"
			}
		]
	}`

	mock := &mockXRayClient{
		batchGetTracesFn: func(ctx context.Context, input *xray.BatchGetTracesInput, opts ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error) {
			return &xray.BatchGetTracesOutput{
				Traces: []xraytypes.Trace{
					{
						Id: aws.String("1-5759e988-bd862e3fe1be46a994272793"),
						Segments: []xraytypes.Segment{
							{Document: aws.String(segDoc)},
						},
					},
				},
			}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	result, err := client.GetSpans(context.Background(), TracesQueryParams{
		TraceID: "5759e988bd862e3fe1be46a994272793",
		Limit:   100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Spans) != 2 {
		t.Fatalf("expected 2 spans (segment + subsegment), got %d", len(result.Spans))
	}
	if result.Spans[0].SpanID != "seg1" {
		t.Errorf("expected first span ID seg1, got %s", result.Spans[0].SpanID)
	}
	if result.Spans[1].SpanID != "sub1" {
		t.Errorf("expected second span ID sub1, got %s", result.Spans[1].SpanID)
	}
	if result.Spans[1].ParentSpanID != "seg1" {
		t.Errorf("expected subsegment parent to be seg1, got %s", result.Spans[1].ParentSpanID)
	}
}

func TestGetSpans_WithScientificNotationTimestamps(t *testing.T) {
	segDoc := `{
		"id": "seg1",
		"name": "frontend",
		"trace_id": "1-6a19017d-ca1791cb19542f1806dc624c",
		"start_time": 1.778478205e+09,
		"end_time": 1.778478205001e+09
	}`

	mock := &mockXRayClient{
		batchGetTracesFn: func(ctx context.Context, input *xray.BatchGetTracesInput, opts ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error) {
			return &xray.BatchGetTracesOutput{
				Traces: []xraytypes.Trace{
					{
						Id: aws.String("1-6a19017d-ca1791cb19542f1806dc624c"),
						Segments: []xraytypes.Segment{
							{Document: aws.String(segDoc)},
						},
					},
				},
			}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	result, err := client.GetSpans(context.Background(), TracesQueryParams{
		TraceID: "6a19017dca1791cb19542f1806dc624c",
		Limit:   100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(result.Spans))
	}
	wantStart := time.Date(2026, 5, 11, 5, 43, 25, 0, time.UTC)
	if !result.Spans[0].StartTime.Equal(wantStart) {
		t.Errorf("expected start time %s, got %s", wantStart, result.Spans[0].StartTime)
	}
	if result.Spans[0].DurationNs != int64(time.Millisecond) {
		t.Errorf("expected duration 1ms, got %d ns", result.Spans[0].DurationNs)
	}
}

func TestGetSpanDetail_Found(t *testing.T) {
	segDoc := `{
		"id": "seg1",
		"name": "GET /api",
		"trace_id": "1-5759e988-bd862e3fe1be46a994272793",
		"start_time": 1465510988.0,
		"end_time": 1465510988.5,
		"annotations": {"key1": "value1"},
		"http": {"request": {"method": "GET"}},
		"subsegments": [
			{
				"id": "sub1",
				"name": "DynamoDB",
				"start_time": 1465510988.1,
				"end_time": 1465510988.3,
				"namespace": "aws"
			}
		]
	}`

	mock := &mockXRayClient{
		batchGetTracesFn: func(ctx context.Context, input *xray.BatchGetTracesInput, opts ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error) {
			return &xray.BatchGetTracesOutput{
				Traces: []xraytypes.Trace{
					{
						Id: aws.String("1-5759e988-bd862e3fe1be46a994272793"),
						Segments: []xraytypes.Segment{
							{Document: aws.String(segDoc)},
						},
					},
				},
			}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	result, err := client.GetSpanDetail(context.Background(), TracesQueryParams{
		TraceID: "5759e988bd862e3fe1be46a994272793",
		SpanID:  "sub1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Span.SpanID != "sub1" {
		t.Errorf("expected spanID sub1, got %s", result.Span.SpanID)
	}
	if result.Span.SpanName != "DynamoDB" {
		t.Errorf("expected spanName DynamoDB, got %s", result.Span.SpanName)
	}
	if result.Span.ParentSpanID != "seg1" {
		t.Errorf("expected parentSpanID seg1, got %s", result.Span.ParentSpanID)
	}
}

func TestGetSpanDetail_NotFound(t *testing.T) {
	segDoc := `{"id": "seg1", "name": "test", "start_time": 1.0, "end_time": 2.0}`

	mock := &mockXRayClient{
		batchGetTracesFn: func(ctx context.Context, input *xray.BatchGetTracesInput, opts ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error) {
			return &xray.BatchGetTracesOutput{
				Traces: []xraytypes.Trace{
					{
						Id: aws.String("1-5759e988-bd862e3fe1be46a994272793"),
						Segments: []xraytypes.Segment{
							{Document: aws.String(segDoc)},
						},
					},
				},
			}, nil
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	_, err := client.GetSpanDetail(context.Background(), TracesQueryParams{
		TraceID: "5759e988bd862e3fe1be46a994272793",
		SpanID:  "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for non-existent span")
	}
}

func TestGetTraces_Error(t *testing.T) {
	mock := &mockXRayClient{
		getSummariesFn: func(ctx context.Context, input *xray.GetTraceSummariesInput, opts ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
			return nil, fmt.Errorf("X-Ray API error")
		},
	}

	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())
	_, err := client.GetTraces(context.Background(), TracesQueryParams{
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		Scope:     Scope{Namespace: "test"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
