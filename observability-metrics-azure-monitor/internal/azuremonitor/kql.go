// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"fmt"
	"strings"
)

const (
	labelNamespace      = "openchoreo.dev/namespace"
	labelComponentUID   = "openchoreo.dev/component-uid"
	labelProjectUID     = "openchoreo.dev/project-uid"
	labelEnvironmentUID = "openchoreo.dev/environment-uid"
)

// Perf counters the metric alerts threshold against. Each source compares a
// usage counter against the pod's corresponding resource-limit counter.
const (
	counterCPUUsageNanoCores     = "cpuUsageNanoCores"
	counterCPULimitNanoCores     = "cpuLimitNanoCores"
	counterMemoryWorkingSetBytes = "memoryWorkingSetBytes"
	counterMemoryLimitBytes      = "memoryLimitBytes"
)

// MetricNameForSource maps the OpenAPI source.metric vocabulary to the Perf
// usage CounterName the alert query aggregates. Only cpu_usage and memory_usage
// are supported; budget (FinOps) is rejected.
func MetricNameForSource(metric string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(metric)) {
	case "cpu_usage":
		return counterCPUUsageNanoCores, nil
	case "memory_usage":
		return counterMemoryWorkingSetBytes, nil
	default:
		return "", fmt.Errorf("unsupported source.metric %q (expected cpu_usage|memory_usage)", metric)
	}
}

// limitCounterForUsage returns the Perf limit CounterName paired with a usage
// counter. The alert thresholds usage as a percentage of this limit, matching
// the Prometheus and CloudWatch metrics adapters.
func limitCounterForUsage(usage string) string {
	switch usage {
	case counterCPUUsageNanoCores:
		return counterCPULimitNanoCores
	case counterMemoryWorkingSetBytes:
		return counterMemoryLimitBytes
	default:
		return ""
	}
}

// BuildAlertKQL renders a metric-alert query over Container Insights' Perf and
// KubePodInventory tables. The query expresses usage as a percentage of the
// pod's resource limit and projects a single AggregatedValue column that
// Azure's scheduledQueryRules thresholds against:
//
//	AggregatedValue = avg(usage counter) / avg(limit counter) * 100
//
// This matches the OpenChoreo alert semantics ("cpu_usage > 80" means 80% of
// the CPU limit) and is consistent with the Prometheus and AWS CloudWatch
// metrics adapters, which both threshold usage/limit*100.
//
// If in.Query already looks like a hand-written KQL statement, it is used
// verbatim (escape hatch for advanced rules).
func BuildAlertKQL(in RuleInput, counter string) string {
	if isKQLStatement(in.Query) {
		return in.Query
	}

	limit := limitCounterForUsage(counter)

	var sb strings.Builder

	// _pods: the matched workload pods' Perf join keys. PodLabel is a JSON
	// array of one object with slash-escaped keys, so parse it rather than
	// substring-match (verified against the live workspace).
	sb.WriteString("let _pods = KubePodInventory")
	sb.WriteString("\n    | extend _labels = parse_json(PodLabel)[0]")
	// Scope by the OpenChoreo UID labels, which are the reliable join keys.
	// Deliberately do NOT filter on the openchoreo.dev/namespace label here:
	// the alert request carries the CR's metadata.namespace (the rendered
	// data-plane namespace, e.g. dp-default-...), whereas the pod label holds
	// the *control-plane* namespace (e.g. default). Filtering the label by the
	// DP namespace matches zero pods, so the rule could never fire. The
	// component/project/environment UID labels uniquely identify the workload.
	// The namespace is only used as a fallback below when no UID is supplied.
	hasUID := normaliseUID(in.ComponentUID) != "" ||
		normaliseUID(in.ProjectUID) != "" ||
		normaliseUID(in.EnvironmentUID) != ""
	if !hasUID {
		if ns := strings.TrimSpace(in.Namespace); ns != "" {
			sb.WriteString("\n    | where tostring(_labels[")
			sb.WriteString(kqlString(labelNamespace))
			sb.WriteString("]) == ")
			sb.WriteString(kqlString(ns))
		}
	}
	if uid := normaliseUID(in.ComponentUID); uid != "" {
		sb.WriteString("\n    | where tostring(_labels[")
		sb.WriteString(kqlString(labelComponentUID))
		sb.WriteString("]) == ")
		sb.WriteString(kqlString(uid))
	}
	if uid := normaliseUID(in.ProjectUID); uid != "" {
		sb.WriteString("\n    | where tostring(_labels[")
		sb.WriteString(kqlString(labelProjectUID))
		sb.WriteString("]) == ")
		sb.WriteString(kqlString(uid))
	}
	if uid := normaliseUID(in.EnvironmentUID); uid != "" {
		sb.WriteString("\n    | where tostring(_labels[")
		sb.WriteString(kqlString(labelEnvironmentUID))
		sb.WriteString("]) == ")
		sb.WriteString(kqlString(uid))
	}
	sb.WriteString("\n    | extend _instance = strcat(ClusterId, \"/\", ContainerName)")
	sb.WriteString("\n    | distinct _instance;")

	// _usage / _limit: the mean usage and the mean configured limit across the
	// scoped pods' containers. A cross join (on a constant key) divides the two
	// scalars; multiplying by 100 yields a percentage of the limit.
	sb.WriteString("\nlet _usage = Perf")
	sb.WriteString("\n    | where ObjectName == \"K8SContainer\" and CounterName == ")
	sb.WriteString(kqlString(counter))
	sb.WriteString("\n    | where InstanceName in (_pods)")
	sb.WriteString("\n    | summarize _u = avg(CounterValue);")

	sb.WriteString("\nlet _limit = Perf")
	sb.WriteString("\n    | where ObjectName == \"K8SContainer\" and CounterName == ")
	sb.WriteString(kqlString(limit))
	sb.WriteString("\n    | where InstanceName in (_pods)")
	sb.WriteString("\n    | summarize _l = avg(CounterValue);")

	sb.WriteString("\n_usage")
	sb.WriteString("\n| extend _k = 1")
	sb.WriteString("\n| join kind=inner (_limit | extend _k = 1) on _k")
	sb.WriteString("\n| summarize AggregatedValue = avg(_u) / avg(_l) * 100")

	return sb.String()
}

func isKQLStatement(q string) bool {
	trimmed := strings.TrimSpace(q)
	for _, prefix := range []string{
		"Perf",
		"KubePodInventory",
		"InsightsMetrics",
		"let ",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// kqlString quotes a Go string as a KQL string literal. KQL uses double
// quotes; embedded double quotes and backslashes are escaped.
func kqlString(s string) string {
	out := strings.ReplaceAll(s, `\`, `\\`)
	out = strings.ReplaceAll(out, `"`, `\"`)
	return fmt.Sprintf(`"%s"`, out)
}

// normaliseUID strips zero-UUIDs that the OpenAPI client sends when a field is
// unset. uuid.UUID's zero value renders as 00000000-0000-0000-0000-000000000000.
func normaliseUID(u string) string {
	if u == "" || u == "00000000-0000-0000-0000-000000000000" {
		return ""
	}
	return u
}
