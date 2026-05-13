// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"fmt"
	"strings"
)

// Kubernetes label constants used for Prometheus metric label filtering.
const (
	LabelComponentUID   = "openchoreo.dev/component-uid"
	LabelProjectUID     = "openchoreo.dev/project-uid"
	LabelEnvironmentUID = "openchoreo.dev/environment-uid"
	LabelNamespace      = "openchoreo.dev/namespace"
	LabelComponent      = "openchoreo.dev/component"
	LabelProject        = "openchoreo.dev/project"
	LabelEnvironment    = "openchoreo.dev/environment"
)

// prometheusLabelName converts a Kubernetes label name to a Prometheus metric label name.
// e.g., "openchoreo.dev/component-uid" becomes "label_openchoreo_dev_component_uid"
func prometheusLabelName(kubernetesLabel string) string {
	label := strings.ReplaceAll(kubernetesLabel, "-", "_")
	label = strings.ReplaceAll(label, ".", "_")
	label = strings.ReplaceAll(label, "/", "_")
	return "label_" + label
}

// BuildLabelFilter builds a Prometheus label filter string for component identification.
// If any of the IDs are empty, they are not included in the filter.
// The namespace is always required and included.
func BuildLabelFilter(namespace, componentUID, projectUID, environmentUID string) string {
	namespaceLabel := prometheusLabelName(LabelNamespace)
	componentLabel := prometheusLabelName(LabelComponentUID)
	projectLabel := prometheusLabelName(LabelProjectUID)
	environmentLabel := prometheusLabelName(LabelEnvironmentUID)

	labelFilter := fmt.Sprintf("%s=%q", namespaceLabel, namespace)
	if componentUID != "" {
		labelFilter = fmt.Sprintf("%s,%s=%q", labelFilter, componentLabel, componentUID)
	}
	if projectUID != "" {
		labelFilter = fmt.Sprintf("%s,%s=%q", labelFilter, projectLabel, projectUID)
	}
	if environmentUID != "" {
		labelFilter = fmt.Sprintf("%s,%s=%q", labelFilter, environmentLabel, environmentUID)
	}
	return labelFilter
}

// BuildScopeLabelNames returns the Prometheus label names for whichever of
// componentUID, projectUID, and environmentUID are non-empty.
func BuildScopeLabelNames(componentUID, projectUID, environmentUID string) []string {
	scopeLabels := make([]string, 0, 3)
	if componentUID != "" {
		scopeLabels = append(scopeLabels, prometheusLabelName(LabelComponentUID))
	}
	if projectUID != "" {
		scopeLabels = append(scopeLabels, prometheusLabelName(LabelProjectUID))
	}
	if environmentUID != "" {
		scopeLabels = append(scopeLabels, prometheusLabelName(LabelEnvironmentUID))
	}
	return scopeLabels
}

// BuildSumByClause builds the label list for a PromQL "sum by (...)" clause.
func BuildSumByClause(metricLabel string, scopeLabels []string) string {
	sumByLabels := make([]string, 0, len(scopeLabels)+1)
	sumByLabels = append(sumByLabels, scopeLabels...)
	if metricLabel != "" {
		sumByLabels = append(sumByLabels, metricLabel)
	}
	return strings.Join(sumByLabels, ", ")
}

// BuildHistogramSumByClause builds the label list for histogram "sum by (..., le)" clauses.
func BuildHistogramSumByClause(sumByClause string) string {
	if strings.TrimSpace(sumByClause) == "" {
		return "le"
	}
	return fmt.Sprintf("%s, le", sumByClause)
}

// BuildGroupLeftClause builds a PromQL group_left clause that propagates the
// given scope labels from the right-hand side of a join.
func BuildGroupLeftClause(scopeLabels []string) string {
	if len(scopeLabels) == 0 {
		return "group_left"
	}
	return fmt.Sprintf("group_left (%s)", strings.Join(scopeLabels, ", "))
}

// BuildComponentLabelFilter builds a label filter using component/project/environment UIDs
// without namespace. Used for alert rule expressions.
func BuildComponentLabelFilter(componentUID, projectUID, environmentUID string) string {
	componentLabel := prometheusLabelName(LabelComponentUID)
	projectLabel := prometheusLabelName(LabelProjectUID)
	environmentLabel := prometheusLabelName(LabelEnvironmentUID)

	return fmt.Sprintf(`%s=%q,%s=%q,%s=%q`,
		componentLabel, componentUID, projectLabel, projectUID, environmentLabel, environmentUID)
}

// ----------------------------
// Resource Metrics Queries
// ----------------------------

