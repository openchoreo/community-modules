// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

func validInput() RuleInput {
	return RuleInput{
		Namespace: "default",
		RuleName:  "high-error-rate",
		Query:     `ContainerLogV2 | where LogLevel == "ERROR" | count`,
		Operator:  "gt",
		Threshold: 5,
		Interval:  "PT5M",
		Window:    "PT5M",
		Enabled:   true,
	}
}

func validCfg() TranslatorConfig {
	return TranslatorConfig{
		Region:                     "eastus2",
		WorkspaceResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws",
		ActionGroupID:              "/subscriptions/sub/resourceGroups/rg/providers/microsoft.insights/actionGroups/ag",
		DefaultEvaluationFrequency: "PT5M",
		DefaultWindowSize:          "PT5M",
	}
}

func TestToScheduledQueryRule_HappyPath(t *testing.T) {
	res, err := ToScheduledQueryRule(validInput(), validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Kind == nil || *res.Kind != armmonitor.KindLogAlert {
		t.Errorf("kind: want LogAlert, got %v", res.Kind)
	}
	if res.Location == nil || *res.Location != "eastus2" {
		t.Errorf("location: want eastus2, got %v", res.Location)
	}
	if got := len(res.Properties.Scopes); got != 1 {
		t.Fatalf("scopes: want 1, got %d", got)
	}
	if res.Properties.Criteria == nil || len(res.Properties.Criteria.AllOf) != 1 {
		t.Fatalf("criteria.allOf: want 1 condition")
	}
	cond := res.Properties.Criteria.AllOf[0]
	if cond.Operator == nil || *cond.Operator != armmonitor.ConditionOperatorGreaterThan {
		t.Errorf("operator: want GreaterThan, got %v", cond.Operator)
	}
	if cond.Threshold == nil || *cond.Threshold != 5 {
		t.Errorf("threshold: want 5, got %v", cond.Threshold)
	}
	if cond.TimeAggregation == nil || *cond.TimeAggregation != armmonitor.TimeAggregationCount {
		t.Errorf("timeAggregation: want Count, got %v", cond.TimeAggregation)
	}
	if cond.Query == nil || !strings.Contains(*cond.Query, "ContainerLogV2") {
		t.Errorf("query missing")
	}
	if res.Properties.Actions == nil || len(res.Properties.Actions.ActionGroups) != 1 {
		t.Fatalf("actions.actionGroups: want 1, got %v", res.Properties.Actions)
	}
	if got := res.Properties.Actions.CustomProperties[CustomPropOpenChoreoNamespace]; got == nil || *got != "default" {
		t.Errorf("customProperties[%q] missing or wrong", CustomPropOpenChoreoNamespace)
	}
	if got := res.Properties.Actions.CustomProperties[CustomPropOpenChoreoRuleName]; got == nil || *got != "high-error-rate" {
		t.Errorf("customProperties[%q] missing or wrong", CustomPropOpenChoreoRuleName)
	}
}

func TestToScheduledQueryRule_AppliesDefaults(t *testing.T) {
	in := validInput()
	in.Interval = ""
	in.Window = ""
	res, err := ToScheduledQueryRule(in, validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Properties.EvaluationFrequency == nil || *res.Properties.EvaluationFrequency != "PT5M" {
		t.Errorf("evaluationFrequency default not applied")
	}
	if res.Properties.WindowSize == nil || *res.Properties.WindowSize != "PT5M" {
		t.Errorf("windowSize default not applied")
	}
}

func TestToScheduledQueryRule_OperatorMapping(t *testing.T) {
	cases := map[string]armmonitor.ConditionOperator{
		"gt":  armmonitor.ConditionOperatorGreaterThan,
		"gte": armmonitor.ConditionOperatorGreaterThanOrEqual,
		"lt":  armmonitor.ConditionOperatorLessThan,
		"lte": armmonitor.ConditionOperatorLessThanOrEqual,
		"eq":  armmonitor.ConditionOperatorEquals,
	}
	for op, want := range cases {
		t.Run(op, func(t *testing.T) {
			in := validInput()
			in.Operator = op
			res, err := ToScheduledQueryRule(in, validCfg())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := *res.Properties.Criteria.AllOf[0].Operator
			if got != want {
				t.Errorf("op %q: want %v, got %v", op, want, got)
			}
		})
	}
}

func TestToScheduledQueryRule_RejectsUnknownOperator(t *testing.T) {
	in := validInput()
	in.Operator = "approximately"
	_, err := ToScheduledQueryRule(in, validCfg())
	if err == nil {
		t.Fatal("expected error for unknown operator")
	}
}

func TestToScheduledQueryRule_RejectsMissingRequired(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*RuleInput, *TranslatorConfig)
	}{
		{"empty namespace", func(in *RuleInput, _ *TranslatorConfig) { in.Namespace = "" }},
		{"empty rule name", func(in *RuleInput, _ *TranslatorConfig) { in.RuleName = "" }},
		{"empty query", func(in *RuleInput, _ *TranslatorConfig) { in.Query = "" }},
		{"empty workspace", func(_ *RuleInput, cfg *TranslatorConfig) { cfg.WorkspaceResourceID = "" }},
		{"empty action group", func(_ *RuleInput, cfg *TranslatorConfig) { cfg.ActionGroupID = "" }},
		{"empty region", func(_ *RuleInput, cfg *TranslatorConfig) { cfg.Region = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			cfg := validCfg()
			tc.mut(&in, &cfg)
			if _, err := ToScheduledQueryRule(in, cfg); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestDeriveAzureName_DeterministicAndSafe(t *testing.T) {
	a := DeriveAzureName("default", "rule-x")
	b := DeriveAzureName("default", "rule-x")
	if a != b {
		t.Errorf("not deterministic: %s != %s", a, b)
	}
	if !strings.HasPrefix(a, "oc-") {
		t.Errorf("missing oc- prefix: %s", a)
	}
	if len(a) > 260 {
		t.Errorf("name too long: %d", len(a))
	}
	// Azure forbids these characters: # < > % & : ? / { } *
	for _, c := range []rune{'#', '<', '>', '%', '&', ':', '?', '/', '{', '}', '*'} {
		if strings.ContainsRune(a, c) {
			t.Errorf("name contains forbidden char %q: %s", c, a)
		}
	}
}

func TestToScheduledQueryRule_ConvertsGoDuration(t *testing.T) {
	in := validInput()
	in.Interval = "1m"
	in.Window = "5m"
	res, err := ToScheduledQueryRule(in, validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := *res.Properties.EvaluationFrequency; got != "PT1M" {
		t.Errorf("evaluationFrequency: want PT1M, got %q", got)
	}
	if got := *res.Properties.WindowSize; got != "PT5M" {
		t.Errorf("windowSize: want PT5M, got %q", got)
	}
}

func TestToScheduledQueryRule_AcceptsISO8601Duration(t *testing.T) {
	in := validInput()
	in.Interval = "PT1M"
	in.Window = "PT5M"
	res, err := ToScheduledQueryRule(in, validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := *res.Properties.EvaluationFrequency; got != "PT1M" {
		t.Errorf("evaluationFrequency: want PT1M, got %q", got)
	}
}

func TestGoDurationToISO8601(t *testing.T) {
	cases := map[string]string{
		"1m":     "PT1M",
		"5m":     "PT5M",
		"1h":     "PT1H",
		"2h30m":  "PT2H30M",
		"24h":    "PT24H",
		"90s":    "PT1M30S",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := toISO8601Duration(in, "PT5M")
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != want {
				t.Errorf("toISO8601Duration(%q): want %q got %q", in, want, got)
			}
		})
	}
}

func TestDeriveAzureName_DifferentInputsDiffer(t *testing.T) {
	a := DeriveAzureName("ns-a", "rule")
	b := DeriveAzureName("ns-b", "rule")
	if a == b {
		t.Errorf("expected different names, both: %s", a)
	}
}
