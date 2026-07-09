// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// metricSpec describes how one of the six resource series maps onto a GKE
// system metric.
type metricSpec struct {
	// key identifies the series in ResourceMetricsResult.
	key string

	// metricType is the Cloud Monitoring metric descriptor.
	metricType string

	// extraFilter narrows the series by metric labels (e.g. memory_type).
	extraFilter string

	// aligner turns raw points into per-alignment-period samples. Cumulative
	// counters use ALIGN_RATE (yielding cores for cpu/core_usage_time);
	// gauges use ALIGN_MEAN.
	aligner monitoringpb.Aggregation_Aligner
}

// resourceMetricSpecs lists the GKE system metrics backing the resource
// series. All are resource.type k8s_container and are populated by GKE's
// built-in metrics agent — nothing extra has to be deployed in the cluster.
//
// memory/used_bytes carries a memory_type label; "non-evictable" approximates
// the working set (matching container_memory_working_set_bytes elsewhere).
var resourceMetricSpecs = []metricSpec{
	{key: "cpuUsage", metricType: "kubernetes.io/container/cpu/core_usage_time", aligner: monitoringpb.Aggregation_ALIGN_RATE},
	{key: "cpuRequests", metricType: "kubernetes.io/container/cpu/request_cores", aligner: monitoringpb.Aggregation_ALIGN_MEAN},
	{key: "cpuLimits", metricType: "kubernetes.io/container/cpu/limit_cores", aligner: monitoringpb.Aggregation_ALIGN_MEAN},
	{key: "memoryUsage", metricType: "kubernetes.io/container/memory/used_bytes", extraFilter: `metric.labels.memory_type = "non-evictable"`, aligner: monitoringpb.Aggregation_ALIGN_MEAN},
	{key: "memoryRequests", metricType: "kubernetes.io/container/memory/request_bytes", aligner: monitoringpb.Aggregation_ALIGN_MEAN},
	{key: "memoryLimits", metricType: "kubernetes.io/container/memory/limit_bytes", aligner: monitoringpb.Aggregation_ALIGN_MEAN},
}

// minAlignmentPeriod is the smallest alignment period Cloud Monitoring
// accepts; smaller steps are clamped up.
const minAlignmentPeriod = time.Minute

// BuildResourceMetricsFilter renders the ListTimeSeries filter for one metric
// spec scoped to the OpenChoreo identity labels. Scoping rides on
// metadata.user_labels, which Cloud Monitoring populates from the pod labels
// of k8s_container resources.
//
// Scoping is by the three UID labels (component/project/environment) ONLY —
// mirroring the Prometheus sibling's BuildComponentLabelFilter. The rule's
// `namespace` is deliberately NOT a metric filter: the control plane sends the
// data-plane runtime namespace (e.g. "dp-default-<project>-<env>-...") as the
// rule namespace, whereas the pod's `openchoreo.dev/namespace` metadata label
// carries the *control-plane* namespace (e.g. "default"), so filtering on it
// matches zero series. Namespace is retained only as policy identity/dedup
// metadata (see policyUserLabels), never in the metric-matching filter.
func BuildResourceMetricsFilter(spec metricSpec, p MetricsQueryParams) string {
	clauses := []string{
		fmt.Sprintf("metric.type = %s", quote(spec.metricType)),
		`resource.type = "k8s_container"`,
	}
	if spec.extraFilter != "" {
		clauses = append(clauses, spec.extraFilter)
	}
	if uid := normalizeUID(p.ComponentUID); uid != "" {
		clauses = append(clauses, metadataLabelClause(labelComponentUID, uid))
	}
	if uid := normalizeUID(p.ProjectUID); uid != "" {
		clauses = append(clauses, metadataLabelClause(labelProjectUID, uid))
	}
	if uid := normalizeUID(p.EnvironmentUID); uid != "" {
		clauses = append(clauses, metadataLabelClause(labelEnvironmentUID, uid))
	}
	return strings.Join(clauses, " AND ")
}

// buildListRequest assembles the full ListTimeSeries request for one spec:
// the identity-scoped filter plus an aggregation that aligns each container
// series to the step and sums across containers, yielding a single series.
func buildListRequest(projectID string, spec metricSpec, p MetricsQueryParams) *monitoringpb.ListTimeSeriesRequest {
	return &monitoringpb.ListTimeSeriesRequest{
		Name:   "projects/" + projectID,
		Filter: BuildResourceMetricsFilter(spec, p),
		Interval: &monitoringpb.TimeInterval{
			StartTime: timestamppb.New(p.StartTime),
			EndTime:   timestamppb.New(p.EndTime),
		},
		Aggregation: &monitoringpb.Aggregation{
			AlignmentPeriod:    durationpb.New(alignmentPeriod(p.Step)),
			PerSeriesAligner:   spec.aligner,
			CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_SUM,
		},
		View: monitoringpb.ListTimeSeriesRequest_FULL,
	}
}

// buildPingRequest is a minimal credentials/reachability probe: one header-only
// page of container CPU series over the last hour.
func buildPingRequest(projectID string, now time.Time) *monitoringpb.ListTimeSeriesRequest {
	return &monitoringpb.ListTimeSeriesRequest{
		Name:   "projects/" + projectID,
		Filter: `metric.type = "kubernetes.io/container/cpu/core_usage_time" AND resource.type = "k8s_container"`,
		Interval: &monitoringpb.TimeInterval{
			StartTime: timestamppb.New(now.Add(-time.Hour)),
			EndTime:   timestamppb.New(now),
		},
		View:     monitoringpb.ListTimeSeriesRequest_HEADERS,
		PageSize: 1,
	}
}

func alignmentPeriod(step time.Duration) time.Duration {
	if step < minAlignmentPeriod {
		return minAlignmentPeriod
	}
	return step
}

func metadataLabelClause(key, value string) string {
	return fmt.Sprintf("metadata.user_labels.%s = %s", quote(key), quote(value))
}

// quote renders a double-quoted monitoring-filter string literal, escaping
// backslashes and quotes and stripping newlines so caller-supplied values
// cannot break out of the literal.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
