// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EMF field names emitted by the Hubble ingest pipeline. The OpenTelemetry
// prometheus receiver folds `hubble_http_request_duration_seconds{_sum,_count,
// _bucket}` into a single histogram metric, and the awsemf exporter writes it
// as a nested object carrying only Count/Sum (explicit-bucket detail and the
// `le` label are NOT preserved). `hubble_http_requests_total` keeps the `status`
// label. The pipeline applies cumulativetodelta, so every emitted value is a
// per-interval delta and the adapter aggregates with sum() over the window.
const (
	fieldRequestsTotal = "hubble_http_requests_total"
	fieldDurationCount = "`hubble_http_request_duration_seconds.Count`"
	fieldDurationSum   = "`hubble_http_request_duration_seconds.Sum`"
	// fieldDurationBucket is the renamed classic-histogram bucket series the
	// collector emits (with an `le` field) so the adapter can reconstruct
	// latency percentiles the awsemf histogram path would otherwise drop.
	fieldDurationBucket = "hubble_http_request_duration_seconds_lebucket"
)

// percentileQuantiles are the latency quantiles reported for HTTP metrics and
// runtime-topology edges, matching the Prometheus reference module.
var percentileQuantiles = struct{ p50, p90, p99 float64 }{0.50, 0.90, 0.99}

// RuntimeTopologyQueryParams captures the project/environment scoped topology query.
type RuntimeTopologyQueryParams struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
	StartTime      time.Time
	EndTime        time.Time
}

// RuntimeTopologyResult is the CloudWatch-native representation of runtime topology edges.
type RuntimeTopologyResult struct {
	Edges []RuntimeTopologyEdgeResult
}

// RuntimeTopologyEdgeResult carries aggregate RED metrics for a component-to-component edge.
//
// Request count, error count, and mean latency come from the primary EMF query.
// Latency percentiles are reconstructed from a dedicated `le`-bucket series the
// collector preserves (the awsemf histogram path would otherwise collapse the
// histogram to Count/Sum); they are best-effort and nil when unavailable.
type RuntimeTopologyEdgeResult struct {
	SourceComponentUID       string
	SourceComponentName      string
	SourceNamespace          string
	DestinationComponentUID  string
	DestinationComponentName string
	DestinationNamespace     string
	RequestCount             float64
	ErrorCount               float64
	MeanLatency              float64
	// Latency percentiles reconstructed from the preserved `le` buckets. Nil
	// when the edge has no duration samples in the window.
	LatencyP50 *float64
	LatencyP90 *float64
	LatencyP99 *float64
}

type topologyEdgeKey struct {
	srcUID string
	dstUID string
}

type topologyAccumulator struct {
	sourceName           string
	sourceNamespace      string
	destinationName      string
	destinationNamespace string
	requestCount         float64
	errorCount           float64
	durationSum          float64
	durationCount        float64
}

// GetRuntimeTopology queries enriched Hubble EMF log events via Logs Insights and
// reconstructs per-edge RED metrics. The collector enriches both the source and
// destination pod with OpenChoreo component identity and emits per-interval delta
// values, which this method aggregates over the requested window.
func (c *Client) GetRuntimeTopology(ctx context.Context, p RuntimeTopologyQueryParams) (*RuntimeTopologyResult, error) {
	if p.StartTime.IsZero() || p.EndTime.IsZero() {
		return nil, fmt.Errorf("startTime and endTime are required")
	}
	if !p.EndTime.After(p.StartTime) {
		return nil, fmt.Errorf("endTime (%s) must be after startTime (%s)", p.EndTime, p.StartTime)
	}
	query := BuildRuntimeTopologyLogsInsightsQuery(p)
	rows, err := c.runLogsInsightsQuery(ctx, query, p.StartTime, p.EndTime)
	if err != nil {
		return nil, err
	}
	result := runtimeTopologyFromRows(rows, p.ComponentUID)

	// Second query: per-edge `le` bucket counts, used to reconstruct latency
	// percentiles. Failure here must not fail the whole topology response — the
	// RED metrics above are still valid — so percentiles are best-effort.
	bucketQuery := BuildRuntimeTopologyBucketLogsInsightsQuery(p)
	bucketRows, err := c.runLogsInsightsQuery(ctx, bucketQuery, p.StartTime, p.EndTime)
	if err != nil {
		// Percentiles are best-effort: keep the RED metrics and leave percentiles
		// empty, but log so a persistently failing bucket query is diagnosable.
		if c.logger != nil {
			c.logger.Warn("runtime-topology percentile query failed; returning RED metrics without percentiles",
				"error", err)
		}
	} else {
		applyTopologyPercentiles(result, bucketRows, p.ComponentUID)
	}
	return result, nil
}

