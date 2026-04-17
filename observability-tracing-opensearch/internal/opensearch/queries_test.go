// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"testing"
)

func TestBuildTracesQuery_Basic(t *testing.T) {
	params := TracesRequestParams{
		StartTime: "2025-06-01T00:00:00Z",
		EndTime:   "2025-06-02T00:00:00Z",
		Limit:     20,
		SortOrder: "desc",
	}

	query := BuildTracesQuery(params)

	// Verify query can be serialized
	data, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("failed to marshal query: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal query: %v", err)
	}

	// Check size
	if size, ok := parsed["size"].(float64); !ok || int(size) != 20 {
		t.Errorf("expected size 20, got %v", parsed["size"])
	}

	// Check query structure exists
	if _, ok := parsed["query"]; !ok {
		t.Error("expected query field")
	}

	// Check sort exists
	if _, ok := parsed["sort"]; !ok {
		t.Error("expected sort field")
	}
}

func TestBuildTracesQuery_WithTraceID(t *testing.T) {
	params := TracesRequestParams{
		StartTime: "2025-06-01T00:00:00Z",
		EndTime:   "2025-06-02T00:00:00Z",
		TraceID:   "abc123",
		Limit:     10,
		SortOrder: "desc",
	}

	query := BuildTracesQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	// Verify the query contains a wildcard filter for traceId
	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	found := false
	for _, f := range filters {
		filter := f.(map[string]interface{})
		if _, ok := filter["wildcard"]; ok {
			found = true
		}
	}
	if !found {
		t.Error("expected wildcard filter for traceId")
	}
}

