// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package xray

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/xray"
	xraytypes "github.com/aws/aws-sdk-go-v2/service/xray/types"
)

// lineIdentity is a release line of identityA1's component: its spans carry the release line UID
// and the component UID under the release line annotation instead of the component UID.
var lineIdentity = struct {
	testIdentity
	line, lineUID string
}{
	testIdentity: testIdentity{"ns-a", "proj-a", "comp-a1", "dev", "puid-a", "", "euid-dev"},
	line:         "next",
	lineUID:      "luid-next",
}

func lineAnnotations() map[string]interface{} {
	a := lineIdentity.annotations()
	a["openchoreo.dev_release_line_uid"] = lineIdentity.lineUID
	a["openchoreo.dev_release_line_component_uid"] = identityA1.componentUID
	return a
}

func lineMetadata() map[string]interface{} {
	m := lineIdentity.metadata()
	d := m["default"].(map[string]interface{})
	d["otel.resource.openchoreo.dev/component"] = identityA1.component
	d["otel.resource.openchoreo.dev/release-line"] = lineIdentity.line
	d["otel.resource.openchoreo.dev/release-line-uid"] = lineIdentity.lineUID
	d["otel.resource.openchoreo.dev/release-line-component-uid"] = identityA1.componentUID
	return m
}

func TestBuildFilterExpression_ReleaseLine(t *testing.T) {
	scope := Scope{Namespace: "ns", ComponentID: "comp", EnvironmentID: "env"}
	tests := []struct {
		name string
		edit func(*Scope)
		want []string
	}{
		{name: "main line", edit: func(*Scope) {},
			want: []string{`annotation[openchoreo.dev_namespace] = "ns" AND annotation[openchoreo.dev_component_uid] = "comp" AND annotation[openchoreo.dev_environment_uid] = "env"`}},
		{name: "every line", edit: func(s *Scope) { s.AllReleaseLines = true },
			want: []string{
				`annotation[openchoreo.dev_namespace] = "ns" AND annotation[openchoreo.dev_component_uid] = "comp" AND annotation[openchoreo.dev_environment_uid] = "env"`,
				`annotation[openchoreo.dev_namespace] = "ns" AND annotation[openchoreo.dev_release_line_component_uid] = "comp" AND annotation[openchoreo.dev_environment_uid] = "env"`,
			}},
		{name: "one line matches on its UID alone", edit: func(s *Scope) { s.ReleaseLineID, s.AllReleaseLines = "line", true },
			want: []string{`annotation[openchoreo.dev_namespace] = "ns" AND annotation[openchoreo.dev_release_line_uid] = "line" AND annotation[openchoreo.dev_environment_uid] = "env"`}},
		{name: "every line without a component", edit: func(s *Scope) { s.ComponentID, s.AllReleaseLines = "", true },
			want: []string{`annotation[openchoreo.dev_namespace] = "ns" AND annotation[openchoreo.dev_environment_uid] = "env"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := scope
			tt.edit(&s)
			got := traceFilterExpressions(s)
			if len(got) != len(tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("expression %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMatchesScope_ReleaseLine(t *testing.T) {
	main := identityA1.annotations()
	line := lineAnnotations()
	other := identityA2.annotations()
	base := Scope{Namespace: "ns-a", ComponentID: identityA1.componentUID}
	tests := []struct {
		name  string
		scope func(Scope) Scope
		want  map[string]bool
	}{
		{name: "main line", scope: func(s Scope) Scope { return s },
			want: map[string]bool{"main": true, "line": false, "other": false}},
		{name: "every line", scope: func(s Scope) Scope { s.AllReleaseLines = true; return s },
			want: map[string]bool{"main": true, "line": true, "other": false}},
		{name: "one line", scope: func(s Scope) Scope { s.ReleaseLineID = lineIdentity.lineUID; return s },
			want: map[string]bool{"main": false, "line": true, "other": false}},
		{name: "namespace", scope: func(s Scope) Scope { s.ComponentID, s.AllReleaseLines = "", true; return s },
			want: map[string]bool{"main": true, "line": true, "other": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := tt.scope(base)
			for name, annotations := range map[string]map[string]interface{}{"main": main, "line": line, "other": other} {
				if got := matchesScope(annotations, scope); got != tt.want[name] {
					t.Errorf("%s span: matchesScope = %v, want %v", name, got, tt.want[name])
				}
			}
		})
	}
}

// A release line's spans get their names from the segment metadata and keep their line identity.
func TestGetSpans_ReleaseLineResourceAttributes(t *testing.T) {
	client := newTraceClient(t,
		testSegment("segLine", "line", lineAnnotations(), lineMetadata(),
			testSubsegment("subLine", lineAnnotations()),
			testSubsegment("subMain", identityA1.annotations())),
	)
	result, err := client.GetSpans(context.Background(), TracesQueryParams{
		TraceID:           "5759e988bd862e3fe1be46a994272793",
		Scope:             Scope{Namespace: "ns-a", ComponentID: identityA1.componentUID, AllReleaseLines: true},
		IncludeAttributes: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := spanIDs(result.Spans); !equalIDs(got, []string{"segLine", "subLine", "subMain"}) {
		t.Fatalf("spans = %v, want every line's spans", got)
	}
	want := map[string]string{
		"openchoreo.dev/namespace":                  "ns-a",
		"openchoreo.dev/project":                    "proj-a",
		"openchoreo.dev/component":                  identityA1.component,
		"openchoreo.dev/release-line":               lineIdentity.line,
		"openchoreo.dev/release-line-uid":           lineIdentity.lineUID,
		"openchoreo.dev/release-line-component-uid": identityA1.componentUID,
	}
	for _, s := range result.Spans {
		switch s.SpanID {
		case "segLine", "subLine":
			assertResourceAttrs(t, s.SpanID, s.ResourceAttributes, want, "openchoreo.dev/component-uid")
		case "subMain":
			// The main line span shares no identity with its release line segment, so it inherits no names.
			assertResourceAttrs(t, s.SpanID, s.ResourceAttributes, identityOnlyResource(identityA1),
				append(resourceNameKeys, "openchoreo.dev/release-line-uid")...)
		}
	}
}

// Every line queries the main line's and the release lines' annotations and merges the traces.
func TestGetTraces_AllReleaseLinesMergesTraces(t *testing.T) {
	at := func(sec int64) *time.Time { ts := time.Unix(sec, 0); return &ts }
	summaries := map[string][]xraytypes.TraceSummary{
		"openchoreo.dev_component_uid": {
			{Id: aws.String("1-00000003-000000000000000000000003"), StartTime: at(3)},
			{Id: aws.String("1-00000001-000000000000000000000001"), StartTime: at(1)},
		},
		"openchoreo.dev_release_line_component_uid": {
			{Id: aws.String("1-00000003-000000000000000000000003"), StartTime: at(3)},
			{Id: aws.String("1-00000002-000000000000000000000002"), StartTime: at(2)},
		},
	}
	var filters []string
	mock := &mockXRayClient{
		getSummariesFn: func(_ context.Context, input *xray.GetTraceSummariesInput, _ ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
			filter := aws.ToString(input.FilterExpression)
			filters = append(filters, filter)
			for annotation, s := range summaries {
				if searchSubstring(filter, "annotation["+annotation+"]") {
					return &xray.GetTraceSummariesOutput{TraceSummaries: s}, nil
				}
			}
			t.Fatalf("unexpected filter expression %q", filter)
			return nil, nil
		},
	}
	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())

	result, err := client.GetTraces(context.Background(), TracesQueryParams{
		StartTime: time.Unix(0, 0), EndTime: time.Unix(10, 0),
		Scope: Scope{Namespace: "ns", ComponentID: "comp", AllReleaseLines: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filters) != 2 {
		t.Fatalf("expected two trace summary queries, got %q", filters)
	}
	var got []string
	for _, tr := range result.Traces {
		got = append(got, tr.TraceID)
	}
	want := []string{
		"00000003000000000000000000000003",
		"00000002000000000000000000000002",
		"00000001000000000000000000000001",
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("traces = %v, want %v, newest first without duplicates", got, want)
	}
	if result.Total != 3 {
		t.Errorf("total = %d, want 3", result.Total)
	}
}

// Merging every line's queries caps the total at MaxQueryLimit.
func TestGetTraces_AllReleaseLinesCapsTotal(t *testing.T) {
	summaries := func(offset int) []xraytypes.TraceSummary {
		out := make([]xraytypes.TraceSummary, MaxQueryLimit)
		for i := range out {
			n := offset + i
			ts := time.Unix(int64(n), 0)
			out[i] = xraytypes.TraceSummary{Id: aws.String(fmt.Sprintf("1-%08x-%024x", n, n)), StartTime: &ts}
		}
		return out
	}
	mock := &mockXRayClient{
		getSummariesFn: func(_ context.Context, input *xray.GetTraceSummariesInput, _ ...func(*xray.Options)) (*xray.GetTraceSummariesOutput, error) {
			if searchSubstring(aws.ToString(input.FilterExpression), "annotation[openchoreo.dev_release_line_component_uid]") {
				return &xray.GetTraceSummariesOutput{TraceSummaries: summaries(MaxQueryLimit)}, nil
			}
			return &xray.GetTraceSummariesOutput{TraceSummaries: summaries(0)}, nil
		},
	}
	client := NewClientWithAWS(mock, &mockSTSClient{}, testLogger())

	result, err := client.GetTraces(context.Background(), TracesQueryParams{
		StartTime: time.Unix(0, 0), EndTime: time.Unix(3*MaxQueryLimit, 0), Limit: MaxQueryLimit,
		Scope: Scope{Namespace: "ns", ComponentID: "comp", AllReleaseLines: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != MaxQueryLimit || len(result.Traces) != MaxQueryLimit {
		t.Errorf("total, traces = %d, %d, want %d, %d", result.Total, len(result.Traces), MaxQueryLimit, MaxQueryLimit)
	}
}
