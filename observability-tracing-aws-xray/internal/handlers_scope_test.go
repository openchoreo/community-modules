// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/xray"
	xraytypes "github.com/aws/aws-sdk-go-v2/service/xray/types"

	"github.com/openchoreo/community-modules/observability-tracing-aws-xray/internal/api/gen"
	xrayclient "github.com/openchoreo/community-modules/observability-tracing-aws-xray/internal/xray"
)

const backendErrorText = "AccessDeniedException: arn:aws:iam::123456789012:role/secret-role"

type fakeXRay struct {
	traces    []xraytypes.Trace
	err       error
	requested [][]string
}

func (f *fakeXRay) GetTraceSummaries(context.Context, *xray.GetTraceSummariesInput, ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &xray.GetTraceSummariesOutput{}, nil
}

func (f *fakeXRay) BatchGetTraces(_ context.Context, input *xray.BatchGetTracesInput, _ ...func(*xray.Options)) (*xray.BatchGetTracesOutput, error) {
	f.requested = append(f.requested, input.TraceIds)
	if f.err != nil {
		return nil, f.err
	}
	return &xray.BatchGetTracesOutput{Traces: f.traces}, nil
}

func newHandlerWithXRay(f *fakeXRay) *TracingHandler {
	return NewTracingHandler(xrayclient.NewClientWithAWS(f, nil, testLogger()), testLogger())
}

func scopedSegment(id, namespace, projectUID, componentUID, environmentUID string) string {
	return `{
		"id": "` + id + `",
		"name": "` + id + `",
		"start_time": 1465510988.0,
		"end_time": 1465510988.5,
		"annotations": {
			"openchoreo.dev_namespace": "` + namespace + `",
			"openchoreo.dev_project_uid": "` + projectUID + `",
			"openchoreo.dev_component_uid": "` + componentUID + `",
			"openchoreo.dev_environment_uid": "` + environmentUID + `"
		}
	}`
}

func traceOf(docs ...string) []xraytypes.Trace {
	trace := xraytypes.Trace{Id: aws.String("1-5759e988-bd862e3fe1be46a994272793")}
	for _, d := range docs {
		trace.Segments = append(trace.Segments, xraytypes.Segment{Document: aws.String(d)})
	}
	return []xraytypes.Trace{trace}
}

func TestQuerySpansForTrace_AppliesEveryScopeField(t *testing.T) {
	fake := &fakeXRay{traces: traceOf(
		scopedSegment("match", "ns-a", "p1", "c1", "e1"),
		strings.Replace(scopedSegment("matchOutsideWindow", "ns-a", "p1", "c1", "e1"),
			`"start_time": 1465510988.0`, `"start_time": 1465510987.0`, 1),
		scopedSegment("otherNamespace", "ns-b", "p1", "c1", "e1"),
		scopedSegment("otherProject", "ns-a", "p2", "c1", "e1"),
		scopedSegment("otherComponent", "ns-a", "p1", "c2", "e1"),
		scopedSegment("otherEnvironment", "ns-a", "p1", "c1", "e2"),
	)}
	handler := newHandlerWithXRay(fake)

	project, component, environment := "p1", "c1", "e1"
	resp, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "5759e988bd862e3fe1be46a994272793",
		Body: &gen.TracesQueryRequest{
			StartTime: time.Unix(1465510988, 0),
			EndTime:   time.Unix(1465510989, 0),
			SearchScope: gen.ComponentSearchScope{
				Namespace:   "ns-a",
				Project:     &project,
				Component:   &component,
				Environment: &environment,
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, isOK := resp.(gen.QuerySpansForTrace200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	var got []string
	for _, s := range *ok.Body.Spans {
		got = append(got, *s.SpanId)
	}
	sort.Strings(got)
	if len(got) != 1 || got[0] != "match" {
		t.Errorf("expected only span 'match', got %v", got)
	}
	if *ok.Body.Total != 1 {
		t.Errorf("expected total 1, got %d", *ok.Body.Total)
	}
	if len(fake.requested) != 1 || len(fake.requested[0]) != 1 || fake.requested[0][0] != "1-5759e988-bd862e3fe1be46a994272793" {
		t.Errorf("expected one BatchGetTraces call for the trace, got %v", fake.requested)
	}
}

func visitSpanDetails(t *testing.T, resp gen.GetSpanDetailsForTraceResponseObject) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := resp.VisitGetSpanDetailsForTraceResponse(rec); err != nil {
		t.Fatalf("visit response: %v", err)
	}
	return rec
}

func TestGetSpanDetailsForTrace_NotFound(t *testing.T) {
	tests := []struct {
		name   string
		traces []xraytypes.Trace
		spanID string
	}{
		{"trace missing", nil, "match"},
		{"span missing", traceOf(scopedSegment("match", "ns-a", "p1", "c1", "e1")), "nonexistent"},
		{"span without openchoreo identity", traceOf(`{"id": "foreign", "name": "foreign", "start_time": 1.0, "end_time": 2.0}`), "foreign"},
	}

	var bodies []string
	for _, tt := range tests {
		handler := newHandlerWithXRay(&fakeXRay{traces: tt.traces})
		resp, err := handler.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
			TraceId: "5759e988bd862e3fe1be46a994272793",
			SpanId:  tt.spanID,
		})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.name, err)
		}
		rec := visitSpanDetails(t, resp)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d (%s)", tt.name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), tt.spanID) || strings.Contains(rec.Body.String(), "5759e988") {
			t.Errorf("%s: not-found body echoes request identifiers: %s", tt.name, rec.Body.String())
		}
		bodies = append(bodies, rec.Body.String())
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Errorf("not-found bodies differ, revealing which lookup missed: %q vs %q", bodies[0], bodies[i])
		}
	}
}

