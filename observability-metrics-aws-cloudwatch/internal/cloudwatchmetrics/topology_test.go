// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"strings"
	"testing"
	"time"
)

func TestBuildRuntimeTopologyLogsInsightsQueryUsesEnrichedFields(t *testing.T) {
	query := BuildRuntimeTopologyLogsInsightsQuery(RuntimeTopologyQueryParams{
		Namespace:      "default",
		ProjectUID:     testProjectUID,
		EnvironmentUID: testEnvironmentUID,
	})
	for _, want := range []string{
		`SourceProjectUID = "` + testProjectUID + `"`,
		`DestinationProjectUID = "` + testProjectUID + `"`,
		`SourceEnvironmentUID = "` + testEnvironmentUID + `"`,
		`DestinationEnvironmentUID = "` + testEnvironmentUID + `"`,
		"SourceComponentUID",
		"DestinationComponentUID",
		"sum(hubble_http_requests_total) as request_total",
		"hubble_http_request_duration_seconds.Count",
		"hubble_http_request_duration_seconds.Sum",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q:\n%s", want, query)
		}
	}
	// The request namespace is the OpenChoreo namespace (e.g. "default"), not
	// Hubble's raw dataplane namespace, so it must NOT be applied as a filter on
	// source_namespace/destination_namespace (that would match nothing). The raw
	// namespace fields are still selected for display, hence the `= "` filter form
	// is what we assert absent.
	for _, unwanted := range []string{
		`source_namespace = "`,
		`destination_namespace = "`,
	} {
		if strings.Contains(query, unwanted) {
			t.Fatalf("query should not filter on Hubble namespace (%q):\n%s", unwanted, query)
		}
	}
}

func TestBuildRuntimeTopologyBucketQueryUsesLeAndBucketField(t *testing.T) {
	query := BuildRuntimeTopologyBucketLogsInsightsQuery(RuntimeTopologyQueryParams{
		Namespace:      "default",
		ProjectUID:     testProjectUID,
		EnvironmentUID: testEnvironmentUID,
	})
	for _, want := range []string{
		"hubble_http_request_duration_seconds_lebucket",
		"ispresent(le)",
		`SourceProjectUID = "` + testProjectUID + `"`,
		`DestinationEnvironmentUID = "` + testEnvironmentUID + `"`,
		"sum(hubble_http_request_duration_seconds_lebucket) as bucket_count",
		"by SourceComponentUID, DestinationComponentUID, le",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("bucket query missing %q:\n%s", want, query)
		}
	}
}

func TestApplyTopologyPercentilesAttachesToEdges(t *testing.T) {
	result := &RuntimeTopologyResult{Edges: []RuntimeTopologyEdgeResult{
		{SourceComponentUID: testSourceComponentUID, DestinationComponentUID: testDestinationComponentUID, RequestCount: 10},
	}}
	rows := []map[string]string{
		{"SourceComponentUID": testSourceComponentUID, "DestinationComponentUID": testDestinationComponentUID, "le": "0.1", "bucket_count": "5"},
		{"SourceComponentUID": testSourceComponentUID, "DestinationComponentUID": testDestinationComponentUID, "le": "0.2", "bucket_count": "8"},
		{"SourceComponentUID": testSourceComponentUID, "DestinationComponentUID": testDestinationComponentUID, "le": "0.5", "bucket_count": "10"},
		{"SourceComponentUID": testSourceComponentUID, "DestinationComponentUID": testDestinationComponentUID, "le": "+Inf", "bucket_count": "10"},
	}
	applyTopologyPercentiles(result, rows, "")
	e := result.Edges[0]
	if e.LatencyP50 == nil || e.LatencyP90 == nil || e.LatencyP99 == nil {
		t.Fatalf("expected percentiles attached, got %v %v %v", e.LatencyP50, e.LatencyP90, e.LatencyP99)
	}
	if *e.LatencyP50 != 0.1 {
		t.Fatalf("p50 = %v, want 0.1", *e.LatencyP50)
	}
}

func TestRuntimeTopologyFromRowsAggregatesEdges(t *testing.T) {
	// Request events carry `status` but no duration; duration (histogram) events
	// carry Count/Sum but no status. The adapter folds both into one edge and
	// sums the per-interval delta values.
	rows := []map[string]string{
		{
			"SourceComponentUID": testSourceComponentUID, "SourceComponent": "frontend", "source_namespace": "payments",
			"DestinationComponentUID": testDestinationComponentUID, "DestinationComponent": "checkout", "destination_namespace": "payments",
			"status": "200", "request_total": "60",
		},
		{
			"SourceComponentUID": testSourceComponentUID, "SourceComponent": "frontend", "source_namespace": "payments",
			"DestinationComponentUID": testDestinationComponentUID, "DestinationComponent": "checkout", "destination_namespace": "payments",
			"status": "500", "request_total": "5",
		},
		{
			"SourceComponentUID": testSourceComponentUID, "SourceComponent": "frontend", "source_namespace": "payments",
			"DestinationComponentUID": testDestinationComponentUID, "DestinationComponent": "checkout", "destination_namespace": "payments",
			"status": "", "request_total": "0", "duration_count": "65", "duration_sum": "13",
		},
	}

	got := runtimeTopologyFromRows(rows, "")
	if len(got.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %#v", got.Edges)
	}
	edge := got.Edges[0]
	if edge.RequestCount != 65 {
		t.Fatalf("request count = %v, want 65", edge.RequestCount)
	}
	if edge.ErrorCount != 5 {
		t.Fatalf("error count = %v, want 5", edge.ErrorCount)
	}
	if edge.MeanLatency == nil || *edge.MeanLatency != 13.0/65.0 {
		t.Fatalf("mean latency = %v, want %v", edge.MeanLatency, 13.0/65.0)
	}
	if edge.SourceComponentName != "frontend" || edge.DestinationNamespace != "payments" {
		t.Fatalf("edge identity not populated: %#v", edge)
	}
}

func TestRuntimeTopologyFromRowsComponentFilter(t *testing.T) {
	rows := []map[string]string{
		{
			"SourceComponentUID": testSourceComponentUID, "DestinationComponentUID": testDestinationComponentUID,
			"status": "200", "request_total": "10",
		},
		{
			"SourceComponentUID": testOtherSourceUID, "DestinationComponentUID": testOtherDestinationUID,
			"status": "200", "request_total": "10",
		},
	}
	got := runtimeTopologyFromRows(rows, testSourceComponentUID)
	if len(got.Edges) != 1 || got.Edges[0].SourceComponentUID != testSourceComponentUID {
		t.Fatalf("component filter not applied: %#v", got.Edges)
	}
}

func TestGetRuntimeTopologyRejectsInvalidWindow(t *testing.T) {
	c := NewClientWithAWS(&stubCloudWatchAPI{}, nil, &stubSTSAPI{}, Config{}, nil)
	now := time.Now()
	_, err := c.GetRuntimeTopology(nil, RuntimeTopologyQueryParams{
		Namespace:      "payments",
		ProjectUID:     testProjectUID,
		EnvironmentUID: testEnvironmentUID,
		StartTime:      now,
		EndTime:        now,
	})
	if err == nil {
		t.Fatal("expected invalid window error")
	}
}
