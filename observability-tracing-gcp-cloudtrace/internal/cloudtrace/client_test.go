// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"cloud.google.com/go/trace/apiv1/tracepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func scopedLabels() map[string]string {
	return map[string]string{LabelNamespace: "default"}
}

func TestQueryTracesBuildsRequest(t *testing.T) {
	var gotReq *tracepb.ListTracesRequest
	var gotMax int

	c := newClientWithAPI(Config{ProjectID: "proj-1"}, testLogger(),
		func(ctx context.Context, req *tracepb.ListTracesRequest, max int) ([]*tracepb.Trace, error) {
			gotReq, gotMax = req, max
			return []*tracepb.Trace{{TraceId: "t1", Spans: []*tracepb.TraceSpan{{SpanId: 1, Name: "root"}}}}, nil
		},
		nil,
	)

	start := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	result, err := c.QueryTraces(context.Background(), TracesParams{
		StartTime: start,
		EndTime:   end,
		Namespace: "default",
		Limit:     50,
		SortOrder: "asc",
	})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}

	if gotReq.GetProjectId() != "proj-1" {
		t.Errorf("ProjectId = %q", gotReq.GetProjectId())
	}
	if gotReq.GetView() != tracepb.ListTracesRequest_COMPLETE {
		t.Errorf("View = %v, want COMPLETE", gotReq.GetView())
	}
	if gotReq.GetFilter() != "+openchoreo.dev/namespace:default" {
		t.Errorf("Filter = %q", gotReq.GetFilter())
	}
	if gotReq.GetOrderBy() != "start" {
		t.Errorf("OrderBy = %q, want start (asc)", gotReq.GetOrderBy())
	}
	if gotReq.GetPageSize() != 50 {
		t.Errorf("PageSize = %d, want 50", gotReq.GetPageSize())
	}
	if !gotReq.GetStartTime().AsTime().Equal(start) || !gotReq.GetEndTime().AsTime().Equal(end) {
		t.Error("time range not passed through")
	}
	if gotMax != 50 {
		t.Errorf("max = %d, want 50", gotMax)
	}
	if len(result.Traces) != 1 || result.Total != 1 {
		t.Errorf("result = %+v", result)
	}
	if result.Traces[0].TraceID != "t1" {
		t.Errorf("TraceID = %q", result.Traces[0].TraceID)
	}
}

func TestQueryTracesDescendingIsDefault(t *testing.T) {
	var gotOrderBy string
	c := newClientWithAPI(Config{ProjectID: "p"}, testLogger(),
		func(ctx context.Context, req *tracepb.ListTracesRequest, max int) ([]*tracepb.Trace, error) {
			gotOrderBy = req.GetOrderBy()
			return nil, nil
		},
		nil,
	)
	if _, err := c.QueryTraces(context.Background(), TracesParams{Namespace: "default", Limit: 10}); err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if gotOrderBy != "start desc" {
		t.Errorf("OrderBy = %q, want %q", gotOrderBy, "start desc")
	}
}

func TestQuerySpansSortsAndLimits(t *testing.T) {
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	c := newClientWithAPI(Config{ProjectID: "p"}, testLogger(), nil,
		func(ctx context.Context, req *tracepb.GetTraceRequest) (*tracepb.Trace, error) {
			return &tracepb.Trace{TraceId: req.GetTraceId(), Spans: []*tracepb.TraceSpan{
				{SpanId: 2, ParentSpanId: 1, StartTime: timestamppb.New(base.Add(2 * time.Second)), Labels: scopedLabels()},
				{SpanId: 1, StartTime: timestamppb.New(base), Labels: scopedLabels()},
				{SpanId: 3, ParentSpanId: 1, StartTime: timestamppb.New(base.Add(time.Second)), Labels: scopedLabels()},
			}}, nil
		},
	)

	result, err := c.QuerySpans(context.Background(), TracesParams{
		Namespace: "default",
		TraceID:   "ABCDEF",
		Limit:     2,
	})
	if err != nil {
		t.Fatalf("QuerySpans: %v", err)
	}
	if result.Total != 2 || len(result.Spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(result.Spans))
	}
	if result.Spans[0].SpanID != "0000000000000001" || result.Spans[1].SpanID != "0000000000000003" {
		t.Errorf("spans not sorted by start time: %q, %q", result.Spans[0].SpanID, result.Spans[1].SpanID)
	}
}