func TestBuildTracesQuery_WithComponentUIDs(t *testing.T) {
	params := TracesRequestParams{
		StartTime:    "2025-06-01T00:00:00Z",
		EndTime:      "2025-06-02T00:00:00Z",
		ComponentUIDs: []string{"comp-1", "comp-2"},
		Limit:        10,
		SortOrder:    "desc",
	}

	query := BuildTracesQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	// Should have time range filters + component UIDs bool/should filter
	found := false
	for _, f := range filters {
		filter := f.(map[string]interface{})
		if boolFilter, ok := filter["bool"]; ok {
			boolMap := boolFilter.(map[string]interface{})
			if should, ok := boolMap["should"]; ok {
				shouldArr := should.([]interface{})
				if len(shouldArr) == 2 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected bool/should filter with 2 component UIDs")
	}
}

func TestBuildTracesQuery_WithAllFilters(t *testing.T) {
	params := TracesRequestParams{
		StartTime:      "2025-06-01T00:00:00Z",
		EndTime:        "2025-06-02T00:00:00Z",
		ComponentUIDs:  []string{"comp-1"},
		EnvironmentUID: "env-1",
		Namespace:      "default",
		ProjectUID:     "proj-1",
		Limit:          50,
		SortOrder:      "desc",
	}

	query := BuildTracesQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	// Should have: 2 range filters + component bool/should + environment term + project term + namespace term = 6
	if len(filters) != 6 {
		t.Errorf("expected 6 filter conditions, got %d", len(filters))
	}
}

func TestBuildTracesAggregationQuery(t *testing.T) {
	params := TracesRequestParams{
		StartTime: "2025-06-01T00:00:00Z",
		EndTime:   "2025-06-02T00:00:00Z",
		Limit:     10,
		SortOrder: "desc",
	}

	query := BuildTracesAggregationQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	// Size should be 0 for aggregation queries
	if size, ok := parsed["size"].(float64); !ok || int(size) != 0 {
		t.Errorf("expected size 0 for aggregation query, got %v", parsed["size"])
	}

	// Should have aggregations
	aggs, ok := parsed["aggs"].(map[string]interface{})
	if !ok {
		t.Fatal("expected aggs field")
	}

	// Should have trace_count and traces aggregations
	if _, ok := aggs["trace_count"]; !ok {
		t.Error("expected trace_count aggregation")
	}
	if _, ok := aggs["traces"]; !ok {
		t.Error("expected traces aggregation")
	}

	// Verify traces aggregation has sub-aggregations
	tracesAgg := aggs["traces"].(map[string]interface{})
	subAggs, ok := tracesAgg["aggs"].(map[string]interface{})
	if !ok {
		t.Fatal("expected sub-aggregations in traces")
	}

	expectedSubAggs := []string{"earliest_span", "root_span", "latest_span", "error_span_count", "min_start_time"}
	for _, name := range expectedSubAggs {
		if _, ok := subAggs[name]; !ok {
			t.Errorf("expected sub-aggregation %q", name)
		}
	}
}

func TestBuildTracesAggregationQuery_DefaultSortOrder(t *testing.T) {
	params := TracesRequestParams{
		StartTime: "2025-06-01T00:00:00Z",
		EndTime:   "2025-06-02T00:00:00Z",
		Limit:     10,
		SortOrder: "", // empty should default to desc
	}

	query := BuildTracesAggregationQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	aggs := parsed["aggs"].(map[string]interface{})
	tracesAgg := aggs["traces"].(map[string]interface{})
	terms := tracesAgg["terms"].(map[string]interface{})
	order := terms["order"].(map[string]interface{})

	if order["min_start_time"] != "desc" {
		t.Errorf("expected default sort order 'desc', got %v", order["min_start_time"])
	}
}

func TestBuildTracesAggregationQuery_WithAllFilters(t *testing.T) {
	params := TracesRequestParams{
		StartTime:      "2025-06-01T00:00:00Z",
		EndTime:        "2025-06-02T00:00:00Z",
		ComponentUIDs:  []string{"d4e5f6a7-b8c9-4d0e-a1b2-c3d4e5f6a7b8"},
		EnvironmentUID: "7a8b9c0d-1e2f-3a4b-5c6d-7e8f9a0b1c2d",
		Namespace:      "choreo-prod-abcd1234-ns",
		ProjectUID:     "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Limit:          50,
		SortOrder:      "asc",
	}

	query := BuildTracesAggregationQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	// Should have: 2 range filters + component bool/should + environment term + project term + namespace term = 6
	if len(filters) != 6 {
		t.Errorf("expected 6 filter conditions, got %d", len(filters))
	}

	// Verify sort order is applied
	aggs := parsed["aggs"].(map[string]interface{})
	tracesAgg := aggs["traces"].(map[string]interface{})
	terms := tracesAgg["terms"].(map[string]interface{})
	order := terms["order"].(map[string]interface{})

	if order["min_start_time"] != "asc" {
		t.Errorf("expected sort order 'asc', got %v", order["min_start_time"])
	}

	// Verify limit is set on the terms aggregation
	if size, ok := terms["size"].(float64); !ok || int(size) != 50 {
		t.Errorf("expected terms size 50, got %v", terms["size"])
	}
}

func TestBuildTracesAggregationQuery_WithComponentUIDs(t *testing.T) {
	params := TracesRequestParams{
		StartTime:     "2025-06-01T00:00:00Z",
		EndTime:       "2025-06-02T00:00:00Z",
		ComponentUIDs: []string{"d4e5f6a7-b8c9-4d0e-a1b2-c3d4e5f6a7b8", "a1b2c3d4-e5f6-7890-abcd-ef1234567890", "9f8e7d6c-5b4a-3210-fedc-ba9876543210"},
		Limit:         10,
		SortOrder:     "desc",
	}

	query := BuildTracesAggregationQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	found := false
	for _, f := range filters {
		filter := f.(map[string]interface{})
		if boolFilter, ok := filter["bool"]; ok {
			boolMap := boolFilter.(map[string]interface{})
			if should, ok := boolMap["should"]; ok {
				shouldArr := should.([]interface{})
				if len(shouldArr) == 3 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected bool/should filter with 3 component UIDs")
	}
}

func TestBuildTracesQuery_WithNamespaceOnly(t *testing.T) {
	params := TracesRequestParams{
		StartTime: "2025-06-01T00:00:00Z",
		EndTime:   "2025-06-02T00:00:00Z",
		Namespace: "choreo-prod-abcd1234-ns",
		Limit:     20,
		SortOrder: "desc",
	}

	query := BuildTracesQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	// Should have: 2 range filters + namespace term = 3
	if len(filters) != 3 {
		t.Errorf("expected 3 filter conditions, got %d", len(filters))
	}

	// Find the namespace term filter
	foundNamespace := false
	for _, f := range filters {
		filter := f.(map[string]interface{})
		if term, ok := filter["term"].(map[string]interface{}); ok {
			if term["resource.openchoreo.dev/namespace"] == "choreo-prod-abcd1234-ns" {
				foundNamespace = true
			}
		}
	}
	if !foundNamespace {
		t.Error("expected namespace term filter")
	}
}

func TestBuildTracesQuery_WithEnvironmentUID(t *testing.T) {
	params := TracesRequestParams{
		StartTime:      "2025-06-01T00:00:00Z",
		EndTime:        "2025-06-02T00:00:00Z",
		EnvironmentUID: "7a8b9c0d-1e2f-3a4b-5c6d-7e8f9a0b1c2d",
		Limit:          20,
		SortOrder:      "desc",
	}

	query := BuildTracesQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	foundEnv := false
	for _, f := range filters {
		filter := f.(map[string]interface{})
		if term, ok := filter["term"].(map[string]interface{}); ok {
			if term["resource.openchoreo.dev/environment-uid"] == "7a8b9c0d-1e2f-3a4b-5c6d-7e8f9a0b1c2d" {
				foundEnv = true
			}
		}
	}
	if !foundEnv {
		t.Error("expected environment-uid term filter")
	}
}

func TestBuildTracesQuery_WithProjectUID(t *testing.T) {
	params := TracesRequestParams{
		StartTime:  "2025-06-01T00:00:00Z",
		EndTime:    "2025-06-02T00:00:00Z",
		ProjectUID: "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Limit:      20,
		SortOrder:  "desc",
	}

	query := BuildTracesQuery(params)
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	foundProj := false
	for _, f := range filters {
		filter := f.(map[string]interface{})
		if term, ok := filter["term"].(map[string]interface{}); ok {
			if term["resource.openchoreo.dev/project-uid"] == "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
				foundProj = true
			}
		}
	}
	if !foundProj {
		t.Error("expected project-uid term filter")
	}
}

func TestBuildSpanDetailsQuery(t *testing.T) {
	query := BuildSpanDetailsQuery("trace-abc", "span-123")
	data, _ := json.Marshal(query)

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	// Size should be 1
	if size, ok := parsed["size"].(float64); !ok || int(size) != 1 {
		t.Errorf("expected size 1, got %v", parsed["size"])
	}

	// Should have 2 term filters
	queryObj := parsed["query"].(map[string]interface{})
	boolObj := queryObj["bool"].(map[string]interface{})
	filters := boolObj["filter"].([]interface{})

	if len(filters) != 2 {
		t.Errorf("expected 2 filter conditions, got %d", len(filters))
	}

	// Verify traceId and spanId terms
	foundTraceId := false
	foundSpanId := false
	for _, f := range filters {
		filter := f.(map[string]interface{})
		if term, ok := filter["term"].(map[string]interface{}); ok {
			if term["traceId"] == "trace-abc" {
				foundTraceId = true
			}
			if term["spanId"] == "span-123" {
				foundSpanId = true
			}
		}
	}
	if !foundTraceId {
		t.Error("expected traceId term filter")
	}
	if !foundSpanId {
		t.Error("expected spanId term filter")
	}
}

func TestSanitizeWildcardValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal-value", "normal-value"},
		{"value*", `value\*`},
		{"value?", `value\?`},
		{`value"quoted"`, `value\"quoted\"`},
		{`back\slash`, `back\\slash`},
		{`all*?"\\`, `all\*\?\"\\\\`},
	}

	for _, tt := range tests {
		result := sanitizeWildcardValue(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeWildcardValue(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