func TestGetSpanDetailsForTrace_Found(t *testing.T) {
	handler := newHandlerWithXRay(&fakeXRay{traces: traceOf(scopedSegment("match", "ns-a", "p1", "c1", "e1"))})
	resp, err := handler.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
		TraceId: "5759e988bd862e3fe1be46a994272793",
		SpanId:  "match",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, isOK := resp.(gen.GetSpanDetailsForTrace200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if ok.ResourceAttributes == nil || (*ok.ResourceAttributes)["openchoreo.dev/namespace"] != "ns-a" {
		t.Errorf("expected openchoreo.dev/namespace resource attribute, got %v", ok.ResourceAttributes)
	}
}

func TestHandlers_BackendErrorNotEchoed(t *testing.T) {
	handler := newHandlerWithXRay(&fakeXRay{err: errors.New(backendErrorText)})
	body := &gen.TracesQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: gen.ComponentSearchScope{Namespace: "ns-a"},
	}

	tracesResp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	traces500, ok := tracesResp.(gen.QueryTraces500JSONResponse)
	if !ok {
		t.Fatalf("expected 500 from QueryTraces, got %T", tracesResp)
	}
	if traces500.Detail != nil && strings.Contains(*traces500.Detail, "arn:aws") {
		t.Errorf("QueryTraces echoes backend error: %q", *traces500.Detail)
	}

	spansResp, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
		TraceId: "5759e988bd862e3fe1be46a994272793",
		Body:    body,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spans500, ok := spansResp.(gen.QuerySpansForTrace500JSONResponse)
	if !ok {
		t.Fatalf("expected 500 from QuerySpansForTrace, got %T", spansResp)
	}
	if spans500.Detail != nil && strings.Contains(*spans500.Detail, "arn:aws") {
		t.Errorf("QuerySpansForTrace echoes backend error: %q", *spans500.Detail)
	}

	detailResp, err := handler.GetSpanDetailsForTrace(context.Background(), gen.GetSpanDetailsForTraceRequestObject{
		TraceId: "5759e988bd862e3fe1be46a994272793",
		SpanId:  "match",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := visitSpanDetails(t, detailResp)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for a backend failure, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "arn:aws") {
		t.Errorf("GetSpanDetailsForTrace echoes backend error: %s", rec.Body.String())
	}
}
