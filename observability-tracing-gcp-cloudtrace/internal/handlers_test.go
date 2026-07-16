// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-gcp-cloudtrace/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-gcp-cloudtrace/internal/cloudtrace"
)

type fakeClient struct {
	tracesParams cloudtrace.TracesParams
	tracesResult *cloudtrace.TracesResult
	spansParams  cloudtrace.TracesParams
	spansResult  *cloudtrace.SpansResult
	span         *cloudtrace.Span
	err          error
}

func (f *fakeClient) QueryTraces(_ context.Context, p cloudtrace.TracesParams) (*cloudtrace.TracesResult, error) {
	f.tracesParams = p
	return f.tracesResult, f.err
}

func (f *fakeClient) QuerySpans(_ context.Context, p cloudtrace.TracesParams) (*cloudtrace.SpansResult, error) {
	f.spansParams = p
	return f.spansResult, f.err
}

func (f *fakeClient) GetSpanDetails(_ context.Context, traceID, spanID string) (*cloudtrace.Span, error) {
	return f.span, f.err
}

func newHandler(client tracesClient) *TracingHandler {
	return NewTracingHandler(client, slog.New(slog.DiscardHandler))
}

func validBody() *gen.TracesQueryRequest {
	start := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	return &gen.TracesQueryRequest{
		StartTime: start,
		EndTime:   start.Add(time.Hour),
		SearchScope: gen.ComponentSearchScope{
			Namespace: "default",
		},
	}
}

func TestHealth(t *testing.T) {
	resp, err := newHandler(&fakeClient{}).Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if _, ok := resp.(gen.Health200JSONResponse); !ok {
		t.Errorf("resp = %T, want Health200JSONResponse", resp)
	}
}

func TestQueryTracesValidation(t *testing.T) {
	h := newHandler(&fakeClient{})

	tests := []struct {
		name string
		body *gen.TracesQueryRequest
	}{
		{"nil body", nil},
		{"missing namespace", func() *gen.TracesQueryRequest {
			b := validBody()
			b.SearchScope.Namespace = "  "
			return b
		}()},
		{"end before start", func() *gen.TracesQueryRequest {
			b := validBody()
			b.EndTime = b.StartTime.Add(-time.Minute)
			return b
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: tt.body})
			if err != nil {
				t.Fatalf("QueryTraces: %v", err)
			}
			if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
				t.Errorf("resp = %T, want QueryTraces400JSONResponse", resp)
			}
		})
	}
}

func TestQueryTracesSuccess(t *testing.T) {
	start := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	client := &fakeClient{
		tracesResult: &cloudtrace.TracesResult{
			Traces: []cloudtrace.TraceEntry{{
				TraceID:      "t1",
				TraceName:    "GET /orders",
				SpanCount:    3,
				RootSpanID:   "0000000000000001",
				RootSpanName: "GET /orders",
				RootSpanKind: "SERVER",
				StartTime:    start,
				EndTime:      start.Add(time.Second),
				DurationNs:   time.Second.Nanoseconds(),
				HasErrors:    true,
			}},
			Total:  1,
			TookMs: 5,
		},
	}
	h := newHandler(client)

	body := validBody()
	body.SearchScope.Component = ptr("comp-uid")
	limit := 7
	body.Limit = &limit

	resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	ok, isOK := resp.(gen.QueryTraces200JSONResponse)
	if !isOK {
		t.Fatalf("resp = %T", resp)
	}
	if client.tracesParams.ComponentUID != "comp-uid" || client.tracesParams.Limit != 7 {
		t.Errorf("params = %+v", client.tracesParams)
	}
	if *ok.Total != 1 || len(*ok.Traces) != 1 {
		t.Fatalf("response = %+v", ok)
	}
	got := (*ok.Traces)[0]
	if *got.TraceId != "t1" || *got.SpanCount != 3 || !*got.HasErrors {
		t.Errorf("trace = %+v", got)
	}
}

