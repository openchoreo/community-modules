// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"strings"
	"testing"
	"time"

	monitoringpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

func TestMapOperator(t *testing.T) {
	cases := map[string]monitoringpb.ComparisonType{
		"gt":  monitoringpb.ComparisonType_COMPARISON_GT,
		"gte": monitoringpb.ComparisonType_COMPARISON_GE,
		"lt":  monitoringpb.ComparisonType_COMPARISON_LT,
		"lte": monitoringpb.ComparisonType_COMPARISON_LE,
		"eq":  monitoringpb.ComparisonType_COMPARISON_EQ,
		"neq": monitoringpb.ComparisonType_COMPARISON_NE,
	}
	for op, want := range cases {
		got, err := mapOperator(op)
		if err != nil || got != want {
			t.Errorf("mapOperator(%q) = %v, %v; want %v", op, got, err, want)
		}
	}
	if _, err := mapOperator("bogus"); err == nil {
		t.Error("expected error for unsupported operator")
	}
}

func TestParseFlexibleDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"PT5M":    5 * time.Minute,
		"PT1H30M": 90 * time.Minute,
		"PT30S":   30 * time.Second,
		"P1D":     24 * time.Hour,
		"P1DT2H":  26 * time.Hour,
		"5m":      5 * time.Minute,
		"1h30m":   90 * time.Minute,
		"90s":     90 * time.Second,
	}
	for in, want := range cases {
		got, err := parseFlexibleDuration(in)
		if err != nil {
			t.Errorf("parseFlexibleDuration(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseFlexibleDuration(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "P1Y", "P1M", "PT", "garbage"} {
		if _, err := parseFlexibleDuration(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestToDuration(t *testing.T) {
	d, err := toDuration("PT5M")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.AsDuration() != 5*time.Minute {
		t.Errorf("toDuration(PT5M) = %v, want 5m", d.AsDuration())
	}
	// Empty window is a request error, not a silently-defaulted value.
	if _, err := toDuration(""); err == nil {
		t.Error("expected error for empty window")
	}
}

func TestBuildAlertPolicy(t *testing.T) {
	in := RuleInput{
		Namespace:    "default",
		RuleName:     "too-many-errors",
		ComponentUID: "846bb581-d6ba-446d-a160-8cec468d2219",
		Query:        "panic",
		Operator:     "gt",
		Threshold:    5,
		Window:       "PT5M",
		Enabled:      true,
	}
	cfg := Config{ProjectID: "p", NotificationChannelID: "projects/p/notificationChannels/1"}
	metricID := deriveResourceName(in.Namespace, in.RuleName)

	policy, err := buildAlertPolicy(in, cfg, metricID)
	if err != nil {
		t.Fatalf("buildAlertPolicy error: %v", err)
	}
	if policy.GetDisplayName() != "too-many-errors" {
		t.Errorf("displayName = %q", policy.GetDisplayName())
	}
	if policy.GetUserLabels()[UserLabelNamespace] != "default" || policy.GetUserLabels()[UserLabelRuleName] != "too-many-errors" {
		t.Errorf("user_labels not set: %v", policy.GetUserLabels())
	}
	if len(policy.GetConditions()) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(policy.GetConditions()))
	}
	mt := policy.GetConditions()[0].GetConditionThreshold()
	if mt == nil {
		t.Fatal("expected a metric-threshold condition")
	}
	if mt.GetComparison() != monitoringpb.ComparisonType_COMPARISON_GT {
		t.Errorf("comparison = %v", mt.GetComparison())
	}
	if mt.GetThresholdValue() != 5 {
		t.Errorf("threshold = %v", mt.GetThresholdValue())
	}
	if len(policy.GetNotificationChannels()) != 1 {
		t.Errorf("expected notification channel wired, got %v", policy.GetNotificationChannels())
	}
}

func TestDeriveResourceName_Deterministic(t *testing.T) {
	a := deriveResourceName("default", "rule1")
	b := deriveResourceName("default", "rule1")
	c := deriveResourceName("default", "rule2")
	if a != b {
		t.Error("same identity must yield the same name")
	}
	if a == c {
		t.Error("different rule must yield a different name")
	}
	if len(a) < 4 || a[:3] != "oc-" {
		t.Errorf("expected oc- prefix, got %q", a)
	}
}

func TestValidateWindow(t *testing.T) {
	ok := []string{"PT1M", "1m", "5m", "PT1H", "90s"}
	for _, w := range ok {
		if err := ValidateWindow(w); err != nil {
			t.Errorf("ValidateWindow(%q) = %v, want nil", w, err)
		}
	}
	bad := []string{"", "30s", "PT30S", "59s", "garbage"}
	for _, w := range bad {
		if err := ValidateWindow(w); err == nil {
			t.Errorf("ValidateWindow(%q) = nil, want error", w)
		}
	}
}

func TestLabelRuleName(t *testing.T) {
	// Two names sharing an identical 63-byte prefix but differing afterwards
	// must map to DIFFERENT label values (the truncation-collision #4 fixes).
	prefix := (strings.Repeat("a", 63))[:63]
	a := labelRuleName(prefix + "-primary")
	b := labelRuleName(prefix + "-secondary")
	if a == b {
		t.Errorf("colliding-prefix names produced identical label: %q", a)
	}

	// Deterministic and GCP-safe: <=63 bytes, stable across calls.
	if labelRuleName("some-rule") != labelRuleName("some-rule") {
		t.Error("labelRuleName must be deterministic")
	}
	for _, n := range []string{"short", prefix + "-primary", strings.Repeat("z", 300)} {
		if got := labelRuleName(n); len(got) > 63 {
			t.Errorf("labelRuleName(%q) = %q (len %d > 63)", n, got, len(got))
		}
	}
}
