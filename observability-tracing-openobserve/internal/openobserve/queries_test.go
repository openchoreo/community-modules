// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateSQLIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "mystream", false},
		{"valid with dots", "org.stream", false},
		{"valid with hyphens", "my-stream", false},
		{"valid with underscores", "my_stream", false},
		{"valid alphanumeric", "stream123", false},
		{"empty string", "", true},
		{"contains space", "my stream", true},
		{"contains semicolon", "stream;DROP", true},
		{"contains single quote", "stream'bad", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateSQLIdentifier(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSQLIdentifier(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.input {
				t.Errorf("validateSQLIdentifier(%q) = %q, want %q", tt.input, got, tt.input)
			}
		})
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

func TestBuildFilterConditions(t *testing.T) {
	t.Run("all filters", func(t *testing.T) {
		params := TracesQueryParams{
			Scope: Scope{
				Namespace:     "test-ns",
				ProjectID:     "proj-1",
				EnvironmentID: "env-1",
				ComponentID:   "comp-1",
			},
		}

		conditions := buildFilterConditions(params)

		if len(conditions) != 4 {
			t.Fatalf("expected 4 conditions, got %d", len(conditions))
		}

		joined := strings.Join(conditions, " AND ")
		checks := []string{
			"service_openchoreo_dev_namespace = 'test-ns'",
			"service_openchoreo_dev_project_uid = 'proj-1'",
			"service_openchoreo_dev_environment_uid = 'env-1'",
			"service_openchoreo_dev_component_uid = 'comp-1'",
		}
		for _, check := range checks {
			if !strings.Contains(joined, check) {
				t.Errorf("expected condition %q in: %s", check, joined)
			}
		}
	})

	t.Run("namespace only", func(t *testing.T) {
		params := TracesQueryParams{
			Scope: Scope{
				Namespace: "test-ns",
			},
		}

		conditions := buildFilterConditions(params)

		if len(conditions) != 1 {
			t.Fatalf("expected 1 condition, got %d", len(conditions))
		}
		if !strings.Contains(conditions[0], "service_openchoreo_dev_namespace = 'test-ns'") {
			t.Errorf("unexpected condition: %s", conditions[0])
		}
	})

	t.Run("empty scope", func(t *testing.T) {
		params := TracesQueryParams{}

		conditions := buildFilterConditions(params)

		if len(conditions) != 0 {
			t.Errorf("expected 0 conditions, got %d", len(conditions))
		}
	})

	t.Run("SQL injection prevention", func(t *testing.T) {
		params := TracesQueryParams{
			Scope: Scope{
				Namespace: "test'; DROP TABLE spans;--",
			},
		}

		conditions := buildFilterConditions(params)

		if len(conditions) != 1 {
			t.Fatalf("expected 1 condition, got %d", len(conditions))
		}
		// Single quotes should be escaped (doubled)
		if strings.Contains(conditions[0], "test'; DROP") {
			t.Errorf("SQL contains unescaped single quotes: %s", conditions[0])
		}
		if !strings.Contains(conditions[0], "test''; DROP") {
			t.Errorf("SQL should contain escaped single quotes: %s", conditions[0])
		}
	})
}

func TestGenerateTracesListQuery(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	t.Run("basic query with namespace", func(t *testing.T) {
		params := TracesQueryParams{
			Scope: Scope{
				Namespace: "test-ns",
			},
			StartTime: startTime,
			EndTime:   endTime,
		}

		result, err := generateTracesListQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		if err := json.Unmarshal(result, &query); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}

		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)
		if !strings.Contains(sql, "service_openchoreo_dev_namespace = 'test-ns'") {
			t.Errorf("expected namespace filter in SQL: %s", sql)
		}
		if !strings.Contains(sql, "FROM mystream") {
			t.Errorf("expected stream name in SQL: %s", sql)
		}
		if !strings.Contains(sql, "ORDER BY start_time DESC") {
			t.Errorf("expected default DESC order in SQL: %s", sql)
		}
		if q["size"].(float64) != 100 {
			t.Errorf("expected default limit 100, got %v", q["size"])
		}
	})

	t.Run("with all filters and custom limit", func(t *testing.T) {
		params := TracesQueryParams{
			Scope: Scope{
				Namespace:     "test-ns",
				ProjectID:     "proj-1",
				EnvironmentID: "env-1",
				ComponentID:   "comp-1",
			},
			Limit:     50,
			SortOrder: "asc",
			StartTime: startTime,
			EndTime:   endTime,
		}

		result, err := generateTracesListQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)

		checks := []string{
			"service_openchoreo_dev_namespace = 'test-ns'",
			"service_openchoreo_dev_project_uid = 'proj-1'",
			"service_openchoreo_dev_environment_uid = 'env-1'",
			"service_openchoreo_dev_component_uid = 'comp-1'",
			"ORDER BY start_time ASC",
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

	t.Run("limit capped at MaxQueryLimit", func(t *testing.T) {
		params := TracesQueryParams{
			Scope: Scope{
				Namespace: "test-ns",
			},
			Limit:     5000,
			StartTime: startTime,
			EndTime:   endTime,
		}

		result, err := generateTracesListQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		if q["size"].(float64) != float64(MaxQueryLimit) {
			t.Errorf("expected limit capped at %d, got %v", MaxQueryLimit, q["size"])
		}
	})

	t.Run("invalid stream identifier", func(t *testing.T) {
		params := TracesQueryParams{
			Scope: Scope{
				Namespace: "test-ns",
			},
			StartTime: startTime,
			EndTime:   endTime,
		}

		_, err := generateTracesListQuery(params, "bad;stream", testLogger())
		if err == nil {
			t.Fatal("expected error for invalid stream identifier")
		}
	})

	t.Run("SQL injection prevention in namespace", func(t *testing.T) {
		params := TracesQueryParams{
			Scope: Scope{
				Namespace: "test'; DROP TABLE spans;--",
			},
			StartTime: startTime,
			EndTime:   endTime,
		}

		result, err := generateTracesListQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)

		if strings.Contains(sql, "test'; DROP") {
			t.Errorf("SQL contains unescaped single quotes: %s", sql)
		}
		if !strings.Contains(sql, "test''; DROP") {
			t.Errorf("SQL should contain escaped single quotes: %s", sql)
		}
	})
}