func TestQuerySpansEnforcesScope(t *testing.T) {
	c := newClientWithAPI(Config{ProjectID: "p"}, testLogger(), nil,
		func(ctx context.Context, req *tracepb.GetTraceRequest) (*tracepb.Trace, error) {
			return &tracepb.Trace{TraceId: req.GetTraceId(), Spans: []*tracepb.TraceSpan{
				{SpanId: 1, Labels: map[string]string{LabelNamespace: "other-tenant"}},
			}}, nil
		},
	)

	result, err := c.QuerySpans(context.Background(), TracesParams{Namespace: "default", TraceID: "ff", Limit: 10})
	if err != nil {
		t.Fatalf("QuerySpans: %v", err)
	}
	if len(result.Spans) != 0 || result.Total != 0 {
		t.Errorf("out-of-scope trace leaked %d spans", len(result.Spans))
	}
}

func TestQuerySpansNotFound(t *testing.T) {
	c := newClientWithAPI(Config{ProjectID: "p"}, testLogger(), nil,
		func(ctx context.Context, req *tracepb.GetTraceRequest) (*tracepb.Trace, error) {
			return nil, status.Error(codes.NotFound, "trace not found")
		},
	)
	result, err := c.QuerySpans(context.Background(), TracesParams{Namespace: "default", TraceID: "ff", Limit: 10})
	if err != nil {
		t.Fatalf("QuerySpans should map NotFound to empty, got %v", err)
	}
	if len(result.Spans) != 0 {
		t.Errorf("got %d spans, want 0", len(result.Spans))
	}
}

func TestGetTraceLowercasesTraceID(t *testing.T) {
	var gotTraceID string
	c := newClientWithAPI(Config{ProjectID: "p"}, testLogger(), nil,
		func(ctx context.Context, req *tracepb.GetTraceRequest) (*tracepb.Trace, error) {
			gotTraceID = req.GetTraceId()
			return &tracepb.Trace{}, nil
		},
	)
	if _, err := c.getTrace(context.Background(), "ABCDEF012345"); err != nil {
		t.Fatalf("getTrace: %v", err)
	}
	if gotTraceID != "abcdef012345" {
		t.Errorf("TraceId = %q, want lowercase", gotTraceID)
	}
}

func TestGetSpanDetails(t *testing.T) {
	c := newClientWithAPI(Config{ProjectID: "p"}, testLogger(), nil,
		func(ctx context.Context, req *tracepb.GetTraceRequest) (*tracepb.Trace, error) {
			return &tracepb.Trace{Spans: []*tracepb.TraceSpan{
				{SpanId: 0xabc, Name: "target", Labels: map[string]string{"http.method": "GET"}},
				{SpanId: 0xdef, Name: "other"},
			}}, nil
		},
	)

	span, err := c.GetSpanDetails(context.Background(), "ff", "0000000000000abc")
	if err != nil {
		t.Fatalf("GetSpanDetails: %v", err)
	}
	if span == nil || span.Name != "target" {
		t.Fatalf("span = %+v, want target", span)
	}
	if span.Attributes == nil {
		t.Error("details lookup should always include attributes")
	}

	missing, err := c.GetSpanDetails(context.Background(), "ff", "0000000000000aaa")
	if err != nil {
		t.Fatalf("GetSpanDetails(missing): %v", err)
	}
	if missing != nil {
		t.Errorf("missing span = %+v, want nil", missing)
	}
}
