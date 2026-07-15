// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"errors"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

func baseTranslatorCfg() TranslatorConfig {
	return TranslatorConfig{
		ProjectID:             "my-project",
		NotificationChannelID: "projects/my-project/notificationChannels/123",
		DefaultInterval:       60 * time.Second,
		DefaultWindow:         300 * time.Second,
	}
}

func validRuleInput() RuleInput {
	return RuleInput{
		Namespace: "default",
		RuleName:  "high-cpu",
		Metric:    "cpu_usage",
		Operator:  "gt",
		Threshold: 80, // percent of limit
		Enabled:   true,
	}
}

func TestToAlertPolicyHappyPath(t *testing.T) {
	p, err := ToAlertPolicy(validRuleInput(), baseTranslatorCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.GetDisplayName() != "default/high-cpu" {
		t.Errorf("displayName = %q", p.GetDisplayName())
	}
	if !p.GetEnabled().GetValue() {
		t.Errorf("policy should be enabled")
	}
	if got := p.GetUserLabels()[policyLabelManagedBy]; got != policyLabelManagedByValue {
		t.Errorf("managed-by label = %q", got)
	}
	if p.GetUserLabels()[policyLabelRuleHash] != ruleHash("default", "high-cpu") {
		t.Errorf("rule hash label mismatch")
	}
	if len(p.GetNotificationChannels()) != 1 || p.GetNotificationChannels()[0] != baseTranslatorCfg().NotificationChannelID {
		t.Errorf("notification channels = %v", p.GetNotificationChannels())
	}
	if len(p.GetConditions()) != 1 {
		t.Fatalf("conditions = %d, want 1", len(p.GetConditions()))
	}
	mt := p.GetConditions()[0].GetConditionThreshold()
	if mt == nil {
		t.Fatalf("condition is not a metric threshold")
	}
	if mt.GetComparison() != monitoringpb.ComparisonType_COMPARISON_GT {
		t.Errorf("comparison = %v", mt.GetComparison())
	}
	// Threshold is percent-of-limit → converted to a fraction for the
	// usage÷limit ratio comparison (80% → 0.80).
	if mt.GetThresholdValue() != 0.8 {
		t.Errorf("threshold = %v, want 0.8 (80%% as fraction)", mt.GetThresholdValue())
	}
	if !strings.Contains(mt.GetFilter(), `kubernetes.io/container/cpu/core_usage_time`) {
		t.Errorf("numerator filter missing cpu usage metric: %s", mt.GetFilter())
	}
	if !strings.Contains(mt.GetDenominatorFilter(), `kubernetes.io/container/cpu/limit_cores`) {
		t.Errorf("denominator filter missing cpu limit metric: %s", mt.GetDenominatorFilter())
	}
	if len(mt.GetDenominatorAggregations()) != 1 {
		t.Errorf("expected 1 denominator aggregation, got %d", len(mt.GetDenominatorAggregations()))
	}
	// Scoping is UID-only; the rule namespace must never be a metric filter.
	if strings.Contains(mt.GetFilter(), "openchoreo.dev/namespace") {
		t.Errorf("namespace must not be a metric filter clause: %s", mt.GetFilter())
	}
	if mt.GetDuration().AsDuration() != 60*time.Second {
		t.Errorf("duration = %v, want default 60s", mt.GetDuration().AsDuration())
	}
	if got := mt.GetAggregations()[0].GetAlignmentPeriod().AsDuration(); got != 300*time.Second {
		t.Errorf("alignment period = %v, want default 300s", got)
	}
}

func TestToAlertPolicyAllOperators(t *testing.T) {
	// MetricThreshold conditions support only COMPARISON_GT and COMPARISON_LT;
	// the remaining OpenChoreo operators are rejected (see the validation
	// errors test).
	cases := map[string]monitoringpb.ComparisonType{
		"gt": monitoringpb.ComparisonType_COMPARISON_GT,
		"lt": monitoringpb.ComparisonType_COMPARISON_LT,
	}
	for op, want := range cases {
		in := validRuleInput()
		in.Operator = op
		p, err := ToAlertPolicy(in, baseTranslatorCfg())
		if err != nil {
			t.Fatalf("operator %q: unexpected error: %v", op, err)
		}
		if got := p.GetConditions()[0].GetConditionThreshold().GetComparison(); got != want {
			t.Errorf("operator %q → %v, want %v", op, got, want)
		}
	}
}

func TestToAlertPolicyMemoryUsesMeanAligner(t *testing.T) {
	in := validRuleInput()
	in.Metric = "memory_usage"
	in.Threshold = 70 // percent
	p, err := ToAlertPolicy(in, baseTranslatorCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mt := p.GetConditions()[0].GetConditionThreshold()
	if mt.GetAggregations()[0].GetPerSeriesAligner() != monitoringpb.Aggregation_ALIGN_MEAN {
		t.Errorf("memory aligner = %v, want ALIGN_MEAN", mt.GetAggregations()[0].GetPerSeriesAligner())
	}
	if !strings.Contains(mt.GetFilter(), "memory/used_bytes") {
		t.Errorf("numerator filter missing memory usage metric: %s", mt.GetFilter())
	}
	if !strings.Contains(mt.GetDenominatorFilter(), "memory/limit_bytes") {
		t.Errorf("denominator filter missing memory limit metric: %s", mt.GetDenominatorFilter())
	}
	if mt.GetThresholdValue() != 0.7 {
		t.Errorf("threshold = %v, want 0.7", mt.GetThresholdValue())
	}
}

func TestToAlertPolicyValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RuleInput)
	}{
		{"missing namespace", func(in *RuleInput) { in.Namespace = "" }},
		{"missing name", func(in *RuleInput) { in.RuleName = "" }},
		{"missing metric", func(in *RuleInput) { in.Metric = "" }},
		{"unsupported metric", func(in *RuleInput) { in.Metric = "disk_usage" }},
		{"unsupported operator", func(in *RuleInput) { in.Operator = "between" }},
		{"operator gte unsupported by MetricThreshold", func(in *RuleInput) { in.Operator = "gte" }},
		{"operator lte unsupported by MetricThreshold", func(in *RuleInput) { in.Operator = "lte" }},
		{"operator eq unsupported by MetricThreshold", func(in *RuleInput) { in.Operator = "eq" }},
		{"operator neq unsupported by MetricThreshold", func(in *RuleInput) { in.Operator = "neq" }},
		{"bad interval", func(in *RuleInput) { in.Interval = "5x" }},
		{"non-minute interval", func(in *RuleInput) { in.Interval = "90s" }},
		{"negative window", func(in *RuleInput) { in.Window = "-5m" }},
	}
	for _, tc := range cases {
		in := validRuleInput()
		tc.mutate(&in)
		_, err := ToAlertPolicy(in, baseTranslatorCfg())
		if err == nil {
			t.Errorf("%s: expected error", tc.name)
			continue
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("%s: error %v is not ErrValidation", tc.name, err)
		}
	}
}

