// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// HTTPMetricsQueryParams captures a per-component HTTP RED metrics query.
//
// The component is identified on the destination side (server reporter): the
// series describe traffic terminating at the component, mirroring the Prometheus
// reference module which joins the destination pod of each Hubble series.
type HTTPMetricsQueryParams struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
	StartTime      time.Time
	EndTime        time.Time
	StepSeconds    int32
}

// HTTPMetricsResult carries the per-step HTTP RED series for a component.
//
// Values follow the Prometheus reference module's semantics: request counts are
// per-second rates over each step bucket and mean latency is in seconds. Latency
// percentiles are reconstructed per step bucket from the preserved `le` buckets
// (the collector emits a dedicated bucket series so they survive the awsemf
// histogram path).
type HTTPMetricsResult struct {
	RequestCount             []TimeValuePoint
	SuccessfulRequestCount   []TimeValuePoint
	UnsuccessfulRequestCount []TimeValuePoint
	MeanLatency              []TimeValuePoint
	LatencyP50               []TimeValuePoint
	LatencyP90               []TimeValuePoint
	LatencyP99               []TimeValuePoint
}

type httpMetricsBucket struct {
	requestCount  float64
	successCount  float64
	errorCount    float64
	durationSum   float64
	durationCount float64
}

// GetHTTPMetrics queries the enriched Hubble EMF log events via Logs Insights and
// reconstructs per-component HTTP RED time series. It reuses the same EMF fields
// and delta-sum aggregation as GetRuntimeTopology, but scopes to the destination
// component and buckets the result into a time series by step.
func (c *Client) GetHTTPMetrics(ctx context.Context, p HTTPMetricsQueryParams) (*HTTPMetricsResult, error) {
	if p.StartTime.IsZero() || p.EndTime.IsZero() {
		return nil, fmt.Errorf("startTime and endTime are required")
	}
	if !p.EndTime.After(p.StartTime) {
		return nil, fmt.Errorf("endTime (%s) must be after startTime (%s)", p.EndTime, p.StartTime)
	}
	query := BuildHTTPMetricsLogsInsightsQuery(p)
	_, binSeconds := logsInsightsBin(p.StepSeconds)
	rows, err := c.runLogsInsightsQuery(ctx, query, p.StartTime, p.EndTime)
	if err != nil {
		return nil, err
	}
	result := httpMetricsFromRows(rows, binSeconds)

	// Second query: per-step `le` bucket counts for latency percentiles. This is
	// best-effort — a failure leaves the RED series intact and percentiles empty.
	bucketQuery := BuildHTTPMetricsBucketLogsInsightsQuery(p)
	bucketRows, err := c.runLogsInsightsQuery(ctx, bucketQuery, p.StartTime, p.EndTime)
	if err != nil {
		// Percentiles are best-effort: keep the RED series and leave percentiles
		// empty, but log so a persistently failing bucket query is diagnosable.
		if c.logger != nil {
			c.logger.Warn("http-metrics percentile query failed; returning RED series without percentiles",
				"error", err)
		}
	} else {
		applyHTTPMetricsPercentiles(result, bucketRows)
	}
	return result, nil
}

// BuildHTTPMetricsBucketLogsInsightsQuery builds the per-step `le` bucket query
// used to reconstruct latency percentiles for a component, scoped to the same
// destination component as BuildHTTPMetricsLogsInsightsQuery.
func BuildHTTPMetricsBucketLogsInsightsQuery(p HTTPMetricsQueryParams) string {
	binExpr, _ := logsInsightsBin(p.StepSeconds)
	filters := []string{
		`reporter = "server"`,
		"ispresent(DestinationComponentUID)",
		"ispresent(le)",
	}
	if p.ComponentUID != "" {
		filters = append(filters, logsEquals("DestinationComponentUID", p.ComponentUID))
	}
	if p.ProjectUID != "" {
		filters = append(filters, logsEquals("DestinationProjectUID", p.ProjectUID))
	}
	if p.EnvironmentUID != "" {
		filters = append(filters, logsEquals("DestinationEnvironmentUID", p.EnvironmentUID))
	}
	return fmt.Sprintf(`fields le, %s
| filter %s
| stats sum(%s) as bucket_count by bin(%s) as ts, le
| sort ts asc`,
		fieldDurationBucket,
		strings.Join(filters, " and "),
		fieldDurationBucket, binExpr,
	)
}

