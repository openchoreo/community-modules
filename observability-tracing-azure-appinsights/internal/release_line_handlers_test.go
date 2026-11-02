// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"testing"

	"github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal/appinsights"
)

const (
	testComponentUID   = "9b2e5f1a-3c4d-4e6f-8a1b-2c3d4e5f6a7b"
	testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
)

var scopeCases = map[string]struct {
	component  *string
	line       *string
	allLines   *bool
	wantLine   string
	wantAll    bool
	wantHeader string
}{
	"main line":    {component: ptr(testComponentUID), wantHeader: "true"},
	"every line":   {component: ptr(testComponentUID), allLines: ptr(true), wantAll: true, wantHeader: "true"},
	"one line":     {component: ptr(testComponentUID), line: ptr(" " + testReleaseLineUID + " "), wantLine: testReleaseLineUID, wantHeader: "true"},
	"no component": {allLines: ptr(true), wantAll: true},
}

func scopedBody(component, line *string, allLines *bool) *gen.TracesQueryRequest {
	body := validBody()
	body.SearchScope.Component = component
	body.SearchScope.ReleaseLine = line
	body.SearchScope.AllReleaseLines = allLines
	return body
}

func TestQueryTraces_ReleaseLineScope(t *testing.T) {
	for name, tc := range scopeCases {
		t.Run(name, func(t *testing.T) {
			var got appinsights.TracesParams
			h := testHandler(&mockClient{
				queryTracesFn: func(_ context.Context, p appinsights.TracesParams) (*appinsights.TracesResult, error) {
					got = p
					return &appinsights.TracesResult{}, nil
				},
			})
			resp, err := h.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: scopedBody(tc.component, tc.line, tc.allLines)})
			if err != nil {
				t.Fatal(err)
			}
			ok, isOK := resp.(gen.QueryTraces200JSONResponse)
			if !isOK {
				t.Fatalf("got %T, want 200", resp)
			}
			if got.ReleaseLineUID != tc.wantLine || got.AllReleaseLines != tc.wantAll {
				t.Errorf("release line scope not passed: line %q all %v", got.ReleaseLineUID, got.AllReleaseLines)
			}
			if h := ok.Headers.OpenChoreoReleaseLineApplied; h != tc.wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", h, tc.wantHeader)
			}
		})
	}
}

func TestQuerySpansForTrace_ReleaseLineScope(t *testing.T) {
	for name, tc := range scopeCases {
		t.Run(name, func(t *testing.T) {
			var got appinsights.TracesParams
			h := testHandler(&mockClient{
				querySpansFn: func(_ context.Context, p appinsights.TracesParams) (*appinsights.SpansResult, error) {
					got = p
					return &appinsights.SpansResult{}, nil
				},
			})
			resp, err := h.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{
				TraceId: "4372fc01295900a9",
				Body:    scopedBody(tc.component, tc.line, tc.allLines),
			})
			if err != nil {
				t.Fatal(err)
			}
			ok, isOK := resp.(gen.QuerySpansForTrace200JSONResponse)
			if !isOK {
				t.Fatalf("got %T, want 200", resp)
			}
			if got.ReleaseLineUID != tc.wantLine || got.AllReleaseLines != tc.wantAll {
				t.Errorf("release line scope not passed: line %q all %v", got.ReleaseLineUID, got.AllReleaseLines)
			}
			if h := ok.Headers.OpenChoreoReleaseLineApplied; h != tc.wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", h, tc.wantHeader)
			}
		})
	}
}
