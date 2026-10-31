// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/trace/apiv1/tracepb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testComponentUID   = "a38a0603-bb5a-4b13-b326-3b831628c3fb"
	testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
)

func TestBuildScopeFilters_ReleaseLine(t *testing.T) {
	const ns = "+openchoreo.dev/namespace:default "
	tests := []struct {
		name          string
		p             TracesParams
		want          []string
		tracesApplied bool
		spansApplied  bool
	}{
		{
			name:         "main line",
			p:            TracesParams{Namespace: "default", ComponentUID: testComponentUID},
			want:         []string{ns + "+openchoreo.dev/component-uid:" + testComponentUID},
			spansApplied: true,
		},
		{
			name:          "one release line matches its UID alone",
			p:             TracesParams{Namespace: "default", ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID},
			want:          []string{ns + "+openchoreo.dev/release-line-uid:" + testReleaseLineUID},
			tracesApplied: true,
			spansApplied:  true,
		},
		{
			name:          "one release line takes precedence over every line",
			p:             TracesParams{Namespace: "default", ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID, AllReleaseLines: true},
			want:          []string{ns + "+openchoreo.dev/release-line-uid:" + testReleaseLineUID},
			tracesApplied: true,
			spansApplied:  true,
		},
		{
			name: "every line takes a filter per component label",
			p:    TracesParams{Namespace: "default", ComponentUID: testComponentUID, AllReleaseLines: true},
			want: []string{
				ns + "+openchoreo.dev/component-uid:" + testComponentUID,
				ns + "+openchoreo.dev/release-line-component-uid:" + testComponentUID,
			},
			tracesApplied: true,
			spansApplied:  true,
		},
		{
			name: "every line without a component",
			p:    TracesParams{Namespace: "default", AllReleaseLines: true},
			want: []string{"+openchoreo.dev/namespace:default"},
		},
		{
			name:         "zero UUID release line is the main line",
			p:            TracesParams{Namespace: "default", ComponentUID: testComponentUID, ReleaseLineUID: zeroUUID},
			want:         []string{ns + "+openchoreo.dev/component-uid:" + testComponentUID},
			spansApplied: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildScopeFilters(tt.p)
			if err != nil {
				t.Fatalf("BuildScopeFilters: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("BuildScopeFilters() = %q, want %q", got, tt.want)
			}
			if got := TracesReleaseLineApplied(tt.p); got != tt.tracesApplied {
				t.Errorf("TracesReleaseLineApplied = %v, want %v", got, tt.tracesApplied)
			}
			if got := SpansReleaseLineApplied(tt.p); got != tt.spansApplied {
				t.Errorf("SpansReleaseLineApplied = %v, want %v", got, tt.spansApplied)
			}
		})
	}
}

func TestBuildScopeFilters_RejectsInvalidReleaseLine(t *testing.T) {
	if _, err := BuildScopeFilters(TracesParams{Namespace: "default", ReleaseLineUID: "x +evil:1"}); err == nil {
		t.Error("expected an error for a release line UID with filter metacharacters")
	}
}

