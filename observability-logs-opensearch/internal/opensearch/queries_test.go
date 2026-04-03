// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildComponentLogsQueryV1(t *testing.T) {
	qb := NewQueryBuilder("logs-")

	t.Run("full params", func(t *testing.T) {
		params := ComponentLogsQueryParamsV1{
			StartTime:     "2025-06-15T00:00:00Z",
			EndTime:       "2025-06-15T23:59:59Z",
			NamespaceName: "test-ns",
			ProjectID:     "550e8400-e29b-41d4-a716-446655440003",
			ComponentID:   "550e8400-e29b-41d4-a716-446655440001",
			EnvironmentID: "550e8400-e29b-41d4-a716-446655440002",
			SearchPhrase:  "error",
			LogLevels:     []string{"ERROR", "WARN"},
			Limit:         50,
			SortOrder:     "asc",
		}

		query, err := qb.BuildComponentLogsQueryV1(params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if query["size"] != 50 {
			t.Errorf("expected size=50, got %v", query["size"])
		}

		// Verify sort order
		sortArr := query["sort"].([]map[string]interface{})
		tsSort := sortArr[0]["@timestamp"].(map[string]interface{})
		if tsSort["order"] != "asc" {
			t.Errorf("expected sort order 'asc', got %v", tsSort["order"])
		}

		// Verify query structure
		boolQuery := query["query"].(map[string]interface{})["bool"].(map[string]interface{})
		mustConditions := boolQuery["must"].([]map[string]interface{})
		if len(mustConditions) < 4 {
			t.Errorf("expected at least 4 must conditions, got %d", len(mustConditions))
		}
	})

	t.Run("minimal params", func(t *testing.T) {
		params := ComponentLogsQueryParamsV1{
			StartTime:     "2025-06-15T00:00:00Z",
			EndTime:       "2025-06-15T23:59:59Z",
			NamespaceName: "test-ns",
		}

		query, err := qb.BuildComponentLogsQueryV1(params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Default limit
		if query["size"] != 100 {
			t.Errorf("expected default size=100, got %v", query["size"])
		}

		// Default sort order
		sortArr := query["sort"].([]map[string]interface{})
		tsSort := sortArr[0]["@timestamp"].(map[string]interface{})
		if tsSort["order"] != "desc" {
			t.Errorf("expected default sort order 'desc', got %v", tsSort["order"])
		}
	})

	t.Run("missing required fields", func(t *testing.T) {
		params := ComponentLogsQueryParamsV1{
			StartTime: "2025-06-15T00:00:00Z",
		}

		_, err := qb.BuildComponentLogsQueryV1(params)
		if err == nil {
			t.Error("expected error for missing required fields")
		}
	})
}

func TestBuildWorkflowRunLogsQuery(t *testing.T) {
	qb := NewQueryBuilder("logs-")

	t.Run("with step name", func(t *testing.T) {
		params := WorkflowRunQueryParams{
			QueryParams: QueryParams{
				StartTime:     "2025-06-15T00:00:00Z",
				EndTime:       "2025-06-15T23:59:59Z",
				NamespaceName: "test-ns",
				Limit:         50,
				SortOrder:     "desc",
			},
			WorkflowRunID: "run-123",
			StepName:      "build",
		}

		query := qb.BuildWorkflowRunLogsQuery(params)

		boolQuery := query["query"].(map[string]interface{})["bool"].(map[string]interface{})
		mustConditions := boolQuery["must"].([]map[string]interface{})

		// Should have: pod name wildcard, step name filter, time range, namespace
		if len(mustConditions) < 4 {
			t.Errorf("expected at least 4 must conditions with step name, got %d", len(mustConditions))
		}

		// Verify must_not conditions exclude init and wait containers
		mustNotConditions := boolQuery["must_not"].([]map[string]interface{})
		if len(mustNotConditions) != 2 {
			t.Errorf("expected 2 must_not conditions, got %d", len(mustNotConditions))
		}
	})

	t.Run("without step name", func(t *testing.T) {
		params := WorkflowRunQueryParams{
			QueryParams: QueryParams{
				StartTime: "2025-06-15T00:00:00Z",
				EndTime:   "2025-06-15T23:59:59Z",
				Limit:     100,
				SortOrder: "desc",
			},
			WorkflowRunID: "run-456",
		}

		query := qb.BuildWorkflowRunLogsQuery(params)

		boolQuery := query["query"].(map[string]interface{})["bool"].(map[string]interface{})
		mustConditions := boolQuery["must"].([]map[string]interface{})

		// Without step name and without namespace: pod name wildcard + time range only
		if len(mustConditions) < 2 {
			t.Errorf("expected at least 2 must conditions without step name, got %d", len(mustConditions))
		}
	})

	t.Run("with namespace", func(t *testing.T) {
		params := WorkflowRunQueryParams{
			QueryParams: QueryParams{
				StartTime:     "2025-06-15T00:00:00Z",
				EndTime:       "2025-06-15T23:59:59Z",
				NamespaceName: "my-ns",
				Limit:         100,
				SortOrder:     "desc",
			},
			WorkflowRunID: "run-789",
		}

		query := qb.BuildWorkflowRunLogsQuery(params)

		// Marshal and check for the workflows- prefix
		queryBytes, _ := json.Marshal(query)
		queryStr := string(queryBytes)
		if !strings.Contains(queryStr, "workflows-my-ns") {
			t.Error("expected namespace to be prefixed with 'workflows-'")
		}
	})
}

func TestGenerateIndices(t *testing.T) {
	qb := NewQueryBuilder("logs-")

	t.Run("single day", func(t *testing.T) {
		indices, err := qb.GenerateIndices("2025-06-15T00:00:00Z", "2025-06-15T23:59:59Z")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(indices) != 1 {
			t.Fatalf("expected 1 index, got %d: %v", len(indices), indices)
		}
		if indices[0] != "logs-2025-06-15" {
			t.Errorf("expected 'logs-2025-06-15', got %q", indices[0])
		}
	})

	t.Run("multi-day span", func(t *testing.T) {
		indices, err := qb.GenerateIndices("2025-06-14T00:00:00Z", "2025-06-16T23:59:59Z")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(indices) != 3 {
			t.Fatalf("expected 3 indices, got %d: %v", len(indices), indices)
		}
		expected := []string{"logs-2025-06-14", "logs-2025-06-15", "logs-2025-06-16"}
		for i, exp := range expected {
			if indices[i] != exp {
				t.Errorf("index[%d] = %q, want %q", i, indices[i], exp)
			}
		}
	})

	t.Run("empty times", func(t *testing.T) {
		indices, err := qb.GenerateIndices("", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(indices) != 1 || indices[0] != "logs-*" {
			t.Errorf("expected ['logs-*'], got %v", indices)
		}
	})

	t.Run("invalid time format", func(t *testing.T) {
		_, err := qb.GenerateIndices("not-a-time", "also-not-a-time")
		if err == nil {
			t.Error("expected error for invalid time format")
		}
	})
}

func TestBuildLogAlertingRuleQuery(t *testing.T) {
	qb := NewQueryBuilder("logs-")

	params := AlertingRuleRequest{
		Metadata: AlertingRuleMetadata{
			Name:           "test-rule",
			ComponentUID:   "550e8400-e29b-41d4-a716-446655440001",
			ProjectUID:     "550e8400-e29b-41d4-a716-446655440003",
			EnvironmentUID: "550e8400-e29b-41d4-a716-446655440002",
		},
		Source: AlertingRuleSource{
			Query: "error",
		},
		Condition: AlertingRuleCondition{
			Window: "1h",
		},
	}

	query, err := qb.BuildLogAlertingRuleQuery(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if query["size"] != 0 {
		t.Errorf("expected size=0, got %v", query["size"])
	}

	// Verify the query contains filter conditions
	boolQuery := query["query"].(map[string]interface{})["bool"].(map[string]interface{})
	filters := boolQuery["filter"].([]map[string]interface{})
	if len(filters) != 5 {
		t.Errorf("expected 5 filter conditions, got %d", len(filters))
	}
}

func TestBuildLogAlertingRuleMonitorBody(t *testing.T) {
	qb := NewQueryBuilder("logs-")

	t.Run("valid params", func(t *testing.T) {
		params := AlertingRuleRequest{
			Metadata: AlertingRuleMetadata{
				Name:           "test-rule",
				Namespace:      "test-ns",
				ComponentUID:   "550e8400-e29b-41d4-a716-446655440001",
				ProjectUID:     "550e8400-e29b-41d4-a716-446655440003",
				EnvironmentUID: "550e8400-e29b-41d4-a716-446655440002",
			},
			Source: AlertingRuleSource{
				Query: "error",
			},
			Condition: AlertingRuleCondition{
				Enabled:   true,
				Window:    "1h",
				Interval:  "5m",
				Operator:  "gt",
				Threshold: 10,
			},
		}

		body, err := qb.BuildLogAlertingRuleMonitorBody(params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if body["type"] != "monitor" {
			t.Errorf("expected type='monitor', got %v", body["type"])
		}
		if body["name"] != "test-rule" {
			t.Errorf("expected name='test-rule', got %v", body["name"])
		}
		if body["enabled"] != true {
			t.Errorf("expected enabled=true, got %v", body["enabled"])
		}
	})

	t.Run("invalid interval", func(t *testing.T) {
		params := AlertingRuleRequest{
			Metadata: AlertingRuleMetadata{Name: "test"},
			Source:   AlertingRuleSource{Query: "error"},
			Condition: AlertingRuleCondition{
				Window:   "1h",
				Interval: "invalid",
			},
		}

		_, err := qb.BuildLogAlertingRuleMonitorBody(params)
		if err == nil {
			t.Error("expected error for invalid interval")
		}
	})

	t.Run("invalid window", func(t *testing.T) {
		params := AlertingRuleRequest{
			Metadata: AlertingRuleMetadata{Name: "test"},
			Source:   AlertingRuleSource{Query: "error"},
			Condition: AlertingRuleCondition{
				Window:   "invalid",
				Interval: "5m",
			},
		}

		_, err := qb.BuildLogAlertingRuleMonitorBody(params)
		if err == nil {
			t.Error("expected error for invalid window")
		}
	})
}

func TestGetOperatorSymbol(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"gt", ">", false},
		{"gte", ">=", false},
		{"lt", "<", false},
		{"lte", "<=", false},
		{"unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := GetOperatorSymbol(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("GetOperatorSymbol(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("GetOperatorSymbol(%q) unexpected error: %v", tt.input, err)
			}
			if result != tt.expected {
				t.Errorf("GetOperatorSymbol(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestReverseMapOperator(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{">", "gt"},
		{">=", "gte"},
		{"<", "lt"},
		{"<=", "lte"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ReverseMapOperator(tt.input)
			if result != tt.expected {
				t.Errorf("ReverseMapOperator(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizeWildcardValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"backslash", `hello\world`, `hello\\world`},
		{"double quote", `hello"world`, `hello\"world`},
		{"asterisk", `hello*world`, `hello\*world`},
		{"question mark", `hello?world`, `hello\?world`},
		{"multiple special chars", `a*b?c\d"e`, `a\*b\?c\\d\"e`},
		{"no special chars", "hello world", "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeWildcardValue(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeWildcardValue(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFormatDurationForOpenSearch(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{"hours", "2h", "2h", false},
		{"minutes", "30m", "30m", false},
		{"one hour", "1h", "1h", false},
		{"seconds not supported", "30s", "", true},
		{"zero duration", "0m", "", true},
		{"negative duration", "-5m", "", true},
		{"negative hours", "-2h", "", true},
		{"invalid", "invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := formatDurationForOpenSearch(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("formatDurationForOpenSearch(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if result != tt.expected {
				t.Errorf("formatDurationForOpenSearch(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