// applyHTTPMetricsPercentiles reconstructs p50/p90/p99 per step bucket from the
// `le` bucket rows and attaches them to result.
func applyHTTPMetricsPercentiles(result *HTTPMetricsResult, rows []map[string]string) {
	// timestamp -> le -> summed count
	perBucket := map[time.Time]map[string]float64{}
	for _, row := range rows {
		ts, ok := parseLogsInsightsTime(row["ts"])
		if !ok {
			continue
		}
		le := row["le"]
		if le == "" {
			continue
		}
		count, ok := parseFloat(row["bucket_count"])
		if !ok {
			continue
		}
		leMap := perBucket[ts]
		if leMap == nil {
			leMap = map[string]float64{}
			perBucket[ts] = leMap
		}
		leMap[le] += count
	}

	timestamps := make([]time.Time, 0, len(perBucket))
	for ts := range perBucket {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i].Before(timestamps[j]) })

	for _, ts := range timestamps {
		p50, p90, p99 := percentilesFromLECounts(perBucket[ts])
		if p50 != nil {
			result.LatencyP50 = append(result.LatencyP50, TimeValuePoint{Timestamp: ts, Value: *p50})
		}
		if p90 != nil {
			result.LatencyP90 = append(result.LatencyP90, TimeValuePoint{Timestamp: ts, Value: *p90})
		}
		if p99 != nil {
			result.LatencyP99 = append(result.LatencyP99, TimeValuePoint{Timestamp: ts, Value: *p99})
		}
	}
}

// logsInsightsBin returns the Logs Insights bin() period string and its width in
// seconds for the requested step. Logs Insights does NOT honor a seconds unit in
// bin() — bin(300s) silently falls back to 1-minute buckets — so the period is
// expressed in whole minutes (or hours) and is never smaller than one minute.
// The returned width is used as the per-second rate divisor so it always matches
// the bucket the query actually produces.
func logsInsightsBin(stepSeconds int32) (string, float64) {
	minutes := (stepSeconds + 59) / 60
	if minutes < 1 {
		minutes = 1
	}
	width := float64(minutes) * 60
	if minutes%60 == 0 {
		return fmt.Sprintf("%dh", minutes/60), width
	}
	return fmt.Sprintf("%dm", minutes), width
}

// BuildHTTPMetricsLogsInsightsQuery builds the Logs Insights query used by
// GetHTTPMetrics. Exposed for tests because the field contract matters.
//
// Request events (hubble_http_requests_total, carrying `status`) and duration
// events (hubble_http_request_duration_seconds, carrying Count/Sum but no
// status) arrive as separate EMF records. Grouping by time bucket plus status
// keeps both; the adapter folds the per-status rows back together per bucket.
func BuildHTTPMetricsLogsInsightsQuery(p HTTPMetricsQueryParams) string {
	binExpr, _ := logsInsightsBin(p.StepSeconds)
	filters := []string{
		`reporter = "server"`,
		"ispresent(DestinationComponentUID)",
	}
	// The destination component UID is globally unique and is the real
	// discriminator, so it (optionally reinforced by project/environment UID)
	// scopes the query. We deliberately do NOT filter on Hubble's raw
	// `destination_namespace`: the request carries the OpenChoreo namespace
	// (e.g. "default") while that EMF field holds the dataplane k8s namespace
	// (e.g. "dp-default-default-development-<hash>"), so filtering it against the
	// request value matches nothing (see topology.go for the fuller rationale).
	if p.ComponentUID != "" {
		filters = append(filters, logsEquals("DestinationComponentUID", p.ComponentUID))
	}
	if p.ProjectUID != "" {
		filters = append(filters, logsEquals("DestinationProjectUID", p.ProjectUID))
	}
	if p.EnvironmentUID != "" {
		filters = append(filters, logsEquals("DestinationEnvironmentUID", p.EnvironmentUID))
	}
	return fmt.Sprintf(`fields status, %s, %s, %s
| filter %s
| stats sum(%s) as request_total,
        sum(%s) as duration_count,
        sum(%s) as duration_sum
  by bin(%s) as ts, status
| sort ts asc`,
		fieldRequestsTotal, fieldDurationCount, fieldDurationSum,
		strings.Join(filters, " and "),
		fieldRequestsTotal, fieldDurationCount, fieldDurationSum,
		binExpr,
	)
}

