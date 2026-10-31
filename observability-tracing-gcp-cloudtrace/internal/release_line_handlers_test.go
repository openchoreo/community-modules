// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"testing"

	"github.com/openchoreo/community-modules/observability-tracing-gcp-cloudtrace/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-gcp-cloudtrace/internal/cloudtrace"
)

const (
	testComponentUID   = "a38a0603-bb5a-4b13-b326-3b831628c3fb"
	testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
)

func TestReleaseLineHeader(t *testing.T) {
	tests := map[string]struct {
		component   *string
		line        *string
		allLines    *bool
		traceHeader string
		spanHeader  string
	}{
		"one line":   {component: ptr(testComponentUID), line: ptr(testReleaseLineUID), traceHeader: "true", spanHeader: "true"},
		"every line": {component: ptr(testComponentUID), allLines: ptr(true), traceHeader: "true", spanHeader: "true"},
		// A trace filter can't leave out release line traces, but a span list is narrowed per span.
		"main line":      {component: ptr(testComponentUID), spanHeader: "true"},
		"namespace only": {allLines: ptr(true)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{tracesResult: &cloudtrace.TracesResult{}, spansResult: &cloudtrace.SpansResult{}}
			h := newHandler(client)
			body := validBody()
			body.SearchScope.Component = tt.component
			body.SearchScope.ReleaseLine = tt.line
			body.SearchScope.AllReleaseLines = tt.allLines

			resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body})
			if err != nil {
				t.Fatalf("QueryTraces: %v", err)
			}
			traces, ok := resp.(gen.QueryTraces200JSONResponse)
			if !ok {
				t.Fatalf("resp = %T", resp)
			}
			if got := traces.Headers.OpenChoreoReleaseLineApplied; got != tt.traceHeader {
				t.Errorf("traces OpenChoreo-Release-Line-Applied = %q, want %q", got, tt.traceHeader)
			}
			if tt.line != nil && client.tracesParams.ReleaseLineUID != *tt.line {
				t.Errorf("release line not forwarded: %+v", client.tracesParams)
			}
			if client.tracesParams.AllReleaseLines != (tt.allLines != nil) {
				t.Errorf("allReleaseLines not forwarded: %+v", client.tracesParams)
			}

			resp2, err := h.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
				TraceId: "0123456789abcdef0123456789abcdef",
				Body:    body,
			})
			if err != nil {
				t.Fatalf("QuerySpansForTrace: %v", err)
			}
			spans, ok := resp2.(gen.QuerySpansForTrace200JSONResponse)
			if !ok {
				t.Fatalf("resp = %T", resp2)
			}
			if got := spans.Headers.OpenChoreoReleaseLineApplied; got != tt.spanHeader {
				t.Errorf("spans OpenChoreo-Release-Line-Applied = %q, want %q", got, tt.spanHeader)
			}
		})
	}
}