// BuildRuntimeTopologyBucketLogsInsightsQuery builds the Logs Insights query that
// returns per-edge cumulative `le` bucket counts over the window. The collector
// emits these as a dedicated series so the histogram buckets survive the awsemf
// path (which collapses assembled histograms to Count/Sum).
func BuildRuntimeTopologyBucketLogsInsightsQuery(p RuntimeTopologyQueryParams) string {
	filters := []string{
		`reporter = "server"`,
		logsEquals("SourceProjectUID", p.ProjectUID),
		logsEquals("DestinationProjectUID", p.ProjectUID),
		logsEquals("SourceEnvironmentUID", p.EnvironmentUID),
		logsEquals("DestinationEnvironmentUID", p.EnvironmentUID),
		"ispresent(SourceComponentUID)",
		"ispresent(DestinationComponentUID)",
		"ispresent(le)",
	}
	return fmt.Sprintf(`fields SourceComponentUID, DestinationComponentUID, le, %s
| filter %s
| stats sum(%s) as bucket_count
  by SourceComponentUID, DestinationComponentUID, le`,
		fieldDurationBucket,
		strings.Join(filters, " and "),
		fieldDurationBucket,
	)
}

// applyTopologyPercentiles reconstructs p50/p90/p99 per edge from the `le` bucket
// rows and attaches them to the matching edges in result.
func applyTopologyPercentiles(result *RuntimeTopologyResult, rows []map[string]string, componentUIDFilter string) {
	// edge key -> le -> summed count
	perEdge := map[topologyEdgeKey]map[string]float64{}
	for _, row := range rows {
		srcUID := firstNonEmpty(row["SourceComponentUID"], row["src_component_uid"])
		dstUID := firstNonEmpty(row["DestinationComponentUID"], row["dst_component_uid"])
		le := row["le"]
		if srcUID == "" || dstUID == "" || le == "" {
			continue
		}
		if componentUIDFilter != "" && srcUID != componentUIDFilter && dstUID != componentUIDFilter {
			continue
		}
		count, ok := parseFloat(row["bucket_count"])
		if !ok {
			continue
		}
		key := topologyEdgeKey{srcUID: srcUID, dstUID: dstUID}
		leMap := perEdge[key]
		if leMap == nil {
			leMap = map[string]float64{}
			perEdge[key] = leMap
		}
		leMap[le] += count
	}

	for i := range result.Edges {
		key := topologyEdgeKey{srcUID: result.Edges[i].SourceComponentUID, dstUID: result.Edges[i].DestinationComponentUID}
		leMap, ok := perEdge[key]
		if !ok {
			continue
		}
		p50, p90, p99 := percentilesFromLECounts(leMap)
		result.Edges[i].LatencyP50 = p50
		result.Edges[i].LatencyP90 = p90
		result.Edges[i].LatencyP99 = p99
	}
}

// percentilesFromLECounts reconstructs the p50/p90/p99 latency (seconds) from a
// map of `le` bound to cumulative count. Any percentile that cannot be computed
// (no data) is returned as nil.
func percentilesFromLECounts(leCounts map[string]float64) (p50, p90, p99 *float64) {
	buckets := bucketsFromLECounts(leCounts)
	if len(buckets) == 0 {
		return nil, nil, nil
	}
	compute := func(q float64) *float64 {
		v, ok := histogramQuantile(q, buckets)
		if !ok || math.IsInf(v, 0) || math.IsNaN(v) {
			return nil
		}
		out := v
		return &out
	}
	return compute(percentileQuantiles.p50), compute(percentileQuantiles.p90), compute(percentileQuantiles.p99)
}

