// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package xray

import (
	"encoding/json"
	"testing"
)

func TestBuildFilterExpression_AllFields(t *testing.T) {
	scope := Scope{
		Namespace:     "my-namespace",
		ProjectID:     "proj-123",
		ComponentID:   "comp-456",
		EnvironmentID: "env-789",
	}

	expr := buildFilterExpression(scope)

	expected := []string{
		`annotation[openchoreo.dev_namespace] = "my-namespace"`,
		`annotation[openchoreo.dev_project_uid] = "proj-123"`,
		`annotation[openchoreo.dev_component_uid] = "comp-456"`,
		`annotation[openchoreo.dev_environment_uid] = "env-789"`,
	}

	for _, want := range expected {
		if !contains(expr, want) {
			t.Errorf("expected filter expression to contain %q, got %q", want, expr)
		}
	}
}

func TestBuildFilterExpression_NamespaceOnly(t *testing.T) {
	scope := Scope{
		Namespace: "test-ns",
	}

	expr := buildFilterExpression(scope)
	want := `annotation[openchoreo.dev_namespace] = "test-ns"`
	if expr != want {
		t.Errorf("expected %q, got %q", want, expr)
	}
}

func TestBuildFilterExpression_Empty(t *testing.T) {
	scope := Scope{}
	expr := buildFilterExpression(scope)
	if expr != "" {
		t.Errorf("expected empty filter expression, got %q", expr)
	}
}

func TestToXRayTraceID_OTLPFormat(t *testing.T) {
	otlpID := "5759e988bd862e3fe1be46a994272793"
	got := toXRayTraceID(otlpID)
	want := "1-5759e988-bd862e3fe1be46a994272793"
	if got != want {
		t.Errorf("toXRayTraceID(%q) = %q, want %q", otlpID, got, want)
	}
}

func TestToXRayTraceID_AlreadyXRayFormat(t *testing.T) {
	xrayID := "1-5759e988-bd862e3fe1be46a994272793"
	got := toXRayTraceID(xrayID)
	if got != xrayID {
		t.Errorf("toXRayTraceID(%q) = %q, want %q", xrayID, got, xrayID)
	}
}

func TestFromXRayTraceID(t *testing.T) {
	xrayID := "1-5759e988-bd862e3fe1be46a994272793"
	got := fromXRayTraceID(xrayID)
	want := "5759e988bd862e3fe1be46a994272793"
	if got != want {
		t.Errorf("fromXRayTraceID(%q) = %q, want %q", xrayID, got, want)
	}
}

func TestFromXRayTraceID_NonXRayFormat(t *testing.T) {
	plainID := "5759e988bd862e3fe1be46a994272793"
	got := fromXRayTraceID(plainID)
	if got != plainID {
		t.Errorf("fromXRayTraceID(%q) = %q, want %q", plainID, got, plainID)
	}
}

func TestSegmentStatus(t *testing.T) {
	tests := []struct {
		name     string
		seg      xraySegment
		expected string
	}{
		{"fault", xraySegment{Fault: true, EndTime: json.Number("1.0")}, "error"},
		{"error", xraySegment{Error: true, EndTime: json.Number("1.0")}, "error"},
		{"throttle", xraySegment{Throttle: true, EndTime: json.Number("1.0")}, "error"},
		{"ok", xraySegment{EndTime: json.Number("1.0")}, "ok"},
		{"unset", xraySegment{}, "unset"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := segmentStatus(&tt.seg)
			if got != tt.expected {
				t.Errorf("segmentStatus() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSegmentKind(t *testing.T) {
	tests := []struct {
		name     string
		seg      xraySegment
		expected string
	}{
		{"server with origin", xraySegment{Origin: "AWS::EKS::Container"}, "SERVER"},
		{"client subsegment", xraySegment{Type: "subsegment", Namespace: "remote"}, "CLIENT"},
		{"aws subsegment", xraySegment{Type: "subsegment", Namespace: "aws"}, "CLIENT"},
		{"internal subsegment", xraySegment{Type: "subsegment"}, "INTERNAL"},
		{"default server", xraySegment{}, "SERVER"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := segmentKind(&tt.seg)
			if got != tt.expected {
				t.Errorf("segmentKind() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestExtractStartTimeFromXRayID(t *testing.T) {
	id := "1-5759e988-bd862e3fe1be46a994272793"
	ts := extractStartTimeFromXRayID(&id)
	if ts.IsZero() {
		t.Fatal("expected non-zero time")
	}
	if ts.Unix() != 0x5759e988 {
		t.Errorf("expected Unix timestamp 0x5759e988, got %d", ts.Unix())
	}
}

func TestExtractStartTimeFromXRayID_InvalidFormat(t *testing.T) {
	id := "not-an-xray-id"
	ts := extractStartTimeFromXRayID(&id)
	if !ts.IsZero() {
		t.Errorf("expected zero time for invalid format, got %v", ts)
	}
}

func TestJSONNumberToNanos(t *testing.T) {
	tests := []struct {
		name string
		in   json.Number
		want int64
	}{
		{
			name: "plain decimal",
			in:   json.Number("1778478205.001234567"),
			want: 1778478205001234567,
		},
		{
			name: "scientific notation",
			in:   json.Number("1.778478205001234567e+09"),
			want: 1778478205001234567,
		},
		{
			name: "truncates beyond nanoseconds",
			in:   json.Number("1778478205.0012345678"),
			want: 1778478205001234567,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonNumberToNanos(tt.in); got != tt.want {
				t.Errorf("jsonNumberToNanos(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestFlattenMap(t *testing.T) {
	m := map[string]interface{}{
		"method":  "GET",
		"url":     "https://example.com",
		"request": map[string]interface{}{"content_length": 100},
	}

	result := flattenMap("http", m)

	if v, ok := result["http.method"]; !ok || v != "GET" {
		t.Errorf("expected http.method=GET, got %v", v)
	}
	if v, ok := result["http.url"]; !ok || v != "https://example.com" {
		t.Errorf("expected http.url, got %v", v)
	}
	if v, ok := result["http.request.content_length"]; !ok || v != 100 {
		t.Errorf("expected http.request.content_length=100, got %v", v)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}