func TestMatchesScope_ReleaseLine(t *testing.T) {
	mainSpan := map[string]string{LabelNamespace: "default", LabelComponentUID: testComponentUID}
	lineSpan := map[string]string{
		LabelNamespace:               "default",
		LabelReleaseLineUID:          testReleaseLineUID,
		LabelReleaseLineComponentUID: testComponentUID,
	}
	mainScope := TracesParams{Namespace: "default", ComponentUID: testComponentUID}
	lineScope := TracesParams{Namespace: "default", ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID}
	allScope := TracesParams{Namespace: "default", ComponentUID: testComponentUID, AllReleaseLines: true}

	tests := []struct {
		name   string
		labels map[string]string
		p      TracesParams
		want   bool
	}{
		{"main line span in the main line", mainSpan, mainScope, true},
		{"release line span outside the main line", lineSpan, mainScope, false},
		{"release line span in its line", lineSpan, lineScope, true},
		{"main line span outside a release line", mainSpan, lineScope, false},
		{"main line span in every line", mainSpan, allScope, true},
		{"release line span in every line", lineSpan, allScope, true},
		{
			"span carrying both labels is not the main line",
			map[string]string{LabelNamespace: "default", LabelComponentUID: testComponentUID, LabelReleaseLineUID: testReleaseLineUID},
			mainScope, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesScope(tt.labels, tt.p); got != tt.want {
				t.Errorf("matchesScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Every line of a component lists traces under both component labels and
// merges them into the first limit in sort order, each trace once.
func TestQueryTraces_EveryLineMergesBothLabels(t *testing.T) {
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	trace := func(id string, minute int) *tracepb.Trace {
		return &tracepb.Trace{TraceId: id, Spans: []*tracepb.TraceSpan{{
			SpanId:    1,
			StartTime: timestamppb.New(base.Add(time.Duration(minute) * time.Minute)),
			EndTime:   timestamppb.New(base.Add(time.Duration(minute)*time.Minute + time.Second)),
		}}}
	}
	var filters []string
	c := newClientWithAPI(Config{ProjectID: "p"}, testLogger(),
		func(_ context.Context, req *tracepb.ListTracesRequest, _ int) ([]*tracepb.Trace, error) {
			filters = append(filters, req.GetFilter())
			if strings.Contains(req.GetFilter(), LabelReleaseLineComponentUID) {
				return []*tracepb.Trace{trace("line-new", 5), trace("shared", 3)}, nil
			}
			return []*tracepb.Trace{trace("main-new", 4), trace("shared", 3), trace("main-old", 1)}, nil
		},
		nil,
	)

	result, err := c.QueryTraces(context.Background(), TracesParams{
		Namespace: "default", ComponentUID: testComponentUID, AllReleaseLines: true, Limit: 3, SortOrder: "desc",
	})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(filters) != 2 {
		t.Fatalf("ran %d ListTraces, want 2: %q", len(filters), filters)
	}
	var ids []string
	for _, e := range result.Traces {
		ids = append(ids, e.TraceID)
	}
	if want := []string{"line-new", "main-new", "shared"}; !slices.Equal(ids, want) {
		t.Errorf("traces = %v, want %v", ids, want)
	}
	if result.Total != 3 {
		t.Errorf("Total = %d, want 3", result.Total)
	}
}

// A trace's spans keep other components' spans, and narrow the scope's
// component to the requested line.
func TestQuerySpans_NarrowsTheComponentToItsLine(t *testing.T) {
	const otherComponentUID = "0b6f8f63-9d41-4d6b-8f5e-6c1f3c2e4a10"
	base := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	span := func(id uint64, offset time.Duration, labels map[string]string) *tracepb.TraceSpan {
		labels[LabelNamespace] = "default"
		return &tracepb.TraceSpan{SpanId: id, StartTime: timestamppb.New(base.Add(offset)), Labels: labels}
	}
	c := newClientWithAPI(Config{ProjectID: "p"}, testLogger(), nil,
		func(_ context.Context, req *tracepb.GetTraceRequest) (*tracepb.Trace, error) {
			return &tracepb.Trace{TraceId: req.GetTraceId(), Spans: []*tracepb.TraceSpan{
				span(1, 0, map[string]string{LabelComponentUID: otherComponentUID}),
				span(2, time.Second, map[string]string{LabelComponentUID: testComponentUID}),
				span(3, 2*time.Second, map[string]string{
					LabelReleaseLineUID: testReleaseLineUID, LabelReleaseLineComponentUID: testComponentUID,
				}),
				span(4, 3*time.Second, map[string]string{
					LabelReleaseLineUID: "0c4e1a77-5b0d-4a2f-9f3e-8d2b6a1c7e55", LabelReleaseLineComponentUID: otherComponentUID,
				}),
			}}, nil
		},
	)

	tests := []struct {
		name string
		p    TracesParams
		want []string
	}{
		{
			name: "main line leaves out the component's release lines",
			p:    TracesParams{Namespace: "default", ComponentUID: testComponentUID},
			want: []string{"0000000000000001", "0000000000000002", "0000000000000004"},
		},
		{
			name: "one release line leaves out the component's other lines",
			p:    TracesParams{Namespace: "default", ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID},
			want: []string{"0000000000000001", "0000000000000003", "0000000000000004"},
		},
		{
			name: "every line keeps the whole trace",
			p:    TracesParams{Namespace: "default", ComponentUID: testComponentUID, AllReleaseLines: true},
			want: []string{"0000000000000001", "0000000000000002", "0000000000000003", "0000000000000004"},
		},
		{
			name: "a release line not in the trace gets no spans",
			p:    TracesParams{Namespace: "default", ComponentUID: testComponentUID, ReleaseLineUID: "5e2d9c18-7a3b-4f60-b1d4-9c8e7f6a5b43"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.p.TraceID = "ff"
			tt.p.Limit = 10
			result, err := c.QuerySpans(context.Background(), tt.p)
			if err != nil {
				t.Fatalf("QuerySpans: %v", err)
			}
			var ids []string
			for _, s := range result.Spans {
				ids = append(ids, s.SpanID)
			}
			if !slices.Equal(ids, tt.want) {
				t.Errorf("spans = %v, want %v", ids, tt.want)
			}
			if result.Total != len(tt.want) {
				t.Errorf("Total = %d, want %d", result.Total, len(tt.want))
			}
		})
	}
}
