// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

func testCfg() TranslatorConfig {
	return TranslatorConfig{
		Region:                     "eastus2",
		WorkspaceResourceID:        "/subscriptions/s/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws",
		ActionGroupID:              "/subscriptions/s/resourceGroups/rg/providers/microsoft.insights/actionGroups/ag",
		DefaultEvaluationFrequency: "PT5M",
		DefaultWindowSize:          "PT5M",
	}
}

func TestToScheduledQueryRule_BuildsAverageMetricAlert(t *testing.T) {
	in := RuleInput{
		Namespace:    "dp-acme-dev",
		RuleName:     "high-cpu",
		ComponentUID: "comp-1",
		Metric:       "cpu_usage",
		Operator:     "gt",
		Threshold:    0.8,
		Interval:     "5m",
		Window:       "10m",
		Enabled:      true,
	}
	res, err := ToScheduledQueryRule(in, testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cond := res.Properties.Criteria.AllOf[0]
	if *cond.TimeAggregation != armmonitor.TimeAggregationAverage {
		t.Errorf("expected Average aggregation, got %v", *cond.TimeAggregation)
	}
	if cond.MetricMeasureColumn == nil || *cond.MetricMeasureColumn != "AggregatedValue" {
		t.Errorf("expected MetricMeasureColumn=AggregatedValue")
	}
	if *cond.Operator != armmonitor.ConditionOperatorGreaterThan {
		t.Errorf("expected GreaterThan operator")
	}
	if *cond.Threshold != 0.8 {
		t.Errorf("threshold = %v, want 0.8", *cond.Threshold)
	}
	if *res.Properties.EvaluationFrequency != "PT5M" {
		t.Errorf("eval freq = %v, want PT5M", *res.Properties.EvaluationFrequency)
	}
	if *res.Properties.WindowSize != "PT10M" {
		t.Errorf("window = %v, want PT10M", *res.Properties.WindowSize)
	}
}

func TestToScheduledQueryRule_RejectsUnsupportedMetric(t *testing.T) {
	in := RuleInput{
		Namespace: "ns", RuleName: "r", Metric: "budget", Operator: "gt",
	}
	if _, err := ToScheduledQueryRule(in, testCfg()); err == nil {
		t.Error("expected error for unsupported metric 'budget'")
	}
}

func TestToISO8601Duration(t *testing.T) {
	cases := map[string]string{
		"5m":    "PT5M",
		"1h":    "PT1H",
		"2h30m": "PT2H30M",
		"90s":   "PT1M30S",
		"PT5M":  "PT5M",
	}
	for in, want := range cases {
		got, err := toISO8601Duration(in, "PT5M")
		if err != nil {
			t.Errorf("toISO8601Duration(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("toISO8601Duration(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSnapWindowToAzureGranularity(t *testing.T) {
	cases := map[string]string{
		"PT1M":   "PT1M",  // exact granularity passes through
		"PT2M":   "PT5M",  // the cart sample: 2m is unsupported, snaps up to 5m
		"PT5M":   "PT5M",  // exact
		"PT7M":   "PT10M", // between 5 and 10 → up to 10
		"PT10M":  "PT10M", // exact
		"PT11M":  "PT15M", // up to 15
		"PT46M":  "PT1H",  // 46m → 60m
		"PT2H":   "PT2H",  // 120m exact
		"PT2H1M": "PT3H",  // 121m → 180m
		"PT10H":  "PT6H",  // beyond max (360m) clamps to 6h
		"PT0S":   "PT1M",  // zero/degenerate → smallest granularity
	}
	for in, want := range cases {
		got, err := snapWindowToAzureGranularity(in)
		if err != nil {
			t.Errorf("snapWindowToAzureGranularity(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("snapWindowToAzureGranularity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToScheduledQueryRule_SnapsUnsupportedWindow(t *testing.T) {
	// Mirrors the cart sample: window 2m, interval 1m. Azure rejects a 2-minute
	// window, so the translator must snap it up to 5m.
	in := RuleInput{
		Namespace: "dp-acme-dev", RuleName: "cart-high-mem", ComponentUID: "comp-2",
		Metric: "memory_usage", Operator: "gt", Threshold: 70,
		Interval: "1m", Window: "2m", Enabled: true,
	}
	res, err := ToScheduledQueryRule(in, testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *res.Properties.WindowSize != "PT5M" {
		t.Errorf("window = %v, want PT5M (snapped from 2m)", *res.Properties.WindowSize)
	}
	if *res.Properties.EvaluationFrequency != "PT1M" {
		t.Errorf("eval freq = %v, want PT1M", *res.Properties.EvaluationFrequency)
	}
}
