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
// A release line matches on its UID alone, since its pods don't carry the
// component UID. Without one, the component UID matches its main line. The
// filter language has no negation outside group definitions, so the main line
// cannot also exclude series with a release line label: it relies on release
// line pods not carrying the component UID label. AllReleaseLines is left to
// BuildResourceMetricsFilters.
func BuildResourceMetricsFilter(spec metricSpec, p MetricsQueryParams) string {
	return resourceMetricsFilter(spec, p, lineScopeClause(p))
}

// BuildResourceMetricsFilters renders the filters whose series add up to the
// query's scope. A release line's pods carry the component UID under another
// label than the main line's, so every line of a component takes one filter
// per label. Any other scope takes BuildResourceMetricsFilter's single filter.
func BuildResourceMetricsFilters(spec metricSpec, p MetricsQueryParams) []string {
	componentUID := normalizeUID(p.ComponentUID)
	if !p.AllReleaseLines || componentUID == "" || normalizeUID(p.ReleaseLineUID) != "" {
		return []string{BuildResourceMetricsFilter(spec, p)}
	}
	return []string{
		resourceMetricsFilter(spec, p, metadataLabelClause(labelComponentUID, componentUID)),
		resourceMetricsFilter(spec, p, metadataLabelClause(labelReleaseLineComponentUID, componentUID)),
	}
}

// ReleaseLineApplied reports whether the scope's filters match it by release
// line: one release line, or every line of a component. A component's main
// line alone is not, since its filter cannot exclude release line series.
func ReleaseLineApplied(p MetricsQueryParams) bool {
	if normalizeUID(p.ReleaseLineUID) != "" {
		return true
	}
	return p.AllReleaseLines && normalizeUID(p.ComponentUID) != ""
}

// lineScopeClause matches the scope's release line, or else its component.
// Empty without either.
func lineScopeClause(p MetricsQueryParams) string {
	if uid := normalizeUID(p.ReleaseLineUID); uid != "" {
		return metadataLabelClause(labelReleaseLineUID, uid)
	}
	if uid := normalizeUID(p.ComponentUID); uid != "" {
		return metadataLabelClause(labelComponentUID, uid)
	}
	return ""
}

func resourceMetricsFilter(spec metricSpec, p MetricsQueryParams, lineClause string) string {
	clauses := []string{
		fmt.Sprintf("metric.type = %s", quote(spec.metricType)),
		`resource.type = "k8s_container"`,
	}
	if spec.extraFilter != "" {
		clauses = append(clauses, spec.extraFilter)
	}
	if lineClause != "" {
		clauses = append(clauses, lineClause)
	}
	if uid := normalizeUID(p.ProjectUID); uid != "" {
		clauses = append(clauses, metadataLabelClause(labelProjectUID, uid))
	}
	if uid := normalizeUID(p.EnvironmentUID); uid != "" {
		clauses = append(clauses, metadataLabelClause(labelEnvironmentUID, uid))
	}
	return strings.Join(clauses, " AND ")
}

// buildListRequests assembles the full ListTimeSeries requests for one spec,
// one per filter of the scope: the identity-scoped filter plus an aggregation
// that aligns each container series to the step and sums across containers,
// yielding a single series per request.
func buildListRequests(projectID string, spec metricSpec, p MetricsQueryParams) []*monitoringpb.ListTimeSeriesRequest {
	filters := BuildResourceMetricsFilters(spec, p)
	reqs := make([]*monitoringpb.ListTimeSeriesRequest, 0, len(filters))
	for _, filter := range filters {
		reqs = append(reqs, &monitoringpb.ListTimeSeriesRequest{
			Name:   "projects/" + projectID,
			Filter: filter,
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
		})
	}
	return reqs
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