// BuildRuntimeTopologyLogsInsightsQuery builds the Logs Insights query used by
// GetRuntimeTopology. Exposed for tests because the field contract is important.
//
// Request events (hubble_http_requests_total) and duration events
// (hubble_http_request_duration_seconds) arrive as separate EMF records: the
// former carries `status` but no duration, the latter carries Count/Sum but no
// status. Grouping by the edge identity plus status keeps both, and the adapter
// folds the per-status rows back into a single edge.
func BuildRuntimeTopologyLogsInsightsQuery(p RuntimeTopologyQueryParams) string {
	// Isolation is by project + environment UID (both required by the contract),
	// which are globally unique and uniquely scope a project/environment to a
	// single dataplane namespace. We deliberately do NOT filter on Hubble's raw
	// `source_namespace`/`destination_namespace`: the request carries the
	// OpenChoreo namespace (e.g. "default"), whereas those EMF fields hold the
	// dataplane k8s namespace (e.g. "dp-default-default-development-<hash>"), so
	// filtering them against the request value matches nothing. The Prometheus
	// reference module filters on the enriched `openchoreo.dev/namespace` label
	// instead; the AWS EMF has no such enriched field, so the UID filters (the
	// same discriminators Prometheus keys on) carry the scoping. The raw
	// namespace fields are still selected below, for edge display only.
	filters := []string{
		`reporter = "server"`,
		logsEquals("SourceProjectUID", p.ProjectUID),
		logsEquals("DestinationProjectUID", p.ProjectUID),
		logsEquals("SourceEnvironmentUID", p.EnvironmentUID),
		logsEquals("DestinationEnvironmentUID", p.EnvironmentUID),
		"ispresent(SourceComponentUID)",
		"ispresent(DestinationComponentUID)",
	}
	return fmt.Sprintf(`fields SourceComponentUID, SourceComponent, source_namespace,
       DestinationComponentUID, DestinationComponent, destination_namespace,
       status, %s, %s, %s
| filter %s
| stats sum(%s) as request_total,
        sum(%s) as duration_count,
        sum(%s) as duration_sum
  by SourceComponentUID, SourceComponent, source_namespace,
     DestinationComponentUID, DestinationComponent, destination_namespace, status`,
		fieldRequestsTotal, fieldDurationCount, fieldDurationSum,
		strings.Join(filters, " and "),
		fieldRequestsTotal, fieldDurationCount, fieldDurationSum,
	)
}

func runtimeTopologyFromRows(rows []map[string]string, componentUIDFilter string) *RuntimeTopologyResult {
	acc := map[topologyEdgeKey]*topologyAccumulator{}
	for _, row := range rows {
		srcUID := firstNonEmpty(row["SourceComponentUID"], row["src_component_uid"])
		dstUID := firstNonEmpty(row["DestinationComponentUID"], row["dst_component_uid"])
		if srcUID == "" || dstUID == "" {
			continue
		}
		if componentUIDFilter != "" && srcUID != componentUIDFilter && dstUID != componentUIDFilter {
			continue
		}
		key := topologyEdgeKey{srcUID: srcUID, dstUID: dstUID}
		edge := acc[key]
		if edge == nil {
			edge = &topologyAccumulator{}
			acc[key] = edge
		}
		edge.sourceName = firstNonEmptyTopologyField(edge.sourceName, row["SourceComponent"], row["src_component"])
		edge.sourceNamespace = firstNonEmptyTopologyField(edge.sourceNamespace, row["source_namespace"], row["SourceNamespace"])
		edge.destinationName = firstNonEmptyTopologyField(edge.destinationName, row["DestinationComponent"], row["dst_component"])
		edge.destinationNamespace = firstNonEmptyTopologyField(edge.destinationNamespace, row["destination_namespace"], row["DestinationNamespace"])

		requests, _ := parseFloat(row["request_total"])
		edge.requestCount += requests
		if isErrorStatus(row["status"]) {
			edge.errorCount += requests
		}
		if durationSum, ok := parseFloat(row["duration_sum"]); ok {
			edge.durationSum += durationSum
		}
		if durationCount, ok := parseFloat(row["duration_count"]); ok {
			edge.durationCount += durationCount
		}
	}

	edges := make([]RuntimeTopologyEdgeResult, 0, len(acc))
	for key, edge := range acc {
		if edge.requestCount <= 0 {
			continue
		}
		result := RuntimeTopologyEdgeResult{
			SourceComponentUID:       key.srcUID,
			SourceComponentName:      edge.sourceName,
			SourceNamespace:          edge.sourceNamespace,
			DestinationComponentUID:  key.dstUID,
			DestinationComponentName: edge.destinationName,
			DestinationNamespace:     edge.destinationNamespace,
			RequestCount:             edge.requestCount,
			ErrorCount:               edge.errorCount,
		}
		if edge.durationCount > 0 {
			result.MeanLatency = edge.durationSum / edge.durationCount
		}
		edges = append(edges, result)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].SourceComponentUID == edges[j].SourceComponentUID {
			return edges[i].DestinationComponentUID < edges[j].DestinationComponentUID
		}
		return edges[i].SourceComponentUID < edges[j].SourceComponentUID
	})
	return &RuntimeTopologyResult{Edges: edges}
}

func parseFloat(value string) (float64, bool) {
	if strings.TrimSpace(value) == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) {
		return 0, false
	}
	return parsed, true
}

func isErrorStatus(status string) bool {
	status = strings.TrimSpace(status)
	return strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5")
}

func firstNonEmptyTopologyField(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func escapeLogsString(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

func logsEquals(field, value string) string {
	return fmt.Sprintf(`%s = "%s"`, field, escapeLogsString(value))
}