// BuildCPUUsageQuery builds a PromQL query for CPU usage rate.
func BuildCPUUsageQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
    rate(container_cpu_usage_seconds_total{container!=""}[2m]) * on (pod, namespace) %s kube_pod_labels{job="kube-state-metrics",%s} )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildCPURequestsQuery builds a PromQL query for CPU requests.
func BuildCPURequestsQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_requests{resource="cpu",job="kube-state-metrics"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            kube_pod_labels{job="kube-state-metrics",%s}
        )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildCPULimitsQuery builds a PromQL query for CPU limits.
func BuildCPULimitsQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_limits{resource="cpu",job="kube-state-metrics"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            kube_pod_labels{job="kube-state-metrics",%s}
        )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildMemoryUsageQuery builds a PromQL query for memory usage.
func BuildMemoryUsageQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
              container_memory_working_set_bytes{container!=""}
              * on (pod, namespace) %s
                kube_pod_labels{job="kube-state-metrics",%s}
            )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildMemoryRequestsQuery builds a PromQL query for memory requests.
func BuildMemoryRequestsQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_requests{resource="memory",job="kube-state-metrics"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            kube_pod_labels{job="kube-state-metrics",%s}
        )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildMemoryLimitsQuery builds a PromQL query for memory limits.
func BuildMemoryLimitsQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_limits{resource="memory",job="kube-state-metrics"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            kube_pod_labels{job="kube-state-metrics",%s}
        )`, sumByClause, groupLeftClause, labelFilter)
}

// ----------------------------
// HTTP Request Metrics Queries
// ----------------------------

// buildHubblePodMappingExpr returns a PromQL expression that exposes
// kube_pod_labels (filtered by labelFilter) under Hubble's join keys
// (destination_namespace, destination_pod).
func buildHubblePodMappingExpr(labelFilter string) string {
	return fmt.Sprintf(`label_replace(
            label_replace(
                kube_pod_labels{job="kube-state-metrics",%s},
                "destination_namespace", "$1", "namespace", "(.*)"
            ),
            "destination_pod", "$1", "pod", "(.*)"
        )`, labelFilter)
}

// buildHTTPRequestCountQueryWithStatus builds the request-count PromQL,
// optionally filtered by an extra `status=~...` matcher.
func buildHTTPRequestCountQueryWithStatus(labelFilter, sumByClause, groupLeftClause, statusMatcher string) string {
	mapping := buildHubblePodMappingExpr(labelFilter)
	return fmt.Sprintf(`
    sum by (%s) (
        rate(hubble_http_requests_total{reporter="server"%s}[2m])
            * on(destination_namespace, destination_pod) %s
            %s
    )
    >= 0
`, sumByClause, statusMatcher, groupLeftClause, mapping)
}

// BuildHTTPRequestCountQuery builds a PromQL query for HTTP request count.
func BuildHTTPRequestCountQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestCountQueryWithStatus(labelFilter, sumByClause, groupLeftClause, "")
}

// BuildSuccessfulHTTPRequestCountQuery builds a PromQL query for successful HTTP request count
// (1xx, 2xx, 3xx).
func BuildSuccessfulHTTPRequestCountQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestCountQueryWithStatus(labelFilter, sumByClause, groupLeftClause, `, status=~"^[123]..?$"`)
}

// BuildUnsuccessfulHTTPRequestCountQuery builds a PromQL query for unsuccessful HTTP request
// count (4xx, 5xx).
func BuildUnsuccessfulHTTPRequestCountQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestCountQueryWithStatus(labelFilter, sumByClause, groupLeftClause, `, status=~"^[45]..?$"`)
}

// BuildMeanHTTPRequestLatencyQuery builds a PromQL query for mean HTTP request latency.
func BuildMeanHTTPRequestLatencyQuery(labelFilter, sumByClause, groupLeftClause string) string {
	mapping := buildHubblePodMappingExpr(labelFilter)
	return fmt.Sprintf(`
    (
        sum by (%s) (
            rate(hubble_http_request_duration_seconds_sum{reporter="server"}[2m])
                * on(destination_namespace, destination_pod) %s
                %s
        )
        /
        sum by (%s) (
            rate(hubble_http_requests_total{reporter="server"}[2m])
                * on(destination_namespace, destination_pod) %s
                %s
        )
    )
    >= 0
