// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal/appinsights"
)

type mockClient struct {
	queryTracesFn    func(ctx context.Context, p appinsights.TracesParams) (*appinsights.TracesResult, error)
	querySpansFn     func(ctx context.Context, p appinsights.TracesParams) (*appinsights.SpansResult, error)
	getSpanDetailsFn func(ctx context.Context, traceID, spanID string) (*appinsights.Span, error)
}

func (m *mockClient) QueryTraces(ctx context.Context, p appinsights.TracesParams) (*appinsights.TracesResult, error) {
	if m.queryTracesFn != nil {
		return m.queryTracesFn(ctx, p)
	}
	return &appinsights.TracesResult{}, nil
}

func (m *mockClient) QuerySpans(ctx context.Context, p appinsights.TracesParams) (*appinsights.SpansResult, error) {
	if m.querySpansFn != nil {
		return m.querySpansFn(ctx, p)
	}
	return &appinsights.SpansResult{}, nil
}

func (m *mockClient) GetSpanDetails(ctx context.Context, traceID, spanID string) (*appinsights.Span, error) {
	if m.getSpanDetailsFn != nil {
		return m.getSpanDetailsFn(ctx, traceID, spanID)
	}
	return nil, nil
}

func testHandler(client tracesClient) *TracingHandler {
	return NewTracingHandler(client, slog.New(slog.DiscardHandler))
}

func validBody() *gen.TracesQueryRequest {
	return &gen.TracesQueryRequest{
		StartTime: time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC),
		SearchScope: gen.ComponentSearchScope{
			Namespace: "spike-ns",
		},
	}
}

func TestQueryTraces_RequiresBody(t *testing.T) {
	h := testHandler(&mockClient{})
	resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestQueryTraces_RequiresNamespace(t *testing.T) {
	h := testHandler(&mockClient{})
	body := validBody()
	body.SearchScope.Namespace = "  "
	resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestQueryTraces_RejectsInvertedTimeRange(t *testing.T) {
	h := testHandler(&mockClient{})
	body := validBody()
	body.EndTime = body.StartTime.Add(-time.Hour)
	resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.QueryTraces400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestQueryTraces_AppliesDefaultsAndCaps(t *testing.T) {
	var captured appinsights.TracesParams
	h := testHandler(&mockClient{
		queryTracesFn: func(_ context.Context, p appinsights.TracesParams) (*appinsights.TracesResult, error) {
			captured = p
			return &appinsights.TracesResult{}, nil
		},
	})

	body := validBody()
	if _, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body}); err != nil {
		t.Fatal(err)
	}
	if captured.Limit != 20 || captured.SortOrder != "desc" {
		t.Errorf("defaults not applied: limit=%d sort=%q", captured.Limit, captured.SortOrder)
	}

	over := 50000
	body.Limit = &over
	if _, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body}); err != nil {
		t.Fatal(err)
	}
	if captured.Limit != 10000 {
		t.Errorf("limit not capped: %d", captured.Limit)
	}
}

func TestQueryTraces_Success(t *testing.T) {
	h := testHandler(&mockClient{
		queryTracesFn: func(_ context.Context, p appinsights.TracesParams) (*appinsights.TracesResult, error) {
			return &appinsights.TracesResult{
				Traces: []appinsights.TraceEntry{{
					TraceID:      "4372fc01295900a9",
					TraceName:    "lets-go",
					SpanCount:    4,
					RootSpanID:   "2419e7552dfbe055",
					RootSpanName: "lets-go",
					RootSpanKind: "CLIENT",
					DurationNs:   123_000_000,
				}},
				Total:  1,
				TookMs: 42,
			}, nil
		},
	})

	resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: validBody()})
	if err != nil {
		t.Fatal(err)
	}
	ok, isOK := resp.(gen.QueryTraces200JSONResponse)
	if !isOK {
		t.Fatalf("got %T, want 200", resp)
	}
	if ok.Total == nil || *ok.Total != 1 {
		t.Errorf("Total = %v", ok.Total)
	}
	traces := *ok.Traces
	if len(traces) != 1 || *traces[0].TraceId != "4372fc01295900a9" {
		t.Errorf("unexpected traces payload: %+v", traces)
	}
}