func TestToAlertPolicyCustomDurations(t *testing.T) {
	in := validRuleInput()
	in.Interval = "2m"
	in.Window = "10m"
	p, err := ToAlertPolicy(in, baseTranslatorCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mt := p.GetConditions()[0].GetConditionThreshold()
	if mt.GetDuration().AsDuration() != 2*time.Minute {
		t.Errorf("duration = %v, want 2m", mt.GetDuration().AsDuration())
	}
	if got := mt.GetAggregations()[0].GetAlignmentPeriod().AsDuration(); got != 10*time.Minute {
		t.Errorf("alignment period = %v, want 10m", got)
	}
}

func TestRuleHashStableAndDistinct(t *testing.T) {
	a := ruleHash("ns1", "r1")
	if a != ruleHash("ns1", "r1") {
		t.Errorf("hash not stable")
	}
	if a == ruleHash("ns2", "r1") || a == ruleHash("ns1", "r2") {
		t.Errorf("hash collides across identities")
	}
	// Guard against the classic delimiter-ambiguity bug.
	if ruleHash("a", "bc") == ruleHash("ab", "c") {
		t.Errorf("hash ignores namespace/name boundary")
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	if got := sanitizeLabelValue("Default/Ns"); got != "default_ns" {
		t.Errorf("sanitize = %q", got)
	}
	long := strings.Repeat("x", 100)
	if got := sanitizeLabelValue(long); len(got) != 63 {
		t.Errorf("sanitize length = %d, want 63", len(got))
	}
}
