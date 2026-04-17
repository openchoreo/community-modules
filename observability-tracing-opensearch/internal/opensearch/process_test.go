// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"testing"
	"time"
)

func TestParseSpanEntry_NilSource(t *testing.T) {
	hit := Hit{Source: nil}
	span := ParseSpanEntry(hit)
	if span.SpanID != "" {
		t.Errorf("expected empty spanID, got %q", span.SpanID)
	}
}

func TestParseSpanEntry_FullSpan(t *testing.T) {
	startTime := "2025-06-01T12:00:00.000000000Z"
	endTime := "2025-06-01T12:00:01.000000000Z"

	hit := Hit{
		Source: map[string]interface{}{
			"spanId":       "span-1",
			"traceId":      "trace-1",
			"name":         "GET /api/users",
			"parentSpanId": "span-root",
			"kind":         "SERVER",
			"startTime":    startTime,
			"endTime":      endTime,
			"status": map[string]interface{}{
				"code": "ok",
			},
			"attributes": map[string]interface{}{
				"http.method": "GET",
			},
			"resource": map[string]interface{}{
				"openchoreo.dev/component-uid": "comp-123",
				"openchoreo.dev/project-uid":   "proj-456",
				"service.name":                 "my-service",
			},
		},
	}

	span := ParseSpanEntry(hit)

	if span.SpanID != "span-1" {
		t.Errorf("expected spanID 'span-1', got %q", span.SpanID)
	}
	if span.Name != "GET /api/users" {
		t.Errorf("expected name 'GET /api/users', got %q", span.Name)
	}
	if span.ParentSpanID != "span-root" {
		t.Errorf("expected parentSpanID 'span-root', got %q", span.ParentSpanID)
	}
	if span.SpanKind != "SERVER" {
		t.Errorf("expected spanKind 'SERVER', got %q", span.SpanKind)
	}
	if span.Status != SpanStatusOK {
		t.Errorf("expected status 'ok', got %q", span.Status)
	}
	if span.OpenChoreoComponentUID != "comp-123" {
		t.Errorf("expected componentUID 'comp-123', got %q", span.OpenChoreoComponentUID)
	}
	if span.OpenChoreoProjectUID != "proj-456" {
		t.Errorf("expected projectUID 'proj-456', got %q", span.OpenChoreoProjectUID)
	}
	if span.Attributes == nil || span.Attributes["http.method"] != "GET" {
		t.Errorf("expected attribute http.method=GET, got %v", span.Attributes)
	}
	if span.ResourceAttributes == nil || span.ResourceAttributes["service.name"] != "my-service" {
		t.Errorf("expected resource attribute service.name=my-service, got %v", span.ResourceAttributes)
	}

	expectedDuration := time.Second.Nanoseconds()
	if span.DurationNanoseconds != expectedDuration {
		t.Errorf("expected duration %d ns, got %d ns", expectedDuration, span.DurationNanoseconds)
	}
}

func TestParseSpanEntry_MissingOptionalFields(t *testing.T) {
	hit := Hit{
		Source: map[string]interface{}{
			"spanId":    "span-1",
			"startTime": "2025-06-01T12:00:00Z",
			"endTime":   "2025-06-01T12:00:01Z",
		},
	}

	span := ParseSpanEntry(hit)

	if span.SpanID != "span-1" {
		t.Errorf("expected spanID 'span-1', got %q", span.SpanID)
	}
	if span.Name != "" {
		t.Errorf("expected empty name, got %q", span.Name)
	}
	if span.Status != SpanStatusUnset {
		t.Errorf("expected status 'unset', got %q", span.Status)
	}
	if span.Attributes != nil {
		t.Errorf("expected nil attributes, got %v", span.Attributes)
	}
}

