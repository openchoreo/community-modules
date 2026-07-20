// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

// Test UID fixtures use real UUIDs, matching what the Observer sends (the
// generated API models the search-scope UIDs as UUID types).
const (
	testComponentUID   = "f3b8e2a4-6c1d-4e9f-9a2b-3d5c7e8f0a1c"
	testProjectUID     = "9d4c2b1a-8e7f-4a3b-b6c5-2f1e0d9c8b7a"
	testEnvironmentUID = "5e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f7a8b9"
)

func specByKey(t *testing.T, key string) metricSpec {
	t.Helper()
	for _, s := range resourceMetricSpecs {
		if s.key == key {
			return s
		}
	}
	t.Fatalf("unknown spec key %q", key)
	return metricSpec{}
}

func TestBuildResourceMetricsFilterBaseNoScope(t *testing.T) {
	// Scoping is UID-only; the rule namespace is never a metric filter clause.
	f := BuildResourceMetricsFilter(specByKey(t, "cpuUsage"), MetricsQueryParams{Namespace: "default"})

	for _, want := range []string{
		`metric.type = "kubernetes.io/container/cpu/core_usage_time"`,
		`resource.type = "k8s_container"`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("filter missing %q:\n%s", want, f)
		}
	}
	// Namespace must NOT appear in the metric filter (the pod's
	// openchoreo.dev/namespace label carries the control-plane namespace,
	// not the rule's data-plane namespace).
	if strings.Contains(f, "openchoreo.dev/namespace") {
		t.Errorf("namespace must not be a metric filter clause:\n%s", f)
	}
	if strings.Contains(f, "component-uid") || strings.Contains(f, "project-uid") || strings.Contains(f, "environment-uid") {
		t.Errorf("filter has UID clauses without UIDs set:\n%s", f)
	}
}

func TestBuildResourceMetricsFilterAllScopes(t *testing.T) {
	f := BuildResourceMetricsFilter(specByKey(t, "cpuUsage"), MetricsQueryParams{
		Namespace:      "default",
		ComponentUID:   testComponentUID,
		ProjectUID:     testProjectUID,
		EnvironmentUID: testEnvironmentUID,
	})

	for _, want := range []string{
		`metadata.user_labels."openchoreo.dev/component-uid" = "` + testComponentUID + `"`,
		`metadata.user_labels."openchoreo.dev/project-uid" = "` + testProjectUID + `"`,
		`metadata.user_labels."openchoreo.dev/environment-uid" = "` + testEnvironmentUID + `"`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("filter missing %q:\n%s", want, f)
		}
	}
}

func TestBuildResourceMetricsFilterDropsZeroUUID(t *testing.T) {
	f := BuildResourceMetricsFilter(specByKey(t, "cpuUsage"), MetricsQueryParams{
		Namespace:    "default",
		ComponentUID: zeroUUID,
	})
	if strings.Contains(f, "component-uid") {
		t.Errorf("zero UUID must not become a filter clause:\n%s", f)
	}
}

func TestBuildResourceMetricsFilterMemoryTypeClause(t *testing.T) {
	f := BuildResourceMetricsFilter(specByKey(t, "memoryUsage"), MetricsQueryParams{Namespace: "default"})
	if !strings.Contains(f, `metric.labels.memory_type = "non-evictable"`) {
		t.Errorf("memoryUsage filter must pin memory_type to non-evictable:\n%s", f)
	}

	f = BuildResourceMetricsFilter(specByKey(t, "memoryLimits"), MetricsQueryParams{Namespace: "default"})
	if strings.Contains(f, "memory_type") {
		t.Errorf("memoryLimits filter must not carry memory_type:\n%s", f)
	}
}

func TestBuildResourceMetricsFilterEscapesValues(t *testing.T) {
	// UID values are the caller-supplied strings that reach the filter; verify
	// they cannot break out of the quoted literal.
	f := BuildResourceMetricsFilter(specByKey(t, "cpuUsage"), MetricsQueryParams{
		ComponentUID: `uid"injected` + "\n" + `x`,
	})
	if !strings.Contains(f, `= "uid\"injected x"`) {
		t.Errorf("component UID value not escaped:\n%s", f)
	}
}

func TestBuildListRequestAggregation(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	p := MetricsQueryParams{
		Namespace: "default",
		StartTime: start,
		EndTime:   start.Add(time.Hour),
		Step:      5 * time.Minute,
	}

	req := buildListRequest("my-project", specByKey(t, "cpuUsage"), p)
	if req.Name != "projects/my-project" {
		t.Errorf("name = %q", req.Name)
	}
	if got := req.Aggregation.AlignmentPeriod.AsDuration(); got != 5*time.Minute {
		t.Errorf("alignment period = %v", got)
	}
	if req.Aggregation.PerSeriesAligner != monitoringpb.Aggregation_ALIGN_RATE {
		t.Errorf("cpuUsage aligner = %v, want ALIGN_RATE", req.Aggregation.PerSeriesAligner)
	}
	if req.Aggregation.CrossSeriesReducer != monitoringpb.Aggregation_REDUCE_SUM {
		t.Errorf("reducer = %v, want REDUCE_SUM", req.Aggregation.CrossSeriesReducer)
	}
	if got := req.Interval.StartTime.AsTime(); !got.Equal(start) {
		t.Errorf("interval start = %v", got)
	}

	req = buildListRequest("my-project", specByKey(t, "memoryUsage"), p)
	if req.Aggregation.PerSeriesAligner != monitoringpb.Aggregation_ALIGN_MEAN {
		t.Errorf("memoryUsage aligner = %v, want ALIGN_MEAN", req.Aggregation.PerSeriesAligner)
	}
}

func TestBuildListRequestClampsSubMinuteStep(t *testing.T) {
	p := MetricsQueryParams{Namespace: "default", Step: 10 * time.Second}
	req := buildListRequest("my-project", specByKey(t, "cpuUsage"), p)
	if got := req.Aggregation.AlignmentPeriod.AsDuration(); got != time.Minute {
		t.Errorf("alignment period = %v, want 1m clamp", got)
	}
}

func TestBuildPingRequest(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	req := buildPingRequest("my-project", now)
	if req.PageSize != 1 {
		t.Errorf("page size = %d", req.PageSize)
	}
	if req.View != monitoringpb.ListTimeSeriesRequest_HEADERS {
		t.Errorf("view = %v, want HEADERS", req.View)
	}
	if got := req.Interval.StartTime.AsTime(); !got.Equal(now.Add(-time.Hour)) {
		t.Errorf("ping window start = %v", got)
	}
}