func TestQueryTracesLimitDefaultsAndClamp(t *testing.T) {
	client := &fakeClient{tracesResult: &cloudtrace.TracesResult{}}
	h := newHandler(client)

	if _, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: validBody()}); err != nil {
		t.Fatal(err)
	}
	if client.tracesParams.Limit != defaultLimit {
		t.Errorf("default Limit = %d, want %d", client.tracesParams.Limit, defaultLimit)
	}
	if client.tracesParams.SortOrder != "desc" {
		t.Errorf("default SortOrder = %q, want desc", client.tracesParams.SortOrder)
	}

	body := validBody()
	huge := 999999
	body.Limit = &huge
	if _, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body}); err != nil {
		t.Fatal(err)
	}
	if client.tracesParams.Limit != maxLimit {
		t.Errorf("clamped Limit = %d, want %d", client.tracesParams.Limit, maxLimit)
	}
}

func TestQueryTracesBackendError(t *testing.T) {
	h := newHandler(&fakeClient{err: errors.New("boom")})
	resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: validBody()})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if _, ok := resp.(gen.QueryTraces500JSONResponse); !ok {
		t.Errorf("resp = %T, want QueryTraces500JSONResponse", resp)
	}
}

func TestQuerySpansForTrace(t *testing.T) {
	client := &fakeClient{
		spansResult: &cloudtrace.SpansResult{
			Spans: []cloudtrace.Span{{
				SpanID:     "0000000000000001",
				Name:       "GET /orders",
				SpanKind:   "SERVER",
				Status:     "ok",
				Attributes: map[string]interface{}{"http.method": "GET"},
			}},
			Total: 1,
		},
	}
	h := newHandler(client)

	body := validBody()
	include := true
	body.IncludeAttributes = &include

	resp, err := h.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "0123456789abcdef0123456789abcdef",
		Body:    body,
	})
	if err != nil {
		t.Fatalf("QuerySpansForTrace: %v", err)
	}
	ok, isOK := resp.(gen.QuerySpansForTrace200JSONResponse)
	if !isOK {
		t.Fatalf("resp = %T", resp)
	}
	if client.spansParams.TraceID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("TraceID = %q", client.spansParams.TraceID)
	}
	span := (*ok.Spans)[0]
	if span.Attributes == nil {
		t.Error("attributes missing despite includeAttributes")
	}
}

func TestQuerySpansForTraceRejectsBadTraceID(t *testing.T) {
	h := newHandler(&fakeClient{})
	resp, err := h.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "not-hex!",
		Body:    validBody(),
	})
	if err != nil {
		t.Fatalf("QuerySpansForTrace: %v", err)
	}
	if _, ok := resp.(gen.QuerySpansForTrace400JSONResponse); !ok {
		t.Errorf("resp = %T, want 400", resp)
	}
}

func TestGetSpanDetailsForTrace(t *testing.T) {
	client := &fakeClient{
		span: &cloudtrace.Span{
			SpanID:   "0000000000000abc",
			Name:     "target",
			SpanKind: "CLIENT",
			Status:   "error",
		},
	}
	h := newHandler(client)

	resp, err := h.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
		TraceId: "0123456789abcdef0123456789abcdef",
		SpanId:  "0000000000000abc",
	})
	if err != nil {
		t.Fatalf("GetSpanDetailsForTrace: %v", err)
	}
	ok, isOK := resp.(gen.GetSpanDetailsForTrace200JSONResponse)
	if !isOK {
		t.Fatalf("resp = %T", resp)
	}
	if *ok.SpanId != "0000000000000abc" || *ok.Status != "error" {
		t.Errorf("span = %+v", ok)
	}
}

func TestGetSpanDetailsForTraceValidation(t *testing.T) {
	h := newHandler(&fakeClient{})

	tests := []struct {
		name    string
		traceID string
		spanID  string
	}{
		{"bad trace id", "zz", "0000000000000abc"},
		{"bad span id", "0123456789abcdef", "not-hex"},
		{"overlong span id", "0123456789abcdef", "0123456789abcdef0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := h.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
				TraceId: tt.traceID,
				SpanId:  tt.spanID,
			})
			if err != nil {
				t.Fatalf("GetSpanDetailsForTrace: %v", err)
			}
			if _, ok := resp.(gen.GetSpanDetailsForTrace400JSONResponse); !ok {
				t.Errorf("resp = %T, want 400", resp)
			}
		})
	}
}
