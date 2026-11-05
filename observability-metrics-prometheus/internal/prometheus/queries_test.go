// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"os"
	"regexp"
	"slices"
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

func TestBuildPodLabels(t *testing.T) {
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
				"label_openchoreo_dev_component_uid=",
				"label_openchoreo_dev_project_uid=",
				"label_openchoreo_dev_environment_uid=",
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
				"label_openchoreo_dev_project_uid=",
				"label_openchoreo_dev_environment_uid=",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPodLabels(tt.namespace, tt.componentUID, tt.projectUID, tt.environmentUID, "", false)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("BuildPodLabels() = %q, want to contain %q", got, want)
				}
			}
			for _, notWant := range tt.wantNotContain {
				if strings.Contains(got, notWant) {
					t.Errorf("BuildPodLabels() = %q, should not contain %q", got, notWant)
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

func TestBuildPodLabels_ReleaseLines(t *testing.T) {
	const base = `label_openchoreo_dev_namespace="ns",label_openchoreo_dev_project_uid="p",label_openchoreo_dev_environment_uid="e"`
	tests := []struct {
		name     string
		line     string
		allLines bool
		want     string
	}{
		{name: "main line", want: onePerPodPrefix + `kube_pod_labels{job="kube-state-metrics",` + base +
			`,label_openchoreo_dev_component_uid="c",label_openchoreo_dev_release_line_uid=""})`},
		{name: "one release line matches on its UID alone", line: "l1", want: onePerPodPrefix + `kube_pod_labels{job="kube-state-metrics",` + base +
			`,label_openchoreo_dev_release_line_uid="l1"})`},
		{name: "every line", allLines: true, want: onePerPodPrefix + `kube_pod_labels{job="kube-state-metrics",` + base +
			`,label_openchoreo_dev_component_uid="c"} or ` +
			`kube_pod_labels{job="kube-state-metrics",` + base + `,label_openchoreo_dev_release_line_component_uid="c"})`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildPodLabels("ns", "c", "p", "e", tt.line, tt.allLines); got != tt.want {
				t.Errorf("BuildPodLabels() =\n %s\nwant\n %s", got, tt.want)
			}
		})
	}
}

func TestBuildComponentPodLabels(t *testing.T) {
	got := BuildComponentPodLabels("comp-1", "proj-1", "env-1", "")
	wantParts := []string{
		`label_openchoreo_dev_component_uid="comp-1"`,
		`label_openchoreo_dev_project_uid="proj-1"`,
		`label_openchoreo_dev_environment_uid="env-1"`,
		`label_openchoreo_dev_release_line_uid=""`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Errorf("BuildComponentPodLabels() = %q, want to contain %q", got, want)
		}
	}

	got = BuildComponentPodLabels("comp-1", "proj-1", "env-1", "line-1")
	if strings.Contains(got, "component_uid=") || !strings.Contains(got, `label_openchoreo_dev_release_line_uid="line-1"`) {
		t.Errorf("a release line's rule must match its UID alone: %q", got)
	}
}