func httpMetricsFromRows(rows []map[string]string, binSeconds float64) *HTTPMetricsResult {
	if binSeconds < 1 {
		binSeconds = 1
	}
	buckets := map[time.Time]*httpMetricsBucket{}
	for _, row := range rows {
		ts, ok := parseLogsInsightsTime(row["ts"])
		if !ok {
			continue
		}
		b := buckets[ts]
		if b == nil {
			b = &httpMetricsBucket{}
			buckets[ts] = b
		}
		requests, _ := parseFloat(row["request_total"])
		b.requestCount += requests
		switch statusClass(row["status"]) {
		case statusSuccess:
			b.successCount += requests
		case statusError:
			b.errorCount += requests
		}
		if durationSum, ok := parseFloat(row["duration_sum"]); ok {
			b.durationSum += durationSum
		}
		if durationCount, ok := parseFloat(row["duration_count"]); ok {
			b.durationCount += durationCount
		}
	}

	timestamps := make([]time.Time, 0, len(buckets))
	for ts := range buckets {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i].Before(timestamps[j]) })

	result := &HTTPMetricsResult{}
	for _, ts := range timestamps {
		b := buckets[ts]
		result.RequestCount = append(result.RequestCount, TimeValuePoint{Timestamp: ts, Value: b.requestCount / binSeconds})
		result.SuccessfulRequestCount = append(result.SuccessfulRequestCount, TimeValuePoint{Timestamp: ts, Value: b.successCount / binSeconds})
		result.UnsuccessfulRequestCount = append(result.UnsuccessfulRequestCount, TimeValuePoint{Timestamp: ts, Value: b.errorCount / binSeconds})
		// Mean latency has no meaning without observed requests; skip empty
		// buckets rather than emit a misleading zero.
		if b.durationCount > 0 {
			result.MeanLatency = append(result.MeanLatency, TimeValuePoint{Timestamp: ts, Value: b.durationSum / b.durationCount})
		}
	}
	return result
}

type statusClassType int

const (
	statusOther statusClassType = iota
	statusSuccess
	statusError
)

// statusClass mirrors the Prometheus reference module: 1xx/2xx/3xx are
// successful, 4xx/5xx are unsuccessful. Duration records carry no status and
// classify as statusOther (their request contribution is zero regardless).
func statusClass(status string) statusClassType {
	status = strings.TrimSpace(status)
	if status == "" {
		return statusOther
	}
	switch status[0] {
	case '1', '2', '3':
		return statusSuccess
	case '4', '5':
		return statusError
	default:
		return statusOther
	}
}

// parseLogsInsightsTime parses the timestamp emitted by a Logs Insights
// `bin()` aggregation. CloudWatch renders it in UTC as "2006-01-02 15:04:05.000";
// epoch-millisecond and RFC3339 forms are accepted defensively.
func parseLogsInsightsTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), true
		}
	}
	if ms, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), true
	}
	return time.Time{}, false
}
