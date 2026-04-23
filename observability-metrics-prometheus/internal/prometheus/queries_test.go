// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"strings"
	"testing"
)

func TestPrometheusLabelName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"openchoreo.dev/component-uid", "label_openchoreo_dev_component_uid"},
		{"openchoreo.dev/project-uid", "label_openchoreo_dev_project_uid"},
		{"openchoreo.dev/environment-uid", "label_openchoreo_dev_environment_uid"},
		{"openchoreo.dev/namespace", "label_openchoreo_dev_namespace"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := prometheusLabelName(tt.input)
			if got != tt.expected {
				t.Errorf("prometheusLabelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestBuildLabelFilter(t *testing.T) {
	tests := []struct {
		name           string
		namespace      string
		componentUID   string
		projectUID     string
		environmentUID string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:         "namespace only",
			namespace:    "test-ns",
			wantContains: []string{`label_openchoreo_dev_namespace="test-ns"`},
			wantNotContain: []string{
				"label_openchoreo_dev_component_uid",
				"label_openchoreo_dev_project_uid",
				"label_openchoreo_dev_environment_uid",
			},
		},
		{
			name:           "all fields",
			namespace:      "test-ns",
			componentUID:   "c5f0a8d3-7e2b-4d9c-a1f4-6b8e3c0d5a7f",
			projectUID:     "d6a1b9e4-8f3c-4e0d-b2a5-7c9f4d1e6b8a",
			environmentUID: "e7b2c0f5-9a4d-4f1e-c3b6-8d0a5e2f7c9b",
			wantContains: []string{
				`label_openchoreo_dev_namespace="test-ns"`,
				`label_openchoreo_dev_component_uid="c5f0a8d3-7e2b-4d9c-a1f4-6b8e3c0d5a7f"`,
				`label_openchoreo_dev_project_uid="d6a1b9e4-8f3c-4e0d-b2a5-7c9f4d1e6b8a"`,
				`label_openchoreo_dev_environment_uid="e7b2c0f5-9a4d-4f1e-c3b6-8d0a5e2f7c9b"`,
			},
		},
		{
			name:         "partial - only component",
			namespace:    "test-ns",
			componentUID: "c5f0a8d3-7e2b-4d9c-a1f4-6b8e3c0d5a7f",
			wantContains: []string{
				`label_openchoreo_dev_namespace="test-ns"`,
				`label_openchoreo_dev_component_uid="c5f0a8d3-7e2b-4d9c-a1f4-6b8e3c0d5a7f"`,
			},
			wantNotContain: []string{
				"label_openchoreo_dev_project_uid",
				"label_openchoreo_dev_environment_uid",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildLabelFilter(tt.namespace, tt.componentUID, tt.projectUID, tt.environmentUID)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("BuildLabelFilter() = %q, want to contain %q", got, want)
				}
			}
			for _, notWant := range tt.wantNotContain {
				if strings.Contains(got, notWant) {
					t.Errorf("BuildLabelFilter() = %q, should not contain %q", got, notWant)
				}
			}
		})
	}
}

