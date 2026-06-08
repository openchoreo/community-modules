// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"strings"
	"testing"
)

func TestMetricNameForSource(t *testing.T) {
	cases := map[string]string{
		"cpu_usage":    counterCPUUsageNanoCores,
		"memory_usage": counterMemoryWorkingSetBytes,
		"CPU_USAGE":    counterCPUUsageNanoCores,
	}
	for in, want := range cases {
		got, err := MetricNameForSource(in)
		if err != nil {
			t.Errorf("MetricNameForSource(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("MetricNameForSource(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := MetricNameForSource("budget"); err == nil {
		t.Error("expected error for unsupported metric 'budget'")
	}
}

func TestBuildAlertKQL_CPU_PercentOfLimit(t *testing.T) {
	in := RuleInput{
		// metadata.namespace is the rendered DP namespace; it must NOT leak
		// into the pod-label filter (the label holds the control-plane ns).
		Namespace:    "dp-acme-dev",
		ComponentUID: "comp-1",
		Metric:       "cpu_usage",
	}
	kql := BuildAlertKQL(in, counterCPUUsageNanoCores)

	mustContain(t, kql, `let _pods = KubePodInventory`)
	mustContain(t, kql, `extend _labels = parse_json(PodLabel)[0]`)
	mustContain(t, kql, `tostring(_labels["openchoreo.dev/component-uid"]) == "comp-1"`)
	// usage and limit counters are both queried and divided.
	mustContain(t, kql, `CounterName == "cpuUsageNanoCores"`)
	mustContain(t, kql, `CounterName == "cpuLimitNanoCores"`)
	mustContain(t, kql, `summarize AggregatedValue = avg(_u) / avg(_l) * 100`)

	// The DP namespace must not appear as a namespace-label filter — doing so
	// matches zero pods because the label carries the control-plane namespace.
	if strings.Contains(kql, `openchoreo.dev/namespace`) {
		t.Errorf("namespace label filter must be omitted when a UID scopes the rule:\n%s", kql)
	}
}

func TestBuildAlertKQL_NamespaceFallbackWhenNoUID(t *testing.T) {
	// With no UID labels, the namespace label is the only available scope, so
	// it is applied as a fallback.
	in := RuleInput{Namespace: "default", Metric: "cpu_usage"}
	kql := BuildAlertKQL(in, counterCPUUsageNanoCores)
	mustContain(t, kql, `tostring(_labels["openchoreo.dev/namespace"]) == "default"`)
}

func TestBuildAlertKQL_Memory_PercentOfLimit(t *testing.T) {
	in := RuleInput{Namespace: "ns", ComponentUID: "comp-1", Metric: "memory_usage"}
	kql := BuildAlertKQL(in, counterMemoryWorkingSetBytes)

	mustContain(t, kql, `CounterName == "memoryWorkingSetBytes"`)
	mustContain(t, kql, `CounterName == "memoryLimitBytes"`)
	mustContain(t, kql, `summarize AggregatedValue = avg(_u) / avg(_l) * 100`)
}

func TestBuildAlertKQL_SkipsZeroUUID(t *testing.T) {
	in := RuleInput{
		Namespace:    "ns",
		ComponentUID: "00000000-0000-0000-0000-000000000000",
		Metric:       "cpu_usage",
	}
	kql := BuildAlertKQL(in, counterCPUUsageNanoCores)
	if strings.Contains(kql, "component-uid") {
		t.Errorf("zero UUID should be skipped:\n%s", kql)
	}
}

func TestBuildAlertKQL_HandWrittenPassthrough(t *testing.T) {
	in := RuleInput{Namespace: "ns", Metric: "cpu_usage", Query: "Perf | take 5"}
	kql := BuildAlertKQL(in, counterCPUUsageNanoCores)
	if kql != "Perf | take 5" {
		t.Errorf("hand-written KQL should pass through, got: %s", kql)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected query to contain %q, got:\n%s", needle, haystack)
	}
}
