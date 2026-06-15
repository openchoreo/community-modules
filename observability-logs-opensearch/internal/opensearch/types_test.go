// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"strings"
	"testing"
	"time"
)

func TestParseLogEntry(t *testing.T) {
	hit := Hit{
		ID: "test-id",
		Source: map[string]interface{}{
			"@timestamp": "2025-06-15T10:30:00Z",
			"log":        "ERROR something went wrong",
			"kubernetes": map[string]interface{}{
				"namespace_name": "test-ns",
				"pod_id":         "pod-123",
				"pod_name":       "my-pod",
				"container_name": "main",
				"labels": map[string]interface{}{
					ReplaceDots(ComponentID):     "550e8400-e29b-41d4-a716-446655440001",
					ReplaceDots(EnvironmentID):   "550e8400-e29b-41d4-a716-446655440002",
					ReplaceDots(ProjectID):       "550e8400-e29b-41d4-a716-446655440003",
					ReplaceDots(Version):         "v1",
					ReplaceDots(VersionID):       "ver-123",
					ReplaceDots(ComponentName):   "my-comp",
					ReplaceDots(EnvironmentName): "dev",
					ReplaceDots(ProjectName):     "my-proj",
					ReplaceDots(NamespaceName):   "my-ns",
				},
			},
		},
	}

	entry := ParseLogEntry(hit)

	expectedTime := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	if !entry.Timestamp.Equal(expectedTime) {
		t.Errorf("expected timestamp %v, got %v", expectedTime, entry.Timestamp)
	}
	if entry.Log != "ERROR something went wrong" {
		t.Errorf("expected log message, got %q", entry.Log)
	}
	if entry.LogLevel != "ERROR" {
		t.Errorf("expected log level ERROR, got %q", entry.LogLevel)
	}
	if entry.ComponentID != "550e8400-e29b-41d4-a716-446655440001" {
		t.Errorf("expected componentId '550e8400-e29b-41d4-a716-446655440001', got %q", entry.ComponentID)
	}
	if entry.EnvironmentID != "550e8400-e29b-41d4-a716-446655440002" {
		t.Errorf("expected environmentId '550e8400-e29b-41d4-a716-446655440002', got %q", entry.EnvironmentID)
	}
	if entry.ProjectID != "550e8400-e29b-41d4-a716-446655440003" {
		t.Errorf("expected projectId '550e8400-e29b-41d4-a716-446655440003', got %q", entry.ProjectID)
	}
	if entry.Version != "v1" {
		t.Errorf("expected version 'v1', got %q", entry.Version)
	}
	if entry.ComponentName != "my-comp" {
		t.Errorf("expected componentName 'my-comp', got %q", entry.ComponentName)
	}
	if entry.EnvironmentName != "dev" {
		t.Errorf("expected environmentName 'dev', got %q", entry.EnvironmentName)
	}
	if entry.ProjectName != "my-proj" {
		t.Errorf("expected projectName 'my-proj', got %q", entry.ProjectName)
	}
	if entry.NamespaceName != "my-ns" {
		t.Errorf("expected namespaceName 'my-ns', got %q", entry.NamespaceName)
	}
	if entry.Namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %q", entry.Namespace)
	}
	if entry.PodID != "pod-123" {
		t.Errorf("expected podId 'pod-123', got %q", entry.PodID)
	}
	if entry.PodName != "my-pod" {
		t.Errorf("expected podName 'my-pod', got %q", entry.PodName)
	}
	if entry.ContainerName != "main" {
		t.Errorf("expected containerName 'main', got %q", entry.ContainerName)
	}
	if len(entry.Labels) == 0 {
		t.Error("expected labels to be populated")
	}
}

func TestParseLogEntry_EmptySource(t *testing.T) {
	hit := Hit{
		ID:     "empty-hit",
		Source: map[string]interface{}{},
	}

	entry := ParseLogEntry(hit)

	if !entry.Timestamp.IsZero() {
		t.Errorf("expected zero timestamp, got %v", entry.Timestamp)
	}
	if entry.Log != "" {
		t.Errorf("expected empty log, got %q", entry.Log)
	}
	if entry.ComponentID != "" {
		t.Errorf("expected empty componentId, got %q", entry.ComponentID)
	}
}

func TestExtractLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Keyword fallback on unstructured lines.
		{"2025-01-01 ERROR something failed", "ERROR"},
		{"WARN disk space low", "WARN"},
		{"WARNING: deprecated function", "WARN"},
		{"[ERROR] connection refused", "ERROR"},
		{"INFO application started", "INFO"},
		{"DEBUG variable x = 5", "DEBUG"},
		{"FATAL out of memory", "FATAL"},
		{"SEVERE critical failure", "SEVERE"},
		{"error in lowercase", "ERROR"},
		{"just a regular log message", "INFO"},
		{"", "INFO"},
		{"   ", "INFO"},

		// Declared logfmt level, trusted over level words in the body.
		{`time="2026-05-05T07:56:21.304Z" level=info msg="sub-process exited" argo=true error="<nil>"`, "INFO"},
		{"level=info request failed: connection ERROR", "INFO"},
		{`level=debug msg="retrying after error"`, "DEBUG"},
		{`level=error msg="boom"`, "ERROR"},
		{`level="warn" msg=disk`, "WARN"},
		{"level = debug starting up", "DEBUG"},
		{"Level=Error mixed case", "ERROR"},
		{"lvl=warn cache miss", "WARN"},

		// Declared JSON level, field anywhere in the object.
		{`{"level":"warn","msg":"disk low"}`, "WARN"},
		{`{"ts":"2026-01-01","msg":"db down","level":"error"}`, "ERROR"},
		{`{"level":"info","ts":1620000000.1,"caller":"m.go:1","msg":"ok"}`, "INFO"},
		{`{"severity":"ERROR","message":"boom"}`, "ERROR"},
		{`{"severityText":"ERROR","body":"x"}`, "ERROR"},
		{`{"severity_text":"WARN","body":"x"}`, "WARN"},
		{`{"levelname":"WARNING","name":"root"}`, "WARN"},
		{`{"@l":"Information","@m":"started"}`, "INFO"},

		// Common level value vocabularies.
		{"level=trace entering fn", "DEBUG"},
		{"level=verbose details", "DEBUG"},
		{"level=silly noise", "DEBUG"},
		{"level=information started", "INFO"},
		{"level=notice heads up", "INFO"},
		{"level=warning deprecated", "WARN"},
		{"level=err short alias", "ERROR"},
		{"level=critical db down", "FATAL"},
		{"level=crit db down", "FATAL"},
		{"level=emerg system down", "FATAL"},
		{"level=alert page oncall", "FATAL"},
		{"level=panic goroutine stack", "FATAL"},
		{"level=fatal shutting down", "FATAL"},
		{"level=bogus unknown value", "INFO"},

		// Single-letter header.
		{"E0505 07:56:21.304123  1 reflector.go:1 watch failed", "ERROR"},
		{"W0505 07:56:21.304123  1 throttle.go:1 slow", "WARN"},
		{"I0505 07:56:21.304123  1 server.go:1 listening", "INFO"},
		{"F0505 07:56:21.304123  1 panic.go:1 boom", "FATAL"},
		{"I2024 not a klog header, no time component", "INFO"},

		// A level word used as a field key is not a severity.
		{"connection error=timeout reset", "INFO"},
		{"connection error = timeout reset", "INFO"}, // whitespace before '='
		{"feature warn=disabled in config", "INFO"},
		{"processed errors=2 warnings=0", "INFO"},

		// Edge cases.
		{"TERROR raised in prose", "INFO"},                  // level word embedded in a larger word
		{"adjust the log level for verbosity", "INFO"},      // "level" with no value is not a declared field
		{"unexpected ERROR after INFO checkpoint", "ERROR"}, // fallback picks highest severity, not first seen
		{"DEBUG trace then WARN raised", "WARN"},            // WARN outranks DEBUG
		{"i0501 12:34:56.000 lowercase prefix", "INFO"},     // header match is case-sensitive
		{"E051 12:34:56 malformed header", "INFO"},          // header needs a 4-digit date
		{"level=warning, retrying", "WARN"},                 // declared value with trailing punctuation
		{"level=warn nested level=error", "WARN"},           // first declared field wins
		{"goroutine 1 [running]:", "INFO"},                  // stack dump, no level
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractLogLevel(tt.input)
			if result != tt.expected {
				t.Errorf("extractLogLevel(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestBuildSearchBody(t *testing.T) {
	query := map[string]interface{}{
		"size": 10,
		"query": map[string]interface{}{
			"match_all": map[string]interface{}{},
		},
	}

	reader := buildSearchBody(query)
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}

	b := make([]byte, 1024)
	n, _ := reader.Read(b)
	if n == 0 {
		t.Error("expected non-empty body")
	}

	if !strings.Contains(string(b[:n]), "match_all") {
		t.Error("expected body to contain 'match_all'")
	}
}

func TestParseSearchResponse(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		jsonStr := `{
			"took": 5,
			"timed_out": false,
			"hits": {
				"total": {
					"value": 2,
					"relation": "eq"
				},
				"hits": [
					{"_id": "1", "_source": {"log": "test1"}},
					{"_id": "2", "_source": {"log": "test2"}}
				]
			}
		}`

		resp, err := parseSearchResponse(strings.NewReader(jsonStr))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Took != 5 {
			t.Errorf("expected took=5, got %d", resp.Took)
		}
		if resp.TimedOut {
			t.Error("expected timed_out=false")
		}
		if resp.Hits.Total.Value != 2 {
			t.Errorf("expected total=2, got %d", resp.Hits.Total.Value)
		}
		if len(resp.Hits.Hits) != 2 {
			t.Errorf("expected 2 hits, got %d", len(resp.Hits.Hits))
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := parseSearchResponse(strings.NewReader("not json"))
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}
