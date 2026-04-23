// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"strings"
	"testing"
)

func TestBuildPrometheusRule_CPUUsage(t *testing.T) {
	params := AlertRuleParams{
		Name:           "test-cpu-alert",
		Namespace:      "test-ns",
		ComponentUID:   "c5f0a8d3-7e2b-4d9c-a1f4-6b8e3c0d5a7f",
		ProjectUID:     "d6a1b9e4-8f3c-4e0d-b2a5-7c9f4d1e6b8a",
		EnvironmentUID: "e7b2c0f5-9a4d-4f1e-c3b6-8d0a5e2f7c9b",
		Metric:         MetricTypeCPUUsage,
		Enabled:        true,
		Window:         "5m",
		Interval:       "1m",
		Operator:       "gt",
		Threshold:      80.0,
	}

	rule, err := BuildPrometheusRule(params, "monitoring")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rule.Name != "test-cpu-alert" {
		t.Errorf("expected name %q, got %q", "test-cpu-alert", rule.Name)
	}
	if rule.Namespace != "monitoring" {
		t.Errorf("expected namespace %q, got %q", "monitoring", rule.Namespace)
	}
	if len(rule.Spec.Groups) != 1 {
		t.Fatalf("expected 1 rule group, got %d", len(rule.Spec.Groups))
	}
	if len(rule.Spec.Groups[0].Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rule.Spec.Groups[0].Rules))
	}

	alertRule := rule.Spec.Groups[0].Rules[0]
	expr := alertRule.Expr.String()
	if !strings.Contains(expr, "container_cpu_usage_seconds_total") {
		t.Errorf("expected CPU usage metric in expression, got %q", expr)
	}
	if !strings.Contains(expr, "> 80") {
		t.Errorf("expected threshold in expression, got %q", expr)
	}
	if alertRule.Annotations["rule_name"] != "test-cpu-alert" {
		t.Errorf("expected rule_name annotation, got %q", alertRule.Annotations["rule_name"])
	}
	if alertRule.Annotations["rule_namespace"] != "test-ns" {
		t.Errorf("expected rule_namespace annotation, got %q", alertRule.Annotations["rule_namespace"])
	}
	if alertRule.Labels["openchoreo_alert"] != "true" {
		t.Errorf("expected openchoreo_alert label, got %q", alertRule.Labels["openchoreo_alert"])
	}
}

func TestBuildPrometheusRule_MemoryUsage(t *testing.T) {
	params := AlertRuleParams{
		Name:           "test-memory-alert",
		Namespace:      "test-ns",
		ComponentUID:   "c5f0a8d3-7e2b-4d9c-a1f4-6b8e3c0d5a7f",
		ProjectUID:     "d6a1b9e4-8f3c-4e0d-b2a5-7c9f4d1e6b8a",
		EnvironmentUID: "e7b2c0f5-9a4d-4f1e-c3b6-8d0a5e2f7c9b",
		Metric:         MetricTypeMemoryUsage,
		Enabled:        true,
		Window:         "10m",
		Interval:       "2m",
		Operator:       "gte",
		Threshold:      90.0,
	}

	rule, err := BuildPrometheusRule(params, "monitoring")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	alertRule := rule.Spec.Groups[0].Rules[0]
	expr := alertRule.Expr.String()
	if !strings.Contains(expr, "container_memory_working_set_bytes") {
		t.Errorf("expected memory usage metric in expression, got %q", expr)
	}
	if !strings.Contains(expr, ">= 90") {
		t.Errorf("expected threshold in expression, got %q", expr)
	}
}

func TestBuildPrometheusRule_UnsupportedMetric(t *testing.T) {
	params := AlertRuleParams{
		Name:     "test-invalid",
		Metric:   "unsupported_metric",
		Window:   "5m",
		Interval: "1m",
		Operator: "gt",
	}

	_, err := BuildPrometheusRule(params, "monitoring")
	if err == nil {
		t.Fatal("expected error for unsupported metric type")
	}
	if !strings.Contains(err.Error(), "unsupported metric type") {
		t.Errorf("expected unsupported metric type error, got: %v", err)
	}
}

