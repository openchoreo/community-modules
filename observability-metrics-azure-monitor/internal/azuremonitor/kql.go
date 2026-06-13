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

const (
	counterCPUUsageNanoCores     = "cpuUsageNanoCores"
	counterCPULimitNanoCores     = "cpuLimitNanoCores"
	counterMemoryWorkingSetBytes = "memoryWorkingSetBytes"
	counterMemoryLimitBytes      = "memoryLimitBytes"
)

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
func BuildAlertKQL(in RuleInput, counter string) string {
	if isKQLStatement(in.Query) {
		return in.Query
	}

	limit := limitCounterForUsage(counter)

	var sb strings.Builder

	sb.WriteString("let _pods = KubePodInventory")
	sb.WriteString("\n    | extend _labels = parse_json(PodLabel)[0]")
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

func kqlString(s string) string {
	out := strings.ReplaceAll(s, `\`, `\\`)
	out = strings.ReplaceAll(out, `"`, `\"`)
	return fmt.Sprintf(`"%s"`, out)
}

func normaliseUID(u string) string {
	if u == "" || u == "00000000-0000-0000-0000-000000000000" {
		return ""
	}
	return u
}