func TestDetermineSpanStatus(t *testing.T) {
	tests := []struct {
		name     string
		spanHit  map[string]interface{}
		expected string
	}{
		{
			name:     "nil input",
			spanHit:  nil,
			expected: SpanStatusUnset,
		},
		{
			name:     "no status field",
			spanHit:  map[string]interface{}{"spanId": "span-1"},
			expected: SpanStatusUnset,
		},
		{
			name: "status ok",
			spanHit: map[string]interface{}{
				"status": map[string]interface{}{"code": "ok"},
			},
			expected: SpanStatusOK,
		},
		{
			name: "status OK uppercase",
			spanHit: map[string]interface{}{
				"status": map[string]interface{}{"code": "OK"},
			},
			expected: SpanStatusOK,
		},
		{
			name: "status error",
			spanHit: map[string]interface{}{
				"status": map[string]interface{}{"code": "error"},
			},
			expected: SpanStatusError,
		},
		{
			name: "status Error mixed case",
			spanHit: map[string]interface{}{
				"status": map[string]interface{}{"code": "Error"},
			},
			expected: SpanStatusError,
		},
		{
			name: "status unknown value",
			spanHit: map[string]interface{}{
				"status": map[string]interface{}{"code": "unknown"},
			},
			expected: SpanStatusUnset,
		},
		{
			name: "status code not string",
			spanHit: map[string]interface{}{
				"status": map[string]interface{}{"code": 1},
			},
			expected: SpanStatusUnset,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetermineSpanStatus(tt.spanHit)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestParseSpanEntry_InvalidTimestamps(t *testing.T) {
	hit := Hit{
		Source: map[string]interface{}{
			"spanId":    "00f067aa0ba902b7",
			"startTime": "not-a-valid-time",
			"endTime":   "also-invalid",
		},
	}

	span := ParseSpanEntry(hit)

	if span.SpanID != "00f067aa0ba902b7" {
		t.Errorf("expected spanID '00f067aa0ba902b7', got %q", span.SpanID)
	}
	if !span.StartTime.IsZero() {
		t.Error("expected zero startTime for invalid timestamp")
	}
	if !span.EndTime.IsZero() {
		t.Error("expected zero endTime for invalid timestamp")
	}
	if span.DurationNanoseconds != 0 {
		t.Errorf("expected zero duration for invalid timestamps, got %d", span.DurationNanoseconds)
	}
}

func TestParseSpanEntry_NonStringTimeValues(t *testing.T) {
	hit := Hit{
		Source: map[string]interface{}{
			"spanId":    "1a2b3c4d5e6f7a8b",
			"startTime": 12345,
			"endTime":   67890,
		},
	}

	span := ParseSpanEntry(hit)

	if !span.StartTime.IsZero() {
		t.Error("expected zero startTime for non-string time value")
	}
	if !span.EndTime.IsZero() {
		t.Error("expected zero endTime for non-string time value")
	}
}

func TestParseSpanEntry_PartialResource(t *testing.T) {
	hit := Hit{
		Source: map[string]interface{}{
			"spanId": "b9c7d8e9f0a1b2c3",
			"resource": map[string]interface{}{
				"service.name":                 "order-service",
				"openchoreo.dev/component-uid": "d4e5f6a7-b8c9-4d0e-a1b2-c3d4e5f6a7b8",
				// project-uid missing
			},
			"startTime": "2025-06-01T12:00:00Z",
			"endTime":   "2025-06-01T12:00:01Z",
		},
	}

	span := ParseSpanEntry(hit)

	if span.OpenChoreoComponentUID != "d4e5f6a7-b8c9-4d0e-a1b2-c3d4e5f6a7b8" {
		t.Errorf("expected componentUID 'd4e5f6a7-b8c9-4d0e-a1b2-c3d4e5f6a7b8', got %q", span.OpenChoreoComponentUID)
	}
	if span.OpenChoreoProjectUID != "" {
		t.Errorf("expected empty projectUID, got %q", span.OpenChoreoProjectUID)
	}
	if span.ResourceAttributes == nil {
		t.Fatal("expected non-nil resource attributes")
	}
	if span.ResourceAttributes["service.name"] != "order-service" {
		t.Errorf("expected service.name 'order-service', got %v", span.ResourceAttributes["service.name"])
	}
}

func TestParseSpanEntry_NonStringFieldValues(t *testing.T) {
	hit := Hit{
		Source: map[string]interface{}{
			"spanId":       123,
			"name":         456,
			"parentSpanId": true,
			"kind":         nil,
			"startTime":    "2025-06-01T12:00:00Z",
			"endTime":      "2025-06-01T12:00:01Z",
		},
	}

	span := ParseSpanEntry(hit)

	// getString should return empty string for non-string values
	if span.SpanID != "" {
		t.Errorf("expected empty spanID for non-string value, got %q", span.SpanID)
	}
	if span.Name != "" {
		t.Errorf("expected empty name for non-string value, got %q", span.Name)
	}
	if span.ParentSpanID != "" {
		t.Errorf("expected empty parentSpanID for non-string value, got %q", span.ParentSpanID)
	}
	if span.SpanKind != "" {
		t.Errorf("expected empty spanKind for nil value, got %q", span.SpanKind)
	}
}

func TestParseSpanEntry_ErrorStatus(t *testing.T) {
	hit := Hit{
		Source: map[string]interface{}{
			"spanId":    "c3d4e5f6a7b8c9d0",
			"startTime": "2025-06-01T12:00:00Z",
			"endTime":   "2025-06-01T12:00:01Z",
			"status": map[string]interface{}{
				"code": "ERROR",
			},
		},
	}

	span := ParseSpanEntry(hit)

	if span.Status != SpanStatusError {
		t.Errorf("expected status 'error', got %q", span.Status)
	}
}

func TestGetTraceID(t *testing.T) {
	tests := []struct {
		name     string
		hit      Hit
		expected string
	}{
		{
			name:     "nil source",
			hit:      Hit{Source: nil},
			expected: "",
		},
		{
			name:     "no traceId",
			hit:      Hit{Source: map[string]interface{}{"spanId": "span-1"}},
			expected: "",
		},
		{
			name:     "valid traceId",
			hit:      Hit{Source: map[string]interface{}{"traceId": "trace-abc"}},
			expected: "trace-abc",
		},
		{
			name:     "traceId not string",
			hit:      Hit{Source: map[string]interface{}{"traceId": 123}},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetTraceID(tt.hit)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
