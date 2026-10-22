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
	LabelReleaseLineUID = "openchoreo.dev/release-line-uid"
	// LabelReleaseLineComponentUID is the component UID on a release line's pods, which don't
	// carry LabelComponentUID.
	LabelReleaseLineComponentUID = "openchoreo.dev/release-line-component-uid"
	LabelNamespace               = "openchoreo.dev/namespace"
	LabelComponent               = "openchoreo.dev/component"
	LabelProject                 = "openchoreo.dev/project"
	LabelEnvironment             = "openchoreo.dev/environment"
)

// prometheusLabelName converts a Kubernetes label name to a Prometheus metric label name.
// e.g., "openchoreo.dev/component-uid" becomes "label_openchoreo_dev_component_uid"
func prometheusLabelName(kubernetesLabel string) string {
	label := strings.ReplaceAll(kubernetesLabel, "-", "_")
	label = strings.ReplaceAll(label, ".", "_")
	label = strings.ReplaceAll(label, "/", "_")
	return "label_" + label
}

// BuildPodLabels builds the kube_pod_labels selector a query joins on to pick a scope's pods.
// IDs that are empty are not matched on; the namespace is always matched.
//
// A release line's pods carry the component UID under another label than the main line's, so the
// component UID alone covers every line, as a union of two selectors. A release line matches on
// its UID alone, and mainOnly keeps the main line's pods, which carry no release line label.
func BuildPodLabels(namespace, componentUID, projectUID, environmentUID, releaseLineUID string, mainOnly bool) string {
	base := fmt.Sprintf("%s=%q", prometheusLabelName(LabelNamespace), namespace)
	if projectUID != "" {
		base = fmt.Sprintf("%s,%s=%q", base, prometheusLabelName(LabelProjectUID), projectUID)
	}
	if environmentUID != "" {
		base = fmt.Sprintf("%s,%s=%q", base, prometheusLabelName(LabelEnvironmentUID), environmentUID)
	}
	return podLabelsExpr(lineScopeMatchers(base, componentUID, releaseLineUID, mainOnly))
}

// lineScopeMatchers returns one matcher list per kube_pod_labels selector of a component's line scope.
func lineScopeMatchers(base, componentUID, releaseLineUID string, mainOnly bool) []string {
	switch {
	case releaseLineUID != "":
		return []string{fmt.Sprintf("%s,%s=%q", base, prometheusLabelName(LabelReleaseLineUID), releaseLineUID)}
	case componentUID == "":
		return []string{base}
	case mainOnly:
		return []string{fmt.Sprintf(`%s,%s=%q,%s=""`, base,
			prometheusLabelName(LabelComponentUID), componentUID, prometheusLabelName(LabelReleaseLineUID))}
	}
	return []string{
		fmt.Sprintf("%s,%s=%q", base, prometheusLabelName(LabelComponentUID), componentUID),
		fmt.Sprintf("%s,%s=%q", base, prometheusLabelName(LabelReleaseLineComponentUID), componentUID),
	}
}

// podLabelsExpr is a kube_pod_labels selector per matcher list, joined with or.
func podLabelsExpr(matchers []string) string {
	selectors := make([]string, len(matchers))
	for i, m := range matchers {
		selectors[i] = fmt.Sprintf(`kube_pod_labels{job="kube-state-metrics",%s}`, m)
	}
	if len(selectors) == 1 {
		return selectors[0]
	}
	return "(" + strings.Join(selectors, " or ") + ")"
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

// BuildComponentPodLabels builds the kube_pod_labels selector of an alert rule's component and
// release line, without the namespace. An empty release line is the component's main line only.
func BuildComponentPodLabels(componentUID, projectUID, environmentUID, releaseLineUID string) string {
	base := fmt.Sprintf(`%s=%q,%s=%q`,
		prometheusLabelName(LabelProjectUID), projectUID, prometheusLabelName(LabelEnvironmentUID), environmentUID)
	return podLabelsExpr(lineScopeMatchers(base, componentUID, releaseLineUID, releaseLineUID == ""))
}

// ----------------------------
// Resource Metrics Queries
// ----------------------------

// BuildCPUUsageQuery builds a PromQL query for CPU usage rate.
func BuildCPUUsageQuery(podLabels, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
    rate(container_cpu_usage_seconds_total{container!=""}[2m]) * on (pod, namespace) %s %s )`, sumByClause, groupLeftClause, podLabels)
}

// BuildCPURequestsQuery builds a PromQL query for CPU requests.
func BuildCPURequestsQuery(podLabels, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_requests{resource="cpu",job="kube-state-metrics"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            %s
        )`, sumByClause, groupLeftClause, podLabels)
}

