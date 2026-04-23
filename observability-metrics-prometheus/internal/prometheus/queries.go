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
    rate(container_cpu_usage_seconds_total{container!=""}[2m]) * on (pod, namespace) %s kube_pod_labels{%s} )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildCPURequestsQuery builds a PromQL query for CPU requests.
func BuildCPURequestsQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_requests{resource="cpu"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            kube_pod_labels{%s}
        )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildCPULimitsQuery builds a PromQL query for CPU limits.
func BuildCPULimitsQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_limits{resource="cpu"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            kube_pod_labels{%s}
        )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildMemoryUsageQuery builds a PromQL query for memory usage.
func BuildMemoryUsageQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
              container_memory_working_set_bytes{container!=""}
              * on (pod, namespace) %s
                kube_pod_labels{%s}
            )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildMemoryRequestsQuery builds a PromQL query for memory requests.
func BuildMemoryRequestsQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_requests{resource="memory"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            kube_pod_labels{%s}
        )`, sumByClause, groupLeftClause, labelFilter)
}

// BuildMemoryLimitsQuery builds a PromQL query for memory limits.
func BuildMemoryLimitsQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`sum by (%s) (
            (
                kube_pod_container_resource_limits{resource="memory"}
                AND ON (pod, namespace)
                (kube_pod_status_phase{phase="Running"} == 1)
            )
          * ON (pod, namespace) %s
            kube_pod_labels{%s}
        )`, sumByClause, groupLeftClause, labelFilter)
}

// ----------------------------
// HTTP Request Metrics Queries
// ----------------------------

// BuildHTTPRequestCountQuery builds a PromQL query for HTTP request count.
func BuildHTTPRequestCountQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`
	    sum by (%s) (
	        rate(hubble_http_requests_total{reporter="client"}[2m])
	            * on(destination_pod, destination_namespace) %s
	            label_replace(
	                label_replace(
	                    kube_pod_labels{%s},
	                    "destination_pod",
	                    "$1",
	                    "pod",
	                    "(.*)"
	                ),
	                "destination_namespace",
	                "$1",
	                "namespace",
	                "(.*)"
	            )
	    )
	    >= 0
	`, sumByClause, groupLeftClause, labelFilter)
}

// BuildSuccessfulHTTPRequestCountQuery builds a PromQL query for successful HTTP request count
// (1xx, 2xx, 3xx).
func BuildSuccessfulHTTPRequestCountQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`
	    sum by (%s) (
	        rate(hubble_http_requests_total{reporter="client", status=~"^[123]..?$"}[2m])
	            * on(destination_pod, destination_namespace) %s
	            label_replace(
	                label_replace(
	                    kube_pod_labels{%s},
	                    "destination_pod",
	                    "$1",
	                    "pod",
	                    "(.*)"
	                ),
	                "destination_namespace",
	                "$1",
	                "namespace",
	                "(.*)"
	            )
	    )
	    >= 0
	`, sumByClause, groupLeftClause, labelFilter)
}

// BuildUnsuccessfulHTTPRequestCountQuery builds a PromQL query for unsuccessful HTTP request
// count (4xx, 5xx).
func BuildUnsuccessfulHTTPRequestCountQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`
	    sum by (%s) (
	        rate(hubble_http_requests_total{reporter="client", status=~"^[45]..?$"}[2m])
	            * on(destination_pod, destination_namespace) %s
	            label_replace(
	                label_replace(
	                    kube_pod_labels{%s},
	                    "destination_pod",
	                    "$1",
	                    "pod",
	                    "(.*)"
	                ),
	                "destination_namespace",
	                "$1",
	                "namespace",
	                "(.*)"
	            )
	    )
	    >= 0
	`, sumByClause, groupLeftClause, labelFilter)
}

// BuildMeanHTTPRequestLatencyQuery builds a PromQL query for mean HTTP request latency.
func BuildMeanHTTPRequestLatencyQuery(labelFilter, sumByClause, groupLeftClause string) string {
	return fmt.Sprintf(`
	    (
	        sum by (%s) (
	            rate(hubble_http_request_duration_seconds_sum{reporter="client"}[2m])
	                * on(destination_pod, destination_namespace) %s
	                label_replace(
	                    label_replace(
	                        kube_pod_labels{%s},
	                        "destination_pod",
	                        "$1",
	                        "pod",
	                        "(.*)"
	                    ),
	                    "destination_namespace",
	                    "$1",
	                    "namespace",
	                    "(.*)"
	                )
	        )
	        /
	        sum by (%s) (
	            rate(hubble_http_requests_total{reporter="client"}[2m])
	                * on(destination_pod, destination_namespace) %s
	                label_replace(
	                    label_replace(
	                        kube_pod_labels{%s},
	                        "destination_pod",
	                        "$1",
	                        "pod",
	                        "(.*)"
	                    ),
	                    "destination_namespace",
	                    "$1",
	                    "namespace",
	                    "(.*)"
	                )
	        )
	    )
	    >= 0
	`, sumByClause, groupLeftClause, labelFilter, sumByClause, groupLeftClause, labelFilter)
}

// Build50thPercentileHTTPRequestLatencyQuery builds a PromQL query for 50th percentile HTTP request latency.
func Build50thPercentileHTTPRequestLatencyQuery(labelFilter, sumByClause, groupLeftClause string) string {
	histogramSumByClause := BuildHistogramSumByClause(sumByClause)
	return fmt.Sprintf(`
	    histogram_quantile(
	        0.5,
	        sum by (%s) (
	            rate(hubble_http_request_duration_seconds_bucket{reporter="client"}[2m])
	                * on(destination_pod, destination_namespace) %s
	                label_replace(
	                    label_replace(
	                        kube_pod_labels{%s},
	                        "destination_pod",
	                        "$1",
	                        "pod",
	                        "(.*)"
	                    ),
	                    "destination_namespace",
	                    "$1",
	                    "namespace",
	                    "(.*)"
	                )
	        )
	    )
	    >= 0
	`, histogramSumByClause, groupLeftClause, labelFilter)
}

// Build90thPercentileHTTPRequestLatencyQuery builds a PromQL query for 90th percentile HTTP request latency.
func Build90thPercentileHTTPRequestLatencyQuery(labelFilter, sumByClause, groupLeftClause string) string {
	histogramSumByClause := BuildHistogramSumByClause(sumByClause)
	return fmt.Sprintf(`
	    histogram_quantile(
	        0.9,
	        sum by (%s) (
	            rate(hubble_http_request_duration_seconds_bucket{reporter="client"}[2m])
	                * on(destination_pod, destination_namespace) %s
	                label_replace(
	                    label_replace(
	                        kube_pod_labels{%s},
	                        "destination_pod",
	                        "$1",
	                        "pod",
	                        "(.*)"
	                    ),
	                    "destination_namespace",
	                    "$1",
	                    "namespace",
	                    "(.*)"
	                )
	        )
	    )
	    >= 0
	`, histogramSumByClause, groupLeftClause, labelFilter)
}

// Build99thPercentileHTTPRequestLatencyQuery builds a PromQL query for 99th percentile HTTP request latency.
func Build99thPercentileHTTPRequestLatencyQuery(labelFilter, sumByClause, groupLeftClause string) string {
	histogramSumByClause := BuildHistogramSumByClause(sumByClause)
	return fmt.Sprintf(`
	    histogram_quantile(
	        0.99,
	        sum by (%s) (
	            rate(hubble_http_request_duration_seconds_bucket{reporter="client"}[2m])
	                * on(destination_pod, destination_namespace) %s
	                label_replace(
	                    label_replace(
	                        kube_pod_labels{%s},
	                        "destination_pod",
	                        "$1",
	                        "pod",
	                        "(.*)"
	                    ),
	                    "destination_namespace",
	                    "$1",
	                    "namespace",
	                    "(.*)"
	                )
	        )
	    )
	    >= 0
	`, histogramSumByClause, groupLeftClause, labelFilter)
}
