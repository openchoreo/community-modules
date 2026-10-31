// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-aws-xray/internal/api/gen"
)

func lineScopes() map[string]struct {
	scope      gen.ComponentSearchScope
	wantHeader string
} {
	component, line, all := "c1", "l1", true
	return map[string]struct {
		scope      gen.ComponentSearchScope
		wantHeader string
	}{
		"one line":   {scope: gen.ComponentSearchScope{Namespace: "ns-a", Component: &component, ReleaseLine: &line}, wantHeader: "true"},
		"every line": {scope: gen.ComponentSearchScope{Namespace: "ns-a", Component: &component, AllReleaseLines: &all}, wantHeader: "true"},
		"main line":  {scope: gen.ComponentSearchScope{Namespace: "ns-a", Component: &component}, wantHeader: "true"},
		"namespace":  {scope: gen.ComponentSearchScope{Namespace: "ns-a", AllReleaseLines: &all}},
	}
}

func TestQueryTraces_ReleaseLineHeader(t *testing.T) {
	for name, tt := range lineScopes() {
		t.Run(name, func(t *testing.T) {
			resp, err := newHandlerWithXRay(&fakeXRay{}).QueryTraces(context.Background(), gen.QueryTracesRequestObject{
				Body: &gen.TracesQueryRequest{StartTime: time.Unix(0, 0), EndTime: time.Unix(10, 0), SearchScope: tt.scope},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, isOK := resp.(gen.QueryTraces200JSONResponse)
			if !isOK {
				t.Fatalf("expected 200 response, got %T", resp)
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != tt.wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, tt.wantHeader)
			}
		})
	}
}

func TestQuerySpansForTrace_ReleaseLineScope(t *testing.T) {
	lineSegment := func(id, lineUID string) string {
		return `{
			"id": "` + id + `", "name": "` + id + `", "start_time": 1465510988.0, "end_time": 1465510988.5,
			"annotations": {
				"openchoreo.dev_namespace": "ns-a",
				"openchoreo.dev_release_line_uid": "` + lineUID + `",
				"openchoreo.dev_release_line_component_uid": "c1"
			}
		}`
	}
	fake := &fakeXRay{traces: traceOf(
		scopedSegment("main", "ns-a", "p1", "c1", "e1"),
		lineSegment("line1", "l1"),
		lineSegment("line2", "l2"),
		scopedSegment("otherComponent", "ns-a", "p1", "c2", "e1"),
	)}
	want := map[string][]string{
		"one line":   {"line1"},
		"every line": {"line1", "line2", "main"},
		"main line":  {"main"},
		"namespace":  {"line1", "line2", "main", "otherComponent"},
	}
	for name, tt := range lineScopes() {
		t.Run(name, func(t *testing.T) {
			resp, err := newHandlerWithXRay(fake).QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
				TraceId: "5759e988bd862e3fe1be46a994272793",
				Body: &gen.TracesQueryRequest{
					StartTime: time.Unix(1465510988, 0), EndTime: time.Unix(1465510989, 0), SearchScope: tt.scope,
				},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, isOK := resp.(gen.QuerySpansForTrace200JSONResponse)
			if !isOK {
				t.Fatalf("expected 200 response, got %T", resp)
			}
			got := map[string]bool{}
			for _, s := range *ok.Body.Spans {
				got[*s.SpanId] = true
			}
			if len(got) != len(want[name]) {
				t.Errorf("spans = %v, want %v", got, want[name])
			}
			for _, id := range want[name] {
				if !got[id] {
					t.Errorf("spans = %v, want %v", got, want[name])
				}
			}
			if h := ok.Headers.OpenChoreoReleaseLineApplied; h != tt.wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", h, tt.wantHeader)
			}
		})
	}
}