func TestGenerateSpansListQuery(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	t.Run("basic query", func(t *testing.T) {
		params := TracesQueryParams{
			TraceID:   "abc123",
			StartTime: startTime,
			EndTime:   endTime,
		}

		result, err := generateSpansListQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		if err := json.Unmarshal(result, &query); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}

		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)
		if !strings.Contains(sql, "trace_id = 'abc123'") {
			t.Errorf("expected trace_id filter in SQL: %s", sql)
		}
		if !strings.Contains(sql, "FROM mystream") {
			t.Errorf("expected stream name in SQL: %s", sql)
		}
		if !strings.Contains(sql, "ORDER BY start_time DESC") {
			t.Errorf("expected default DESC order in SQL: %s", sql)
		}
	})

	t.Run("with ASC sort order", func(t *testing.T) {
		params := TracesQueryParams{
			TraceID:   "abc123",
			SortOrder: "asc",
			StartTime: startTime,
			EndTime:   endTime,
		}

		result, err := generateSpansListQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)
		if !strings.Contains(sql, "ORDER BY start_time ASC") {
			t.Errorf("expected ASC order in SQL: %s", sql)
		}
	})

	t.Run("SQL injection in traceID", func(t *testing.T) {
		params := TracesQueryParams{
			TraceID:   "abc'; DROP TABLE spans;--",
			StartTime: startTime,
			EndTime:   endTime,
		}

		result, err := generateSpansListQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)
		if strings.Contains(sql, "abc'; DROP") {
			t.Errorf("SQL contains unescaped single quotes: %s", sql)
		}
	})

	t.Run("invalid stream identifier", func(t *testing.T) {
		params := TracesQueryParams{
			TraceID:   "abc123",
			StartTime: startTime,
			EndTime:   endTime,
		}

		_, err := generateSpansListQuery(params, "bad;stream", testLogger())
		if err == nil {
			t.Fatal("expected error for invalid stream identifier")
		}
	})
}

