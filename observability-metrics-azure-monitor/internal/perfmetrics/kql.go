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
// returns one row per (CounterName, time-bin) with the counter value summed
// across the matched pods' containers.
//
// Shape:
//   - Filter KubePodInventory to pods whose PodLabel carries the requested
//     OpenChoreo namespace (required) plus optional component/project/env UIDs.
//   - Derive the Perf join key: InstanceName = strcat(ClusterId, '/', ContainerName).
//     (Verified against Microsoft's Container Insights visualization queries.)
//   - Join to Perf rows for ObjectName == 'K8SContainer' carrying the six
//     resource counters, scoped to those instances.
//   - Reduce in two steps: average each instance's samples within the bin, then
//     sum across instances. These counters are gauges sampled at ~1-minute
//     cadence, so a single bin holds several samples per container (e.g. ~5 at
//     the 5-minute default). Summing raw samples would inflate the value by the
//     sample count (and scale with the step); averaging per instance first
//     collapses the samples to one value per container before the cross-
//     container sum. This matches the avg() reduction the alert path uses.
//
// The time range is passed via the SDK Timespan option, not the query body,
// matching the logs adapter.
func BuildResourceMetricsKQL(p MetricsQueryParams) string {
	bin := binOrDefault(p.Step)

	// Optional UID filters; only the namespace label is always present.
	var scopeFilters strings.Builder
	for _, f := range []struct{ label, value string }{
		{LabelComponentUID, p.ComponentUID},
		{LabelProjectUID, p.ProjectUID},
		{LabelEnvironmentUID, p.EnvironmentUID},
	} {
		if f.value != "" {
			scopeFilters.WriteString("\n    | where ")
			scopeFilters.WriteString(podLabelEquals(f.label, f.value))
		}
	}

	return fmt.Sprintf(`let _pods = %s
    | extend _labels = parse_json(PodLabel)[0]
    | where %s%s
    | extend _instance = strcat(ClusterId, "/", ContainerName)
    | distinct _instance;
%s
| where ObjectName == "K8SContainer"
| where CounterName in (%s)
| where InstanceName in (_pods)
| summarize _instanceValue = avg(CounterValue) by CounterName, InstanceName, TimeGenerated = bin(TimeGenerated, %s)
| summarize Value = sum(_instanceValue) by CounterName, TimeGenerated
| order by TimeGenerated asc
| project CounterName, TimeGenerated, Value`,
		KubePodInventoryTable,
		podLabelEquals(LabelNamespace, p.Namespace),
		scopeFilters.String(),
		PerfTable,
		strings.Join(quoteAll(allCounters()), ", "),
		kqlTimespan(bin))
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
