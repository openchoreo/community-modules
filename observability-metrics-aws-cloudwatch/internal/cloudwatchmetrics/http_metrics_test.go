// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"strings"
	"testing"
	"time"
)

func TestBuildHTTPMetricsLogsInsightsQueryScopesToDestination(t *testing.T) {
	query := BuildHTTPMetricsLogsInsightsQuery(HTTPMetricsQueryParams{
		Namespace:      `pay"ments`,
		ComponentUID:   "comp-1",
		ProjectUID:     "proj-1",
		EnvironmentUID: "env-1",
		StepSeconds:    300,
	})
	for _, want := range []string{
		`reporter = "server"`,
		"ispresent(DestinationComponentUID)",
		`DestinationComponentUID = "comp-1"`,
		`DestinationProjectUID = "proj-1"`,
		`DestinationEnvironmentUID = "env-1"`,
		"by bin(5m) as ts, status",
		"sum(hubble_http_requests_total) as request_total",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q:\n%s", want, query)
		}
	}
	// Source-side identity must not leak into the scope filter.
	if strings.Contains(query, "SourceComponentUID =") {
		t.Fatalf("query should not filter on source component:\n%s", query)
	}
	// The request namespace is the OpenChoreo namespace (e.g. "default"), not
	// Hubble's raw dataplane namespace, so it must NOT be applied as a filter.
	// The destination component UID is the discriminator.
	if strings.Contains(query, "destination_namespace") {
		t.Fatalf("query should not reference Hubble namespace:\n%s", query)
	}
}

func TestBuildHTTPMetricsBucketQueryScopesAndBinsByLe(t *testing.T) {
	query := BuildHTTPMetricsBucketLogsInsightsQuery(HTTPMetricsQueryParams{
		ComponentUID: "comp-1",
		StepSeconds:  300,
	})
	for _, want := range []string{
		"hubble_http_request_duration_seconds_lebucket",
		"ispresent(le)",
		`DestinationComponentUID = "comp-1"`,
		"by bin(5m) as ts, le",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("bucket query missing %q:\n%s", want, query)
		}
	}
}

func TestApplyHTTPMetricsPercentilesBucketsByTime(t *testing.T) {
	result := &HTTPMetricsResult{}
	rows := []map[string]string{
		{"ts": "2026-07-14 09:00:00.000", "le": "0.1", "bucket_count": "5"},
		{"ts": "2026-07-14 09:00:00.000", "le": "0.2", "bucket_count": "8"},
		{"ts": "2026-07-14 09:00:00.000", "le": "0.5", "bucket_count": "10"},
		{"ts": "2026-07-14 09:00:00.000", "le": "+Inf", "bucket_count": "10"},
	}
	applyHTTPMetricsPercentiles(result, rows)
	if len(result.LatencyP50) != 1 || len(result.LatencyP90) != 1 || len(result.LatencyP99) != 1 {
		t.Fatalf("expected one percentile point each, got %d/%d/%d",
			len(result.LatencyP50), len(result.LatencyP90), len(result.LatencyP99))
	}
	if result.LatencyP50[0].Value != 0.1 {
		t.Fatalf("p50 = %v, want 0.1", result.LatencyP50[0].Value)
	}
}

func TestBuildHTTPMetricsLogsInsightsQueryOmitsBlankScope(t *testing.T) {
	query := BuildHTTPMetricsLogsInsightsQuery(HTTPMetricsQueryParams{
		Namespace: "default",
	})
	if strings.Contains(query, "DestinationComponentUID =") {
		t.Fatalf("blank componentUID should not add an equality filter:\n%s", query)
	}
	// Logs Insights ignores a seconds unit in bin(); a zero/sub-minute step must
	// round up to a one-minute bucket, not bin(1s).
	if !strings.Contains(query, "bin(1m)") {
		t.Fatalf("zero step should default to bin(1m):\n%s", query)
	}
}

func TestLogsInsightsBinFormatsMinutesAndHours(t *testing.T) {
	cases := []struct {
		stepSeconds int32
		wantExpr    string
		wantWidth   float64
	}{
		{0, "1m", 60},
		{60, "1m", 60},
		{300, "5m", 300},
		{900, "15m", 900},
		{1800, "30m", 1800},
		{3600, "1h", 3600},
		{90, "2m", 120}, // sub-minute remainder rounds up
	}
	for _, tc := range cases {
		expr, width := logsInsightsBin(tc.stepSeconds)
		if expr != tc.wantExpr || width != tc.wantWidth {
			t.Fatalf("logsInsightsBin(%d) = (%q, %v), want (%q, %v)", tc.stepSeconds, expr, width, tc.wantExpr, tc.wantWidth)
		}
	}
}

func TestHTTPMetricsFromRowsBucketsAndRates(t *testing.T) {
	// Two 60s buckets. Request events carry status; the duration (histogram)
	// event carries Count/Sum but no status. Values are per-interval deltas, so
	// request counts are summed then divided by the step to yield a rate.
	rows := []map[string]string{
		{"ts": "2026-07-14 09:00:00.000", "status": "200", "request_total": "90"},
		{"ts": "2026-07-14 09:00:00.000", "status": "404", "request_total": "30"},
		{"ts": "2026-07-14 09:00:00.000", "status": "", "duration_count": "120", "duration_sum": "24"},
		{"ts": "2026-07-14 09:01:00.000", "status": "200", "request_total": "60"},
	}

	got := httpMetricsFromRows(rows, 60)

	if len(got.RequestCount) != 2 {
		t.Fatalf("expected 2 request-count points, got %#v", got.RequestCount)
	}
	// Bucket 1: (90+30)/60 = 2 req/s.
	if v := got.RequestCount[0].Value; v != 2 {
		t.Fatalf("bucket 1 request rate = %v, want 2", v)
	}
	// Successful = 90/60 = 1.5; unsuccessful = 30/60 = 0.5.
	if v := got.SuccessfulRequestCount[0].Value; v != 1.5 {
		t.Fatalf("bucket 1 successful rate = %v, want 1.5", v)
	}
	if v := got.UnsuccessfulRequestCount[0].Value; v != 0.5 {
		t.Fatalf("bucket 1 unsuccessful rate = %v, want 0.5", v)
	}
	// Mean latency = 24/120 = 0.2s, only for the bucket with duration data.
	if len(got.MeanLatency) != 1 {
		t.Fatalf("expected 1 mean-latency point, got %#v", got.MeanLatency)
	}
	if v := got.MeanLatency[0].Value; v != 0.2 {
		t.Fatalf("mean latency = %v, want 0.2", v)
	}
	if !got.MeanLatency[0].Timestamp.Equal(time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected mean-latency timestamp: %s", got.MeanLatency[0].Timestamp)
	}
	// Buckets must be time-ordered.
	if !got.RequestCount[0].Timestamp.Before(got.RequestCount[1].Timestamp) {
		t.Fatalf("request-count points not sorted ascending")
	}
}

func TestParseLogsInsightsTimeFormats(t *testing.T) {
	want := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	for _, in := range []string{
		"2026-07-14 09:00:00.000",
		"2026-07-14 09:00:00",
		"2026-07-14T09:00:00Z",
	} {
		got, ok := parseLogsInsightsTime(in)
		if !ok || !got.Equal(want) {
			t.Fatalf("parseLogsInsightsTime(%q) = %s, %v", in, got, ok)
		}
	}
	if _, ok := parseLogsInsightsTime(""); ok {
		t.Fatalf("empty string should not parse")
	}
}