func TestGenerateSpanDetailQuery(t *testing.T) {
	t.Run("basic query", func(t *testing.T) {
		params := TracesQueryParams{
			TraceID: "trace-1",
			SpanID:  "span-1",
		}

		result, err := generateSpanDetailQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		if err := json.Unmarshal(result, &query); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}

		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)
		if !strings.Contains(sql, "trace_id = 'trace-1'") {
			t.Errorf("expected trace_id filter in SQL: %s", sql)
		}
		if !strings.Contains(sql, "span_id = 'span-1'") {
			t.Errorf("expected span_id filter in SQL: %s", sql)
		}
		if !strings.Contains(sql, "SELECT * FROM mystream") {
			t.Errorf("expected SELECT * FROM in SQL: %s", sql)
		}
		if q["size"].(float64) != 1 {
			t.Errorf("expected size 1, got %v", q["size"])
		}
	})

	t.Run("SQL injection in traceID and spanID", func(t *testing.T) {
		params := TracesQueryParams{
			TraceID: "trace'; DROP TABLE spans;--",
			SpanID:  "span'; DROP TABLE spans;--",
		}

		result, err := generateSpanDetailQuery(params, "mystream", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var query map[string]interface{}
		json.Unmarshal(result, &query)
		q := query["query"].(map[string]interface{})
		sql := q["sql"].(string)
		if strings.Contains(sql, "trace'; DROP") {
			t.Errorf("SQL contains unescaped traceID single quotes: %s", sql)
		}
		if strings.Contains(sql, "span'; DROP") {
			t.Errorf("SQL contains unescaped spanID single quotes: %s", sql)
		}
	})

	t.Run("invalid stream identifier", func(t *testing.T) {
		params := TracesQueryParams{
			TraceID: "trace-1",
			SpanID:  "span-1",
		}

		_, err := generateSpanDetailQuery(params, "bad;stream", testLogger())
		if err == nil {
			t.Fatal("expected error for invalid stream identifier")
		}
	})
}

// captureStdout runs fn and returns whatever it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = origStdout

	captured, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read captured output: %v", err)
	}
	return string(captured)
}

func debugLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestGenerateTracesListQuery_DebugLogging(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	params := TracesQueryParams{
		Scope: Scope{
			Namespace: "test-ns",
		},
		StartTime: startTime,
		EndTime:   endTime,
	}

	var result []byte
	output := captureStdout(t, func() {
		var err error
		result, err = generateTracesListQuery(params, "mystream", debugLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	var query map[string]interface{}
	if err := json.Unmarshal(result, &query); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	q := query["query"].(map[string]interface{})
	sql := q["sql"].(string)
	if !strings.Contains(sql, "FROM mystream") {
		t.Errorf("expected stream name in SQL: %s", sql)
	}

	if !strings.Contains(output, "FROM mystream") {
		t.Errorf("expected debug output to contain SQL with stream name, got: %s", output)
	}
}

func TestGenerateSpansListQuery_DebugLogging(t *testing.T) {
	startTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)

	params := TracesQueryParams{
		TraceID:   "abc123",
		StartTime: startTime,
		EndTime:   endTime,
	}

	var result []byte
	output := captureStdout(t, func() {
		var err error
		result, err = generateSpansListQuery(params, "mystream", debugLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	var query map[string]interface{}
	if err := json.Unmarshal(result, &query); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	q := query["query"].(map[string]interface{})
	sql := q["sql"].(string)
	if !strings.Contains(sql, "trace_id = 'abc123'") {
		t.Errorf("expected trace_id filter in SQL: %s", sql)
	}

	if !strings.Contains(output, "trace_id = 'abc123'") {
		t.Errorf("expected debug output to contain trace_id filter, got: %s", output)
	}
}

func TestGenerateSpanDetailQuery_DebugLogging(t *testing.T) {
	params := TracesQueryParams{
		TraceID: "trace-1",
		SpanID:  "span-1",
	}

	var result []byte
	output := captureStdout(t, func() {
		var err error
		result, err = generateSpanDetailQuery(params, "mystream", debugLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	var query map[string]interface{}
	if err := json.Unmarshal(result, &query); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	q := query["query"].(map[string]interface{})
	sql := q["sql"].(string)
	if !strings.Contains(sql, "trace_id = 'trace-1'") {
		t.Errorf("expected trace_id filter in SQL: %s", sql)
	}
	if !strings.Contains(sql, "span_id = 'span-1'") {
		t.Errorf("expected span_id filter in SQL: %s", sql)
	}

	if !strings.Contains(output, "trace_id = 'trace-1'") {
		t.Errorf("expected debug output to contain trace_id filter, got: %s", output)
	}
}