// BuildCPULimitsQuery builds a PromQL query for CPU limits.
func BuildCPULimitsQuery(podLabels, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_limits{resource="cpu",job="kube-state-metrics"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            %s
        )`, sumByClause, groupLeftClause, podLabels)
}

// BuildMemoryUsageQuery builds a PromQL query for memory usage.
func BuildMemoryUsageQuery(podLabels, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
              container_memory_working_set_bytes{container!=""}
              * on (pod, namespace) %s
                %s
            )`, sumByClause, groupLeftClause, podLabels)
}

// BuildMemoryRequestsQuery builds a PromQL query for memory requests.
func BuildMemoryRequestsQuery(podLabels, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_requests{resource="memory",job="kube-state-metrics"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            %s
        )`, sumByClause, groupLeftClause, podLabels)
}

// BuildMemoryLimitsQuery builds a PromQL query for memory limits.
func BuildMemoryLimitsQuery(podLabels, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_limits{resource="memory",job="kube-state-metrics"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            %s
        )`, sumByClause, groupLeftClause, podLabels)
}

// ----------------------------
// HTTP Request Metrics Queries
// ----------------------------

// buildHubblePodMappingExpr returns a PromQL expression that exposes
// the podLabels selector under Hubble's join keys
// (destination_namespace, destination_pod).
func buildHubblePodMappingExpr(podLabels string) string {
	return fmt.Sprintf(`label_replace(
            label_replace(
                %s,
                "destination_namespace", "$1", "namespace", "(.*)"
            ),
            "destination_pod", "$1", "pod", "(.*)"
        )`, podLabels)
}

// buildHTTPRequestCountQueryWithStatus builds the request-count PromQL,
// optionally filtered by an extra `status=~...` matcher.
func buildHTTPRequestCountQueryWithStatus(podLabels, sumByClause, groupLeftClause, statusMatcher string) string {
	mapping := buildHubblePodMappingExpr(podLabels)
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
func BuildHTTPRequestCountQuery(podLabels, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestCountQueryWithStatus(podLabels, sumByClause, groupLeftClause, "")
}

// BuildSuccessfulHTTPRequestCountQuery builds a PromQL query for successful HTTP request count
// (1xx, 2xx, 3xx).
func BuildSuccessfulHTTPRequestCountQuery(podLabels, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestCountQueryWithStatus(podLabels, sumByClause, groupLeftClause, `, status=~"^[123]..?$"`)
}

// BuildUnsuccessfulHTTPRequestCountQuery builds a PromQL query for unsuccessful HTTP request
// count (4xx, 5xx).
func BuildUnsuccessfulHTTPRequestCountQuery(podLabels, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestCountQueryWithStatus(podLabels, sumByClause, groupLeftClause, `, status=~"^[45]..?$"`)
}

// BuildMeanHTTPRequestLatencyQuery builds a PromQL query for mean HTTP request latency.
func BuildMeanHTTPRequestLatencyQuery(podLabels, sumByClause, groupLeftClause string) string {
	mapping := buildHubblePodMappingExpr(podLabels)
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
func buildHTTPRequestLatencyPercentileQuery(quantile, podLabels, sumByClause, groupLeftClause string) string {
	mapping := buildHubblePodMappingExpr(podLabels)
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
func Build50thPercentileHTTPRequestLatencyQuery(podLabels, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestLatencyPercentileQuery("0.5", podLabels, sumByClause, groupLeftClause)
}

// Build90thPercentileHTTPRequestLatencyQuery builds a PromQL query for 90th percentile HTTP request latency.
func Build90thPercentileHTTPRequestLatencyQuery(podLabels, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestLatencyPercentileQuery("0.9", podLabels, sumByClause, groupLeftClause)
}

// Build99thPercentileHTTPRequestLatencyQuery builds a PromQL query for 99th percentile HTTP request latency.
func Build99thPercentileHTTPRequestLatencyQuery(podLabels, sumByClause, groupLeftClause string) string {
	return buildHTTPRequestLatencyPercentileQuery("0.99", podLabels, sumByClause, groupLeftClause)
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
// topology queries using UID labels.
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
	return withReleaseLineComponentUID(fmt.Sprintf(`label_replace(
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
        )`, labelFilter, nsLabel, podLabel, outputNameLabel, componentNameLabel, outputUIDLabel, componentUIDLabel), outputUIDLabel)
}

// withReleaseLineComponentUID fills outputUIDLabel of a release line pod's series, which has no
// component UID label, from the label that carries it on release line pods.
func withReleaseLineComponentUID(expr, outputUIDLabel string) string {
	return fmt.Sprintf(`label_replace(%s, "%s", "$1", "%s", "(.+)")`,
		expr, outputUIDLabel, prometheusLabelName(LabelReleaseLineComponentUID))
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