func TestBuildScopeLabelNames(t *testing.T) {
	tests := []struct {
		name           string
		componentUID   string
		projectUID     string
		environmentUID string
		wantLen        int
	}{
		{"none", "", "", "", 0},
		{"component only", "comp-1", "", "", 1},
		{"all", "comp-1", "proj-1", "env-1", 3},
		{"project and env", "", "proj-1", "env-1", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildScopeLabelNames(tt.componentUID, tt.projectUID, tt.environmentUID)
			if len(got) != tt.wantLen {
				t.Errorf("BuildScopeLabelNames() returned %d labels, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestBuildSumByClause(t *testing.T) {
	tests := []struct {
		name        string
		metricLabel string
		scopeLabels []string
		want        string
	}{
		{"empty", "", nil, ""},
		{"metric only", "container", nil, "container"},
		{"scope only", "", []string{"label_a", "label_b"}, "label_a, label_b"},
		{"both", "container", []string{"label_a"}, "label_a, container"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildSumByClause(tt.metricLabel, tt.scopeLabels)
			if got != tt.want {
				t.Errorf("BuildSumByClause() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildHistogramSumByClause(t *testing.T) {
	tests := []struct {
		name        string
		sumByClause string
		want        string
	}{
		{"empty", "", "le"},
		{"whitespace", "   ", "le"},
		{"with labels", "label_a, label_b", "label_a, label_b, le"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildHistogramSumByClause(tt.sumByClause)
			if got != tt.want {
				t.Errorf("BuildHistogramSumByClause() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildGroupLeftClause(t *testing.T) {
	tests := []struct {
		name        string
		scopeLabels []string
		want        string
	}{
		{"empty", nil, "group_left"},
		{"one label", []string{"label_a"}, "group_left (label_a)"},
		{"two labels", []string{"label_a", "label_b"}, "group_left (label_a, label_b)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildGroupLeftClause(tt.scopeLabels)
			if got != tt.want {
				t.Errorf("BuildGroupLeftClause() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildComponentLabelFilter(t *testing.T) {
	got := BuildComponentLabelFilter("comp-1", "proj-1", "env-1")
	wantParts := []string{
		`label_openchoreo_dev_component_uid="comp-1"`,
		`label_openchoreo_dev_project_uid="proj-1"`,
		`label_openchoreo_dev_environment_uid="env-1"`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Errorf("BuildComponentLabelFilter() = %q, want to contain %q", got, want)
		}
	}
}

func TestResourceQueryBuilders(t *testing.T) {
	labelFilter := `label_openchoreo_dev_namespace="test-ns"`
	sumByClause := "label_openchoreo_dev_component_uid"
	groupLeftClause := "group_left (label_openchoreo_dev_component_uid)"

	tests := []struct {
		name     string
		queryFn  func(string, string, string) string
		contains []string
	}{
		{"CPUUsage", BuildCPUUsageQuery, []string{"container_cpu_usage_seconds_total", "rate(", labelFilter}},
		{"CPURequests", BuildCPURequestsQuery, []string{"kube_pod_container_resource_requests", `resource="cpu"`, labelFilter}},
		{"CPULimits", BuildCPULimitsQuery, []string{"kube_pod_container_resource_limits", `resource="cpu"`, labelFilter}},
		{"MemoryUsage", BuildMemoryUsageQuery, []string{"container_memory_working_set_bytes", labelFilter}},
		{"MemoryRequests", BuildMemoryRequestsQuery, []string{"kube_pod_container_resource_requests", `resource="memory"`, labelFilter}},
		{"MemoryLimits", BuildMemoryLimitsQuery, []string{"kube_pod_container_resource_limits", `resource="memory"`, labelFilter}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := tt.queryFn(labelFilter, sumByClause, groupLeftClause)
			for _, want := range tt.contains {
				if !strings.Contains(query, want) {
					t.Errorf("%s query = %q, want to contain %q", tt.name, query, want)
				}
			}
		})
	}
}

func TestHTTPQueryBuilders(t *testing.T) {
	labelFilter := `label_openchoreo_dev_namespace="test-ns"`
	sumByClause := "label_openchoreo_dev_component_uid"
	groupLeftClause := "group_left (label_openchoreo_dev_component_uid)"

	tests := []struct {
		name     string
		queryFn  func(string, string, string) string
		contains []string
	}{
		{"HTTPRequestCount", BuildHTTPRequestCountQuery, []string{"hubble_http_requests_total", labelFilter}},
		{"SuccessfulHTTPRequestCount", BuildSuccessfulHTTPRequestCountQuery, []string{"hubble_http_requests_total", `status=~"^[123]..?$"`, labelFilter}},
		{"UnsuccessfulHTTPRequestCount", BuildUnsuccessfulHTTPRequestCountQuery, []string{"hubble_http_requests_total", `status=~"^[45]..?$"`, labelFilter}},
		{"MeanHTTPRequestLatency", BuildMeanHTTPRequestLatencyQuery, []string{"hubble_http_request_duration_seconds_sum", labelFilter}},
		{"P50Latency", Build50thPercentileHTTPRequestLatencyQuery, []string{"histogram_quantile", "0.5", labelFilter}},
		{"P90Latency", Build90thPercentileHTTPRequestLatencyQuery, []string{"histogram_quantile", "0.9", labelFilter}},
		{"P99Latency", Build99thPercentileHTTPRequestLatencyQuery, []string{"histogram_quantile", "0.99", labelFilter}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := tt.queryFn(labelFilter, sumByClause, groupLeftClause)
			for _, want := range tt.contains {
				if !strings.Contains(query, want) {
					t.Errorf("%s query = %q, want to contain %q", tt.name, query, want)
				}
			}
		})
	}
}
