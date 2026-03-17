// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", `"simple"`},
		{`has"quote`, `"has""quote"`},
		{"", `""`},
		{`a"b"c`, `"a""b""c"`},
	}
	for _, tt := range tests {
		got := quoteIdentifier(tt.input)
		if got != tt.expected {
			t.Errorf("quoteIdentifier(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEscapeSQLString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"it's", "it''s"},
		{`back\slash`, `back\\slash`},
		{`it's a back\slash`, `it''s a back\\slash`},
		{"", ""},
	}
	for _, tt := range tests {
		got := escapeSQLString(tt.input)
		if got != tt.expected {
			t.Errorf("escapeSQLString(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMapOperator(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"gt", ">", false},
		{"gte", ">=", false},
		{"lt", "<", false},
		{"lte", "<=", false},
		{"eq", "=", false},
		{"neq", "!=", false},
		{"invalid", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := mapOperator(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("mapOperator(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.expected {
			t.Errorf("mapOperator(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestReverseMapOperator(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"=", "eq"},
		{">", "gt"},
		{">=", "gte"},
		{"<", "lt"},
		{"<=", "lte"},
		{"!=", "neq"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		got := ReverseMapOperator(tt.input)
		if got != tt.expected {
			t.Errorf("ReverseMapOperator(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMapOperator_RoundTrip(t *testing.T) {
	apiOps := []string{"gt", "gte", "lt", "lte", "eq", "neq"}
	for _, op := range apiOps {
		sqlOp, err := mapOperator(op)
		if err != nil {
			t.Fatalf("mapOperator(%q) unexpected error: %v", op, err)
		}
		reversed := ReverseMapOperator(sqlOp)
		if reversed != op {
			t.Errorf("round trip failed: %q -> %q -> %q", op, sqlOp, reversed)
		}
	}
}

func TestExtractSearchPattern(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		expected string
	}{
		{"basic pattern", "SELECT count(*) as match_count FROM \"default\" WHERE str_match(log, 'error')", "error"},
		{"pattern with escaped quote", "SELECT count(*) as match_count FROM \"default\" WHERE str_match(log, 'it''s an error')", "it's an error"},
		{"pattern with escaped backslash", "SELECT count(*) as match_count FROM \"default\" WHERE str_match(log, 'path\\\\file')", "path\\file"},
		{"no str_match", "SELECT * FROM default", ""},
		{"empty pattern", "SELECT count(*) as match_count FROM \"default\" WHERE str_match(log, '')", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSearchPattern(tt.sql)
			if got != tt.expected {
				t.Errorf("ExtractSearchPattern() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestToDurationString(t *testing.T) {
	tests := []struct {
		value         int
		frequencyType string
		expected      string
	}{
		{5, "minutes", "5m"},
		{2, "hours", "2h"},
		{10, "unknown", "10m"},
		{0, "minutes", "0m"},
	}
	for _, tt := range tests {
		got := ToDurationString(tt.value, tt.frequencyType)
		if got != tt.expected {
			t.Errorf("ToDurationString(%d, %q) = %q, want %q", tt.value, tt.frequencyType, got, tt.expected)
		}
	}
}

func TestParseDurationMinutes(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		wantErr  bool
	}{
		{"5m", 5, false},
		{"2h", 120, false},
		{"10m", 10, false},
		{"1h", 60, false},
		{"", 0, true},
		{"m", 0, true},
		{"5s", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDurationMinutes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDurationMinutes(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("parseDurationMinutes(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestGenerateComponentLogsQuery(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	t.Run("basic query with namespace", func(t *testing.T) {
		params := ComponentLogsParams{
			Namespace: "test-ns",
			StartTime: startTime,
			EndTime:   endTime,
		}

		result, err := generateComponentLogsQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		if err := json.Unmarshal(result, &query); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}

		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)
		if !strings.Contains(sql, `kubernetes_labels_openchoreo_dev_namespace = 'test-ns'`) {
			t.Errorf("expected namespace filter in SQL: %s", sql)
		}
		if !strings.Contains(sql, `ORDER BY _timestamp DESC`) {
			t.Errorf("expected default DESC order in SQL: %s", sql)
		}
		if q["size"].(float64) != 100 {
			t.Errorf("expected default limit 100, got %v", q["size"])
		}
	})

	t.Run("missing namespace returns error", func(t *testing.T) {
		params := ComponentLogsParams{
			StartTime: startTime,
			EndTime:   endTime,
		}
		_, err := generateComponentLogsQuery(params, "mystream", testLogger())
		if err == nil {
			t.Fatal("expected error for missing namespace")
		}
	})

	t.Run("with all filters", func(t *testing.T) {
		params := ComponentLogsParams{
			Namespace:    "test-ns",
			ProjectID:    "proj-1",
			EnvironmentID: "env-1",
			ComponentIDs: []string{"comp-1", "comp-2"},
			SearchPhrase: "error",
			LogLevels:    []string{"ERROR", "WARN"},
			Limit:        50,
			SortOrder:    "ASC",
			StartTime:    startTime,
			EndTime:      endTime,
		}

		result, err := generateComponentLogsQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)

		checks := []string{
			"kubernetes_labels_openchoreo_dev_namespace = 'test-ns'",
			"kubernetes_labels_openchoreo_dev_project_uid = 'proj-1'",
			"kubernetes_labels_openchoreo_dev_environment_uid = 'env-1'",
			"kubernetes_labels_openchoreo_dev_component_uid = 'comp-1'",
			"kubernetes_labels_openchoreo_dev_component_uid = 'comp-2'",
			"log LIKE '%error%'",
			"logLevel = 'ERROR'",
			"logLevel = 'WARN'",
			"ORDER BY _timestamp ASC",
		}
		for _, check := range checks {
			if !strings.Contains(sql, check) {
				t.Errorf("expected SQL to contain %q, got: %s", check, sql)
			}
		}
		if q["size"].(float64) != 50 {
			t.Errorf("expected limit 50, got %v", q["size"])
		}
	})

	t.Run("SQL injection prevention", func(t *testing.T) {
		params := ComponentLogsParams{
			Namespace:    "test'; DROP TABLE users;--",
			SearchPhrase: "'; DROP TABLE logs;--",
			StartTime:    startTime,
			EndTime:      endTime,
		}

		result, err := generateComponentLogsQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)

		// Single quotes in values should be escaped (doubled)
		if strings.Contains(sql, "test'; DROP") {
			t.Errorf("SQL contains unescaped single quotes: %s", sql)
		}
		// The escaped version should be present
		if !strings.Contains(sql, "test''; DROP") {
			t.Errorf("SQL should contain escaped single quotes: %s", sql)
		}
	})
}

func TestGenerateWorkflowLogsQuery(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	t.Run("basic query", func(t *testing.T) {
		params := WorkflowLogsParams{
			Namespace:       "test-ns",
			WorkflowRunName: "run-1",
			StartTime:       startTime,
			EndTime:         endTime,
		}

		result, err := generateWorkflowLogsQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)

		if !strings.Contains(sql, "kubernetes_namespace_name = 'workflows-test-ns'") {
			t.Errorf("expected workflow namespace filter in SQL: %s", sql)
		}
		if !strings.Contains(sql, "kubernetes_labels_workflows_argoproj_io_workflow = 'run-1'") {
			t.Errorf("expected workflow run name filter in SQL: %s", sql)
		}
	})

	t.Run("with search and levels", func(t *testing.T) {
		params := WorkflowLogsParams{
			Namespace:       "test-ns",
			WorkflowRunName: "run-1",
			SearchPhrase:    "timeout",
			LogLevels:       []string{"ERROR"},
			SortOrder:       "asc",
			Limit:           25,
			StartTime:       startTime,
			EndTime:         endTime,
		}

		result, err := generateWorkflowLogsQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)

		if !strings.Contains(sql, "log LIKE '%timeout%'") {
			t.Errorf("expected search phrase in SQL: %s", sql)
		}
		if !strings.Contains(sql, "logLevel = 'ERROR'") {
			t.Errorf("expected log level filter in SQL: %s", sql)
		}
		if !strings.Contains(sql, "ORDER BY _timestamp ASC") {
			t.Errorf("expected ASC order in SQL: %s", sql)
		}
		if q["size"].(float64) != 25 {
			t.Errorf("expected limit 25, got %v", q["size"])
		}
	})
}

func TestGenerateAlertConfig(t *testing.T) {
	enabled := true
	name := "test-alert"

	t.Run("valid config", func(t *testing.T) {
		params := LogAlertParams{
			Name:           &name,
			Namespace:      "ns-1",
			ProjectUID:     "proj-uid",
			EnvironmentUID: "env-uid",
			ComponentUID:   "comp-uid",
			SearchPattern:  "error",
			Operator:       "gt",
			ThresholdValue: 5,
			Window:         "5m",
			Interval:       "1m",
			Enabled:        &enabled,
		}

		result, err := generateAlertConfig(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var config map[string]interface{}
		if err := json.Unmarshal(result, &config); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}

		if config["name"] != "test-alert" {
			t.Errorf("expected name 'test-alert', got %v", config["name"])
		}
		if config["stream_name"] != "mystream" {
			t.Errorf("expected stream_name 'mystream', got %v", config["stream_name"])
		}
		if config["enabled"] != true {
			t.Errorf("expected enabled true, got %v", config["enabled"])
		}

		qc := config["query_condition"].(map[string]interface{})
		sql := qc["sql"].(string)
		if !strings.Contains(sql, "str_match(log, 'error')") {
			t.Errorf("expected str_match in SQL: %s", sql)
		}

		tc := config["trigger_condition"].(map[string]interface{})
		if tc["operator"] != ">" {
			t.Errorf("expected operator '>', got %v", tc["operator"])
		}
		if tc["period"].(float64) != 5 {
			t.Errorf("expected period 5, got %v", tc["period"])
		}
		if tc["frequency"].(float64) != 1 {
			t.Errorf("expected frequency 1, got %v", tc["frequency"])
		}

		ca := config["context_attributes"].(map[string]interface{})
		if ca["namespace"] != "ns-1" {
			t.Errorf("expected namespace 'ns-1', got %v", ca["namespace"])
		}
	})

	t.Run("invalid operator", func(t *testing.T) {
		params := LogAlertParams{
			Name:     &name,
			Operator: "invalid",
			Window:   "5m",
			Interval: "1m",
			Enabled:  &enabled,
		}
		_, err := generateAlertConfig(params, "mystream", testLogger())
		if err == nil {
			t.Fatal("expected error for invalid operator")
		}
	})

	t.Run("invalid window", func(t *testing.T) {
		params := LogAlertParams{
			Name:     &name,
			Operator: "gt",
			Window:   "bad",
			Interval: "1m",
			Enabled:  &enabled,
		}
		_, err := generateAlertConfig(params, "mystream", testLogger())
		if err == nil {
			t.Fatal("expected error for invalid window")
		}
	})

	t.Run("invalid interval", func(t *testing.T) {
		params := LogAlertParams{
			Name:     &name,
			Operator: "gt",
			Window:   "5m",
			Interval: "bad",
			Enabled:  &enabled,
		}
		_, err := generateAlertConfig(params, "mystream", testLogger())
		if err == nil {
			t.Fatal("expected error for invalid interval")
		}
	})
}
