// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import (
	"strings"
	"testing"
	"time"
)

func TestBuildResourceMetricsKQL_NamespaceOnly(t *testing.T) {
	kql := BuildResourceMetricsKQL(MetricsQueryParams{
		Namespace: "dp-acme-dev",
		Step:      5 * time.Minute,
	})

	mustContain(t, kql, `extend _labels = parse_json(PodLabel)[0]`)
	mustContain(t, kql, `tostring(_labels["openchoreo.dev/namespace"]) == "dp-acme-dev"`)
	mustContain(t, kql, `Perf`)
	mustContain(t, kql, `where ObjectName == "K8SContainer"`)
	mustContain(t, kql, `strcat(ClusterId, "/", ContainerName)`)
	mustContain(t, kql, `where InstanceName in (_pods)`)
	mustContain(t, kql, `bin(TimeGenerated, 5m)`)

	// Gauges must be reduced per instance (avg over the bin's samples) before
	// summing across instances; a raw sum(CounterValue) over the bin would
	// inflate the value by the per-instance sample count.
	mustContain(t, kql, `_instanceValue = avg(CounterValue) by CounterName, InstanceName, TimeGenerated = bin(TimeGenerated, 5m)`)
	mustContain(t, kql, `Value = sum(_instanceValue) by CounterName, TimeGenerated`)
	if strings.Contains(kql, "sum(CounterValue)") {
		t.Errorf("must not sum raw samples (inflates by sample count), got:\n%s", kql)
	}

	// UID filters must be absent when not requested.
	if strings.Contains(kql, "component-uid") {
		t.Errorf("did not expect component-uid filter, got:\n%s", kql)
	}
}

func TestBuildResourceMetricsKQL_AllScopes(t *testing.T) {
	kql := BuildResourceMetricsKQL(MetricsQueryParams{
		Namespace:      "dp-acme-dev",
		ComponentUID:   "comp-123",
		ProjectUID:     "proj-456",
		EnvironmentUID: "env-789",
		Step:           time.Minute,
	})

	mustContain(t, kql, `tostring(_labels["openchoreo.dev/component-uid"]) == "comp-123"`)
	mustContain(t, kql, `tostring(_labels["openchoreo.dev/project-uid"]) == "proj-456"`)
	mustContain(t, kql, `tostring(_labels["openchoreo.dev/environment-uid"]) == "env-789"`)
	mustContain(t, kql, `bin(TimeGenerated, 1m)`)

	for _, counter := range allCounters() {
		mustContain(t, kql, counter)
	}
}

func TestBuildResourceMetricsKQL_DefaultsBin(t *testing.T) {
	// Step omitted -> default 5m.
	kql := BuildResourceMetricsKQL(MetricsQueryParams{Namespace: "ns"})
	mustContain(t, kql, `bin(TimeGenerated, 5m)`)

	// Sub-minute step -> clamped to 1m.
	kql = BuildResourceMetricsKQL(MetricsQueryParams{Namespace: "ns", Step: 10 * time.Second})
	mustContain(t, kql, `bin(TimeGenerated, 1m)`)

	// Hour step renders as 1h.
	kql = BuildResourceMetricsKQL(MetricsQueryParams{Namespace: "ns", Step: time.Hour})
	mustContain(t, kql, `bin(TimeGenerated, 1h)`)
}

func TestBuildResourceMetricsKQL_EscapesInjection(t *testing.T) {
	// A namespace with a quote must not break out of the KQL literal.
	kql := BuildResourceMetricsKQL(MetricsQueryParams{Namespace: `ns" or "1"=="1`})
	// The raw, unescaped injection payload must not appear as a comparison RHS.
	if strings.Contains(kql, `== "ns" or "1"=="1"`) {
		t.Errorf("namespace was not escaped, injection possible:\n%s", kql)
	}
	mustContain(t, kql, `\"`)
}

func TestKQLTimespan(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "30s",
		5 * time.Minute:  "5m",
		2 * time.Hour:    "2h",
		90 * time.Second: "90s",
	}
	for d, want := range cases {
		if got := kqlTimespan(d); got != want {
			t.Errorf("kqlTimespan(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestPingKQL(t *testing.T) {
	kql := PingKQL()
	mustContain(t, kql, "Perf")
	mustContain(t, kql, "K8SContainer")
	mustContain(t, kql, "take 1")
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected query to contain %q, got:\n%s", needle, haystack)
	}
}
