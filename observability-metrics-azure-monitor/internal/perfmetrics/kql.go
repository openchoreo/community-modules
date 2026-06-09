// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import (
	"fmt"
	"strings"
	"time"
)

const defaultBin = 5 * time.Minute
const minBin = time.Minute

// BuildResourceMetricsKQL renders a MetricsQueryParams as a KQL query that
// returns one row per (CounterName, time-bin) with the summed counter value
// across the matched pods' containers.
//
// Shape:
//   - Filter KubePodInventory to pods whose PodLabel carries the requested
//     OpenChoreo namespace (required) plus optional component/project/env UIDs.
//   - Derive the Perf join key: InstanceName = strcat(ClusterId, '/', ContainerName).
//     (Verified against Microsoft's Container Insights visualization queries.)
//   - Join to Perf rows for ObjectName == 'K8SContainer' carrying the six
//     resource counters, scoped to those instances.
//   - summarize sum(CounterValue) by CounterName, bin(TimeGenerated, step).
//
// The time range is passed via the SDK Timespan option, not the query body,
// matching the logs adapter.
func BuildResourceMetricsKQL(p MetricsQueryParams) string {
	bin := binOrDefault(p.Step)

	var sb strings.Builder

	sb.WriteString("let _pods = ")
	sb.WriteString(KubePodInventoryTable)
	sb.WriteString("\n    | extend _labels = parse_json(PodLabel)[0]")
	sb.WriteString("\n    | where ")
	sb.WriteString(podLabelEquals(LabelNamespace, p.Namespace))
	if p.ComponentUID != "" {
		sb.WriteString("\n    | where ")
		sb.WriteString(podLabelEquals(LabelComponentUID, p.ComponentUID))
	}
	if p.ProjectUID != "" {
		sb.WriteString("\n    | where ")
		sb.WriteString(podLabelEquals(LabelProjectUID, p.ProjectUID))
	}
	if p.EnvironmentUID != "" {
		sb.WriteString("\n    | where ")
		sb.WriteString(podLabelEquals(LabelEnvironmentUID, p.EnvironmentUID))
	}
	sb.WriteString("\n    | extend _instance = strcat(ClusterId, \"/\", ContainerName)")
	sb.WriteString("\n    | distinct _instance;\n")

	sb.WriteString(PerfTable)
	sb.WriteString("\n| where ObjectName == \"K8SContainer\"")
	sb.WriteString("\n| where CounterName in (")
	sb.WriteString(strings.Join(quoteAll(allCounters()), ", "))
	sb.WriteString(")")
	sb.WriteString("\n| where InstanceName in (_pods)")
	sb.WriteString(fmt.Sprintf("\n| summarize Value = sum(CounterValue) by CounterName, TimeGenerated = bin(TimeGenerated, %s)", kqlTimespan(bin)))
	sb.WriteString("\n| order by TimeGenerated asc")
	sb.WriteString("\n| project CounterName, TimeGenerated, Value")

	return sb.String()
}

func PingKQL() string {
	return PerfTable + " | where ObjectName == \"K8SContainer\" | take 1"
}

// allCounters returns the six Perf counters in a stable order.
func allCounters() []string {
	return []string{
		CounterCPUUsageNanoCores,
		CounterCPURequestNanoCores,
		CounterCPULimitNanoCores,
		CounterMemoryWorkingSetBytes,
		CounterMemoryRequestBytes,
		CounterMemoryLimitBytes,
	}
}

func podLabelEquals(label, value string) string {
	return fmt.Sprintf("tostring(_labels[%s]) == %s", kqlString(label), kqlString(value))
}

func binOrDefault(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultBin
	}
	if d < minBin {
		return minBin
	}
	return d
}

// kqlTimespan renders a Go duration as a KQL timespan literal (e.g. 5m, 1h).
func kqlTimespan(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int64(d/time.Second))
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = kqlString(s)
	}
	return out
}

// kqlString quotes and escapes a string for safe inclusion in a KQL literal.
func kqlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