func TestQueryTraces_ClientError(t *testing.T) {
	h := testHandler(&mockClient{
		queryTracesFn: func(_ context.Context, _ appinsights.TracesParams) (*appinsights.TracesResult, error) {
			return nil, errors.New("boom")
		},
	})
	resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: validBody()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.QueryTraces500JSONResponse); !ok {
		t.Errorf("got %T, want 500", resp)
	}
}

func TestQuerySpansForTrace_RejectsBadTraceID(t *testing.T) {
	h := testHandler(&mockClient{})
	resp, err := h.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: `" | take 1`,
		Body:    validBody(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.QuerySpansForTrace400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestQuerySpansForTrace_Success(t *testing.T) {
	h := testHandler(&mockClient{
		querySpansFn: func(_ context.Context, p appinsights.TracesParams) (*appinsights.SpansResult, error) {
			if p.TraceID != "4372fc01295900a9" {
				t.Errorf("TraceID = %q", p.TraceID)
			}
			return &appinsights.SpansResult{
				Spans: []appinsights.Span{{
					SpanID:   "2419e7552dfbe055",
					Name:     "lets-go",
					SpanKind: "CLIENT",
					Status:   "ok",
					Attributes: map[string]interface{}{
						"peer.service": "telemetrygen-server",
					},
				}},
				Total:  1,
				TookMs: 7,
			}, nil
		},
	})

	resp, err := h.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "4372fc01295900a9",
		Body:    validBody(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, isOK := resp.(gen.QuerySpansForTrace200JSONResponse)
	if !isOK {
		t.Fatalf("got %T, want 200", resp)
	}
	spans := *ok.Spans
	if len(spans) != 1 || *spans[0].SpanId != "2419e7552dfbe055" {
		t.Errorf("unexpected spans payload: %+v", spans)
	}
	// includeAttributes defaults to false: attributes must be omitted.
	if spans[0].Attributes != nil {
		t.Error("attributes included without includeAttributes")
	}
}

func TestGetSpanDetailsForTrace_RejectsBadIDs(t *testing.T) {
	h := testHandler(&mockClient{})
	resp, err := h.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
		TraceId: `" | take 1`,
		SpanId:  "2419e7552dfbe055",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.GetSpanDetailsForTrace400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestGetSpanDetailsForTrace_NotFound(t *testing.T) {
	h := testHandler(&mockClient{})
	resp, err := h.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
		TraceId: "4372fc01295900a9",
		SpanId:  "2419e7552dfbe055",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(gen.GetSpanDetailsForTrace500JSONResponse); !ok {
		t.Errorf("got %T, want 500 (not found)", resp)
	}
}

func TestGetSpanDetailsForTrace_Success(t *testing.T) {
	h := testHandler(&mockClient{
		getSpanDetailsFn: func(_ context.Context, traceID, spanID string) (*appinsights.Span, error) {
			return &appinsights.Span{
				SpanID:   spanID,
				Name:     "lets-go",
				SpanKind: "CLIENT",
				Status:   "ok",
				ResourceAttributes: map[string]interface{}{
					"openchoreo.dev/namespace": "spike-ns",
				},
			}, nil
		},
	})
	resp, err := h.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
		TraceId: "4372fc01295900a9",
		SpanId:  "2419e7552dfbe055",
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, isOK := resp.(gen.GetSpanDetailsForTrace200JSONResponse)
	if !isOK {
		t.Fatalf("got %T, want 200", resp)
	}
	if *ok.SpanId != "2419e7552dfbe055" {
		t.Errorf("SpanId = %q", *ok.SpanId)
	}
	if len(*ok.ResourceAttributes) != 1 {
		t.Errorf("ResourceAttributes = %+v", *ok.ResourceAttributes)
	}
}