func TestBuildPrometheusRule_InvalidWindow(t *testing.T) {
	params := AlertRuleParams{
		Name:     "test-invalid-window",
		Metric:   MetricTypeCPUUsage,
		Window:   "invalid",
		Interval: "1m",
		Operator: "gt",
	}

	_, err := BuildPrometheusRule(params, "monitoring")
	if err == nil {
		t.Fatal("expected error for invalid window duration")
	}
}

func TestBuildPrometheusRule_InvalidInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval string
	}{
		{"invalid format", "invalid"},
		{"empty interval", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := AlertRuleParams{
				Name:     "test-invalid-interval",
				Metric:   MetricTypeCPUUsage,
				Window:   "5m",
				Interval: tt.interval,
				Operator: "gt",
			}

			_, err := BuildPrometheusRule(params, "monitoring")
			if err == nil {
				t.Fatal("expected error for invalid interval duration")
			}
			if !strings.Contains(err.Error(), "failed to parse interval duration") {
				t.Errorf("expected interval duration error, got: %v", err)
			}
		})
	}
}

func TestBuildPrometheusRule_InvalidOperator(t *testing.T) {
	params := AlertRuleParams{
		Name:     "test-invalid-op",
		Metric:   MetricTypeCPUUsage,
		Window:   "5m",
		Interval: "1m",
		Operator: "invalid",
	}

	_, err := BuildPrometheusRule(params, "monitoring")
	if err == nil {
		t.Fatal("expected error for invalid operator")
	}
}

func TestConvertOperator(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"gt", ">", false},
		{"lt", "<", false},
		{"gte", ">=", false},
		{"lte", "<=", false},
		{"eq", "==", false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := convertOperator(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertOperator(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("convertOperator(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"5m", "5m", false},
		{"1h", "1h", false},
		{"30s", "30s", false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildCPUUsageAlertExpression_UsesWindow(t *testing.T) {
	tests := []struct {
		name           string
		window         string
		expectedWindow string // The window that should appear in the rate expression
	}{
		{
			name:           "5m window",
			window:         "5m",
			expectedWindow: "[5m]",
		},
		{
			name:           "10m window",
			window:         "10m",
			expectedWindow: "[10m]",
		},
		{
			name:           "1m window enforces minimum 2m",
			window:         "1m",
			expectedWindow: "[2m]", // Should be enforced to minimum of 2m
		},
		{
			name:           "30s window enforces minimum 2m",
			window:         "30s",
			expectedWindow: "[2m]", // Should be enforced to minimum of 2m
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := buildCPUUsageAlertExpression(
				"comp-uid",
				"proj-uid",
				"env-uid",
				">",
				80.0,
				tt.window,
			)

			if !strings.Contains(expr, tt.expectedWindow) {
				t.Errorf("expected expression to contain %q, got %q", tt.expectedWindow, expr)
			}

			// Ensure it contains the CPU metric
			if !strings.Contains(expr, "container_cpu_usage_seconds_total") {
				t.Errorf("expected expression to contain CPU metric, got %q", expr)
			}

			// Ensure it contains rate() function
			if !strings.Contains(expr, "rate(") {
				t.Errorf("expected expression to contain rate() function, got %q", expr)
			}
		})
	}
}

func TestEnsureMinimumWindow(t *testing.T) {
	tests := []struct {
		name     string
		window   string
		minimum  string
		expected string
	}{
		{
			name:     "window exceeds minimum",
			window:   "5m",
			minimum:  "2m",
			expected: "5m",
		},
		{
			name:     "window equals minimum",
			window:   "2m",
			minimum:  "2m",
			expected: "2m",
		},
		{
			name:     "window below minimum",
			window:   "1m",
			minimum:  "2m",
			expected: "2m",
		},
		{
			name:     "window below minimum in seconds",
			window:   "30s",
			minimum:  "2m",
			expected: "2m",
		},
		{
			name:     "invalid window returns minimum",
			window:   "invalid",
			minimum:  "2m",
			expected: "2m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureMinimumWindow(tt.window, tt.minimum)
			if got != tt.expected {
				t.Errorf("ensureMinimumWindow(%q, %q) = %q, want %q",
					tt.window, tt.minimum, got, tt.expected)
			}
		})
	}
}
