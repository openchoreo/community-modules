// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import (
	"testing"
	"time"

	"cloud.google.com/go/trace/apiv1/tracepb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ts(t *testing.T, s string) *timestamppb.Timestamp {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad test timestamp %q: %v", s, err)
	}
	return timestamppb.New(parsed)
}

func TestSpanIDHexRoundTrip(t *testing.T) {
	for _, id := range []uint64{0x1, 0xdeadbeefcafe0123, ^uint64(0)} {
		hex := SpanIDHex(id)
		if len(hex) != 16 {
			t.Errorf("SpanIDHex(%d) = %q, want 16 chars", id, hex)
		}
		back, err := ParseSpanID(hex)
		if err != nil {
			t.Fatalf("ParseSpanID(%q): %v", hex, err)
		}
		if back != id {
			t.Errorf("round trip %d -> %q -> %d", id, hex, back)
		}
	}
}

func TestParseSpanIDRejectsOverlongInput(t *testing.T) {
	if _, err := ParseSpanID("0123456789abcdef0"); err == nil {
		t.Error("ParseSpanID accepted a 17-char id")
	}
}

func TestMapSpan(t *testing.T) {
	span := &tracepb.TraceSpan{
		SpanId:       0xabc,
		ParentSpanId: 0xdef,
		Kind:         tracepb.TraceSpan_RPC_SERVER,
		Name:         "GET /orders",
		StartTime:    ts(t, "2026-07-14T10:00:00Z"),
		EndTime:      ts(t, "2026-07-14T10:00:01Z"),
		Labels: map[string]string{
			"g.co/status/code":         "0",
			"http.method":              "GET",
			"openchoreo.dev/namespace": "default",
			"k8s.pod.name":             "orders-abc",
			"g.co/agent":               "opentelemetry-collector",
		},
	}

	got := mapSpan(span, true)

	if got.SpanID != "0000000000000abc" {
		t.Errorf("SpanID = %q", got.SpanID)
	}
	if got.ParentSpanID != "0000000000000def" {
		t.Errorf("ParentSpanID = %q", got.ParentSpanID)
	}
	if got.SpanKind != "SERVER" {
		t.Errorf("SpanKind = %q, want SERVER", got.SpanKind)
	}
	if got.Status != "ok" {
		t.Errorf("Status = %q, want ok", got.Status)
	}
	if got.DurationNanoseconds != time.Second.Nanoseconds() {
		t.Errorf("DurationNanoseconds = %d", got.DurationNanoseconds)
	}
	if _, ok := got.Attributes["http.method"]; !ok {
		t.Error("http.method should be a span attribute")
	}
	for _, key := range []string{"openchoreo.dev/namespace", "k8s.pod.name", "g.co/agent"} {
		if _, ok := got.ResourceAttributes[key]; !ok {
			t.Errorf("%s should be a resource attribute", key)
		}
		if _, ok := got.Attributes[key]; ok {
			t.Errorf("%s should not be a span attribute", key)
		}
	}
}

func TestMapSpanRootAndNoAttributes(t *testing.T) {
	span := &tracepb.TraceSpan{
		SpanId: 1,
		Labels: map[string]string{"http.method": "GET"},
	}
	got := mapSpan(span, false)
	if got.ParentSpanID != "" {
		t.Errorf("root span ParentSpanID = %q, want empty", got.ParentSpanID)
	}
	if got.Attributes != nil || got.ResourceAttributes != nil {
		t.Error("attributes should be omitted when includeAttributes is false")
	}
}

func TestSpanKindFallback(t *testing.T) {
	tests := []struct {
		name string
		span *tracepb.TraceSpan
		want string
	}{
		{"rpc client", &tracepb.TraceSpan{Kind: tracepb.TraceSpan_RPC_CLIENT}, "CLIENT"},
		{"kind label fallback", &tracepb.TraceSpan{Labels: map[string]string{"g.co/spankind": "producer"}}, "PRODUCER"},
		{"no signal defaults to internal", &tracepb.TraceSpan{}, "INTERNAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spanKind(tt.span); got != tt.want {
				t.Errorf("spanKind() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"status code ok", map[string]string{"g.co/status/code": "0"}, "ok"},
		{"status code error", map[string]string{"g.co/status/code": "2"}, "error"},
		{"otel status ok", map[string]string{"otel.status_code": "OK"}, "ok"},
		{"otel status error", map[string]string{"otel.status_code": "ERROR"}, "error"},
		{"error flag", map[string]string{"error": "true"}, "error"},
		{"http 500", map[string]string{"/http/status_code": "503"}, "error"},
		{"http 200", map[string]string{"/http/status_code": "200"}, "ok"},
		{"no signal", map[string]string{}, "unset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusFromLabels(tt.labels); got != tt.want {
				t.Errorf("statusFromLabels() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSummarizeTrace(t *testing.T) {
	trace := &tracepb.Trace{
		TraceId: "0123456789abcdef0123456789abcdef",
		Spans: []*tracepb.TraceSpan{
			{
				SpanId:       2,
				ParentSpanId: 1,
				Name:         "child",
				StartTime:    ts(t, "2026-07-14T10:00:00.5Z"),
				EndTime:      ts(t, "2026-07-14T10:00:02Z"),
				Labels:       map[string]string{"g.co/status/code": "13"},
			},
			{
				SpanId:    1,
				Kind:      tracepb.TraceSpan_RPC_SERVER,
				Name:      "GET /orders",
				StartTime: ts(t, "2026-07-14T10:00:00Z"),
				EndTime:   ts(t, "2026-07-14T10:00:01Z"),
			},
		},
	}

	got := summarizeTrace(trace)

	if got.TraceID != trace.TraceId {
		t.Errorf("TraceID = %q", got.TraceID)
	}
	if got.SpanCount != 2 {
		t.Errorf("SpanCount = %d, want 2", got.SpanCount)
	}
	if got.RootSpanID != "0000000000000001" {
		t.Errorf("RootSpanID = %q", got.RootSpanID)
	}
	if got.RootSpanName != "GET /orders" || got.TraceName != "GET /orders" {
		t.Errorf("RootSpanName = %q, TraceName = %q", got.RootSpanName, got.TraceName)
	}
	if got.RootSpanKind != "SERVER" {
		t.Errorf("RootSpanKind = %q", got.RootSpanKind)
	}
	if !got.HasErrors {
		t.Error("HasErrors = false, want true (child has error status)")
	}
	// Trace spans 10:00:00 .. 10:00:02 -> 2s.
	if got.DurationNs != (2 * time.Second).Nanoseconds() {
		t.Errorf("DurationNs = %d", got.DurationNs)
	}
}

func TestSummarizeTraceFallsBackToEarliestSpan(t *testing.T) {
	trace := &tracepb.Trace{
		TraceId: "ff",
		Spans: []*tracepb.TraceSpan{
			{SpanId: 5, ParentSpanId: 9, Name: "late", StartTime: ts(t, "2026-07-14T10:00:05Z"), EndTime: ts(t, "2026-07-14T10:00:06Z")},
			{SpanId: 4, ParentSpanId: 9, Name: "early", StartTime: ts(t, "2026-07-14T10:00:01Z"), EndTime: ts(t, "2026-07-14T10:00:02Z")},
		},
	}
	got := summarizeTrace(trace)
	if got.RootSpanName != "early" {
		t.Errorf("RootSpanName = %q, want fallback to earliest span", got.RootSpanName)
	}
	if got.HasErrors {
		t.Error("HasErrors = true, want false")
	}
}