`, sumByClause, groupLeftClause, mapping, sumByClause, groupLeftClause, mapping)
}

// buildHTTPRequestLatencyPercentileQuery builds a histogram_quantile PromQL
// expression for the given quantile (e.g. "0.5", "0.9", "0.99").
func buildHTTPRequestLatencyPercentileQuery(quantile, labelFilter, sumByClause, groupLeftClause string) string {
	mapping := buildHubblePodMappingExpr(labelFilter)
	return fmt.Sprintf(`
    histogram_quantile(
        %s,
        sum by (%s) (
            rate(hubble_http_request_duration_seconds_bucket{reporter="server"}[2m])
                * on(destination_namespace, destination_pod) %s
                %s
        )
    )
    >= 0
`, quantile, BuildHistogramSumByClause(sumByClause), groupLeftClause, mapping)
}

// Build50thPercentileHTTPRequestLatencyQuery builds a PromQL query for 50th percentile HTTP request latency.
func Build50thPercentileHTTPRequestLatencyQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestLatencyPercentileQuery("0.5", labelFilter, sumByClause, groupLeftClause)
}

// Build90thPercentileHTTPRequestLatencyQuery builds a PromQL query for 90th percentile HTTP request latency.
func Build90thPercentileHTTPRequestLatencyQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestLatencyPercentileQuery("0.9", labelFilter, sumByClause, groupLeftClause)
}

// Build99thPercentileHTTPRequestLatencyQuery builds a PromQL query for 99th percentile HTTP request latency.
func Build99thPercentileHTTPRequestLatencyQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestLatencyPercentileQuery("0.99", labelFilter, sumByClause, groupLeftClause)
}

// ----------------------------
// Runtime topology queries
// ----------------------------

// Output label names used by runtime topology queries to carry the resolved
// OpenChoreo component names and UIDs after the kube_pod_labels join.
const (
	RuntimeTopologySrcComponentNameLabel = "src_component"
	RuntimeTopologyDstComponentNameLabel = "dst_component"
	RuntimeTopologySrcComponentUIDLabel  = "src_component_uid"
	RuntimeTopologyDstComponentUIDLabel  = "dst_component_uid"
)

// BuildRuntimeTopologyLabelFilter builds a Prometheus label filter for runtime
// topology queries using UID labels (same convention as BuildLabelFilter).
func BuildRuntimeTopologyLabelFilter(namespace, componentUID, projectUID, environmentUID string) string {
	namespaceLabel := prometheusLabelName(LabelNamespace)
	componentLabel := prometheusLabelName(LabelComponentUID)
	projectLabel := prometheusLabelName(LabelProjectUID)
	environmentLabel := prometheusLabelName(LabelEnvironmentUID)

	filter := fmt.Sprintf("%s=%q", namespaceLabel, namespace)
	if componentUID != "" {
		filter = fmt.Sprintf("%s,%s=%q", filter, componentLabel, componentUID)
	}
	if projectUID != "" {
		filter = fmt.Sprintf("%s,%s=%q", filter, projectLabel, projectUID)
	}
	if environmentUID != "" {
		filter = fmt.Sprintf("%s,%s=%q", filter, environmentLabel, environmentUID)
	}
	return filter
}

// buildHubblePodMappingExprWithSide returns a PromQL expression that joins
// kube_pod_labels (filtered by labelFilter) onto Hubble's per-side keys.
// `side` is either "source" or "destination". The resulting series carries
// both a name label (`outputNameLabel`) and a UID label (`outputUIDLabel`)
// for the OpenChoreo component.
func buildHubblePodMappingExprWithSide(side, outputNameLabel, outputUIDLabel, labelFilter string) string {
	nsLabel := side + "_namespace"
	podLabel := side + "_pod"
	componentNameLabel := prometheusLabelName(LabelComponent)
	componentUIDLabel := prometheusLabelName(LabelComponentUID)
	return fmt.Sprintf(`label_replace(
            label_replace(
                label_replace(
                    label_replace(
                        kube_pod_labels{job="kube-state-metrics",%s},
                        "%s", "$1", "namespace", "(.*)"
                    ),
                    "%s", "$1", "pod", "(.*)"
                ),
                "%s", "$1", "%s", "(.*)"
            ),
            "%s", "$1", "%s", "(.*)"
        )`, labelFilter, nsLabel, podLabel, outputNameLabel, componentNameLabel, outputUIDLabel, componentUIDLabel)
}

// buildRuntimeTopologyIncreaseQuery builds a PromQL sum-by expression that
// aggregates the total increase of a Hubble metric over the given duration by
// (src_component, dst_component, src_component_uid, dst_component_uid).
// extraMatcher is an optional additional Hubble label selector.
// duration must be a valid PromQL duration string (e.g. "3600s").
func buildRuntimeTopologyIncreaseQuery(metric, extraMatcher, labelFilter, duration string) string {
	src := RuntimeTopologySrcComponentNameLabel
	dst := RuntimeTopologyDstComponentNameLabel
	srcUID := RuntimeTopologySrcComponentUIDLabel
	dstUID := RuntimeTopologyDstComponentUIDLabel
	srcMapping := buildHubblePodMappingExprWithSide("source", src, srcUID, labelFilter)
	dstMapping := buildHubblePodMappingExprWithSide("destination", dst, dstUID, labelFilter)

	// Join dst side first (server reporter), then join src side.
	withDst := fmt.Sprintf(
		`increase(%s{reporter="server"%s}[%s])
            * on(destination_namespace, destination_pod) group_left(%s, %s) %s`,
		metric, extraMatcher, duration, dst, dstUID, dstMapping)

	return fmt.Sprintf(`
    sum by (%s, %s, %s, %s) (
        (%s)
        * on(source_namespace, source_pod) group_left(%s, %s) %s
    )`, src, dst, srcUID, dstUID, withDst, src, srcUID, srcMapping)
}

// BuildRuntimeTopologyComponentEdgeRequestCountQuery builds a PromQL instant query
// returning the total HTTP request count per (src_component_uid, dst_component_uid)
// edge over the given duration window.
func BuildRuntimeTopologyComponentEdgeRequestCountQuery(duration, labelFilter string) string {
	return buildRuntimeTopologyIncreaseQuery("hubble_http_requests_total", "", labelFilter, duration) + "\n    > 0"
}

// BuildRuntimeTopologyComponentEdgeErrorCountQuery builds a PromQL instant query
// returning the total 4xx/5xx error count per (src_component_uid, dst_component_uid)
// edge over the given duration window.
func BuildRuntimeTopologyComponentEdgeErrorCountQuery(duration, labelFilter string) string {
	return buildRuntimeTopologyIncreaseQuery(
		"hubble_http_requests_total", `,status=~"^[45]..?$"`, labelFilter, duration,
	) + "\n    > 0"
}

// BuildRuntimeTopologyComponentEdgeMeanLatencyQuery builds a PromQL instant query
// for mean HTTP request latency per (src_component_uid, dst_component_uid) edge
// over the given duration window. Result is in seconds.
func BuildRuntimeTopologyComponentEdgeMeanLatencyQuery(duration, labelFilter string) string {
	durationSum := buildRuntimeTopologyIncreaseQuery("hubble_http_request_duration_seconds_sum", "", labelFilter, duration)
	requestCount := buildRuntimeTopologyIncreaseQuery("hubble_http_requests_total", "", labelFilter, duration)
	return fmt.Sprintf("(%s\n    /\n%s)\n    >= 0", durationSum, requestCount)
}

// buildRuntimeTopologyBucketQuery returns the histogram bucket increase expression
// keyed by (src_component, dst_component, src_component_uid, dst_component_uid, le).
func buildRuntimeTopologyBucketQuery(duration, labelFilter string) string {
	src := RuntimeTopologySrcComponentNameLabel
	dst := RuntimeTopologyDstComponentNameLabel
	srcUID := RuntimeTopologySrcComponentUIDLabel
	dstUID := RuntimeTopologyDstComponentUIDLabel
	srcMapping := buildHubblePodMappingExprWithSide("source", src, srcUID, labelFilter)
	dstMapping := buildHubblePodMappingExprWithSide("destination", dst, dstUID, labelFilter)

	withDst := fmt.Sprintf(
		`increase(hubble_http_request_duration_seconds_bucket{reporter="server"}[%s])
            * on(destination_namespace, destination_pod) group_left(%s, %s) %s`,
		duration, dst, dstUID, dstMapping)

	return fmt.Sprintf(`
    sum by (%s, %s, %s, %s, le) (
        (%s)
        * on(source_namespace, source_pod) group_left(%s, %s) %s
    )`, src, dst, srcUID, dstUID, withDst, src, srcUID, srcMapping)
}

// BuildRuntimeTopologyComponentEdgeLatencyPercentileQuery builds a PromQL instant
// query using histogram_quantile for the given quantile (e.g. "0.5", "0.9", "0.99")
// over the given duration window. Result is in seconds.
func BuildRuntimeTopologyComponentEdgeLatencyPercentileQuery(quantile, duration, labelFilter string) string {
	return fmt.Sprintf("histogram_quantile(%s,\n%s)\n    >= 0", quantile, buildRuntimeTopologyBucketQuery(duration, labelFilter))
}