func TestResourceQueryBuilders(t *testing.T) {
	labelFilter := BuildPodLabels("test-ns", "", "", "", "", false)
	sumByClause := "label_openchoreo_dev_component_uid"
	groupLeftClause := "group_left (label_openchoreo_dev_component_uid)"

	tests := []struct {
		name     string
		queryFn  func(string, string, string) string
		contains []string
	}{
		{"CPUUsage", BuildCPUUsageQuery, []string{`max by (namespace, pod, container) (rate(container_cpu_usage_seconds_total{container!=""}[2m]))`, labelFilter}},
		{"CPURequests", BuildCPURequestsQuery, []string{`max by (namespace, pod, container) (kube_pod_container_resource_requests{resource="cpu",job="kube-state-metrics"})`, labelFilter}},
		{"CPULimits", BuildCPULimitsQuery, []string{`max by (namespace, pod, container) (kube_pod_container_resource_limits{resource="cpu",job="kube-state-metrics"})`, labelFilter}},
		{"MemoryUsage", BuildMemoryUsageQuery, []string{`max by (namespace, pod, container) (container_memory_working_set_bytes{container!=""})`, labelFilter}},
		{"MemoryRequests", BuildMemoryRequestsQuery, []string{`max by (namespace, pod, container) (kube_pod_container_resource_requests{resource="memory",job="kube-state-metrics"})`, labelFilter}},
		{"MemoryLimits", BuildMemoryLimitsQuery, []string{`max by (namespace, pod, container) (kube_pod_container_resource_limits{resource="memory",job="kube-state-metrics"})`, labelFilter}},
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
	labelFilter := BuildPodLabels("test-ns", "", "", "", "", false)
	sumByClause := "label_openchoreo_dev_component_uid"
	groupLeftClause := "group_left (label_openchoreo_dev_component_uid)"

	serverReporter := `reporter="server"`
	podJoin := "destination_namespace, destination_pod"
	kubePodLabels := "kube_pod_labels"

	tests := []struct {
		name     string
		queryFn  func(string, string, string) string
		contains []string
	}{
		{"HTTPRequestCount", BuildHTTPRequestCountQuery, []string{"hubble_http_requests_total", serverReporter, podJoin, kubePodLabels, labelFilter}},
		{"SuccessfulHTTPRequestCount", BuildSuccessfulHTTPRequestCountQuery, []string{"hubble_http_requests_total", serverReporter, `status=~"^[123]..?$"`, podJoin, kubePodLabels, labelFilter}},
		{"UnsuccessfulHTTPRequestCount", BuildUnsuccessfulHTTPRequestCountQuery, []string{"hubble_http_requests_total", serverReporter, `status=~"^[45]..?$"`, podJoin, kubePodLabels, labelFilter}},
		{"MeanHTTPRequestLatency", BuildMeanHTTPRequestLatencyQuery, []string{"hubble_http_request_duration_seconds_sum", serverReporter, podJoin, kubePodLabels, labelFilter}},
		{"P50Latency", Build50thPercentileHTTPRequestLatencyQuery, []string{"histogram_quantile", "0.5", serverReporter, podJoin, kubePodLabels, labelFilter}},
		{"P90Latency", Build90thPercentileHTTPRequestLatencyQuery, []string{"histogram_quantile", "0.9", serverReporter, podJoin, kubePodLabels, labelFilter}},
		{"P99Latency", Build99thPercentileHTTPRequestLatencyQuery, []string{"histogram_quantile", "0.99", serverReporter, podJoin, kubePodLabels, labelFilter}},
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

// onePerPodPrefix is the onePerPod wrapper, spelled out so a change to the kept labels is deliberate.
const onePerPodPrefix = "max by (namespace, pod, label_openchoreo_dev_component_uid, label_openchoreo_dev_project_uid, " +
	"label_openchoreo_dev_environment_uid, label_openchoreo_dev_release_line_uid, label_openchoreo_dev_release_line_component_uid, " +
	"label_openchoreo_dev_namespace, label_openchoreo_dev_component, label_openchoreo_dev_project, label_openchoreo_dev_environment) ("

// TestPodLabelNames_MatchKubeStateMetricsAllowlist keeps onePerPod in step with the pod labels the chart's
// kube-state-metrics exports: a label missing from podLabelNames would be dropped before the join.
func TestPodLabelNames_MatchKubeStateMetricsAllowlist(t *testing.T) {
	values, err := os.ReadFile("../../helm/values.yaml")
	if err != nil {
		t.Fatalf("read chart values: %v", err)
	}
	m := regexp.MustCompile(`pods=\[([^\]]*)\]`).FindSubmatch(values)
	if m == nil {
		t.Fatal("no pods=[...] entry in kube-state-metrics metricLabelsAllowlist")
	}
	var want []string
	for _, l := range strings.Split(string(m[1]), ",") {
		want = append(want, prometheusLabelName(strings.TrimSpace(l)))
	}
	got := podLabelNames[2:]
	slices.Sort(want)
	got = slices.Sorted(slices.Values(got))
	if !slices.Equal(got, want) {
		t.Errorf("podLabelNames = %v, allowlist = %v", got, want)
	}
}

func TestPodLabelSelectors_OnePerPod(t *testing.T) {
	selectors := map[string]string{
		"query":    BuildPodLabels("ns", "c", "p", "e", "", true),
		"alert":    BuildComponentPodLabels("c", "p", "e", ""),
		"topology": BuildRuntimeTopologyComponentEdgeRequestCountQuery("3600s", `label_openchoreo_dev_namespace="ns"`),
	}
	for name, expr := range selectors {
		t.Run(name, func(t *testing.T) {
			if got, want := strings.Count(expr, onePerPodPrefix+"kube_pod_labels{"), strings.Count(expr, "(kube_pod_labels{"); got == 0 || got != want {
				t.Errorf("every kube_pod_labels join must keep one series per pod (%d of %d wrapped):\n%s", got, want, expr)
			}
		})
	}
}
