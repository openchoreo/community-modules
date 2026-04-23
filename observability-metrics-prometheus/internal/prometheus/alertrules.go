// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"fmt"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// MetricTypeCPUUsage is the metric type for CPU usage alerts.
	MetricTypeCPUUsage = "cpu_usage"
	// MetricTypeMemoryUsage is the metric type for memory usage alerts.
	MetricTypeMemoryUsage = "memory_usage"
)

// AlertRuleParams contains the parameters needed to build a PrometheusRule CR.
type AlertRuleParams struct {
	Name           string
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
	Metric         string
	Enabled        bool
	Window         string
	Interval       string
	Operator       string
	Threshold      float64
}

// BuildPrometheusRule builds a PrometheusRule CR from alert rule parameters.
func BuildPrometheusRule(rule AlertRuleParams, namespace string) (*monitoringv1.PrometheusRule, error) {
	if rule.Metric != MetricTypeCPUUsage && rule.Metric != MetricTypeMemoryUsage {
		return nil, fmt.Errorf("unsupported metric type: %s (supported: %s, %s)", rule.Metric, MetricTypeCPUUsage, MetricTypeMemoryUsage)
	}

	expr, err := buildAlertExpression(rule)
	if err != nil {
		return nil, fmt.Errorf("failed to build alert expression: %w", err)
	}

	forDuration, err := parseDuration(rule.Window)
	if err != nil {
		return nil, fmt.Errorf("failed to parse window duration: %w", err)
	}

	intervalDuration, err := parseDuration(rule.Interval)
	if err != nil {
		return nil, fmt.Errorf("failed to parse interval duration: %w", err)
	}

	alertAnnotations := map[string]string{
		"rule_name":      rule.Name,
		"rule_namespace": rule.Namespace,
		"alert_value":    "{{ $value | printf \"%.2f\" }}",
	}

	alertLabels := map[string]string{
		"openchoreo_alert": "true",
	}

	interval := monitoringv1.Duration(intervalDuration)
	forDur := monitoringv1.Duration(forDuration)

	prometheusRule := &monitoringv1.PrometheusRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "monitoring.coreos.com/v1",
			Kind:       "PrometheusRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rule.Name,
			Namespace: namespace,
		},
		Spec: monitoringv1.PrometheusRuleSpec{
			Groups: []monitoringv1.RuleGroup{
				{
					Name:     rule.Name,
					Interval: &interval,
					Rules: []monitoringv1.Rule{
						{
							Alert:       rule.Name,
							Expr:        intstr.FromString(expr),
							For:         &forDur,
							Annotations: alertAnnotations,
							Labels:      alertLabels,
						},
					},
				},
			},
		},
	}

	return prometheusRule, nil
}

func buildAlertExpression(rule AlertRuleParams) (string, error) {
	operator, err := convertOperator(rule.Operator)
	if err != nil {
		return "", err
	}

	switch rule.Metric {
	case MetricTypeCPUUsage:
		return buildCPUUsageAlertExpression(rule.ComponentUID, rule.ProjectUID, rule.EnvironmentUID, operator, rule.Threshold, rule.Window), nil
	case MetricTypeMemoryUsage:
		return buildMemoryUsageAlertExpression(rule.ComponentUID, rule.ProjectUID, rule.EnvironmentUID, operator, rule.Threshold, rule.Window), nil
	default:
		return "", fmt.Errorf("unsupported metric type: %s", rule.Metric)
	}
}

// buildCPUUsageAlertExpression builds a PromQL expression for CPU usage alerts.
// The window parameter is used for the rate() calculation window. A minimum of 2m is enforced
// to ensure sufficient data points for rate calculation.
func buildCPUUsageAlertExpression(componentUID, projectUID, environmentUID, operator string, threshold float64, window string) string {
	labelFilter := BuildComponentLabelFilter(componentUID, projectUID, environmentUID)

	// Use the provided window, but ensure a minimum of 2m for rate calculation
	rateWindow := ensureMinimumWindow(window, "2m")

	return fmt.Sprintf(
		`(sum(rate(container_cpu_usage_seconds_total{container!=""}[%s]) * on (pod) group_left(label_openchoreo_dev_component_uid,label_openchoreo_dev_project_uid,label_openchoreo_dev_environment_uid) kube_pod_labels{%s}) / sum(kube_pod_container_resource_limits{resource="cpu"} * on (pod) group_left(label_openchoreo_dev_component_uid,label_openchoreo_dev_project_uid,label_openchoreo_dev_environment_uid) kube_pod_labels{%s})) * 100 %s %v`,
		rateWindow, labelFilter, labelFilter, operator, threshold,
	)
}

// buildMemoryUsageAlertExpression builds a PromQL expression for memory usage alerts.
// The window parameter is accepted for API consistency but not used, as memory usage
// is measured as an instant value rather than a rate over time.
func buildMemoryUsageAlertExpression(componentUID, projectUID, environmentUID, operator string, threshold float64, window string) string {
	labelFilter := BuildComponentLabelFilter(componentUID, projectUID, environmentUID)

	return fmt.Sprintf(
		`(sum(container_memory_working_set_bytes{container!=""} * on (pod) group_left(label_openchoreo_dev_component_uid,label_openchoreo_dev_project_uid,label_openchoreo_dev_environment_uid) kube_pod_labels{%s}) / sum(kube_pod_container_resource_limits{resource="memory"} * on (pod) group_left(label_openchoreo_dev_component_uid,label_openchoreo_dev_project_uid,label_openchoreo_dev_environment_uid) kube_pod_labels{%s})) * 100 %s %v`,
		labelFilter, labelFilter, operator, threshold,
	)
}

// convertOperator converts the alert rule operator to PromQL comparison operator.
func convertOperator(op string) (string, error) {
	switch op {
	case "gt":
		return ">", nil
	case "lt":
		return "<", nil
	case "gte":
		return ">=", nil
	case "lte":
		return "<=", nil
	case "eq":
		return "==", nil
	default:
		return "", fmt.Errorf("unsupported operator: %s", op)
	}
}

// parseDuration parses a duration string and returns it as-is if valid and positive.
func parseDuration(durationStr string) (string, error) {
	d, err := time.ParseDuration(durationStr)
	if err != nil {
		return "", fmt.Errorf("invalid duration format: %s", durationStr)
	}
	if d <= 0 {
		return "", fmt.Errorf("duration must be positive, got: %s", durationStr)
	}
	return durationStr, nil
}

// ensureMinimumWindow ensures the provided window is at least the specified minimum.
// This is important for rate() calculations which require sufficient data points.
func ensureMinimumWindow(window, minimum string) string {
	windowDur, err := time.ParseDuration(window)
	if err != nil {
		// If parsing fails, return the minimum as a safe fallback
		return minimum
	}

	minDur, err := time.ParseDuration(minimum)
	if err != nil {
		// If minimum parsing fails, return the window as-is
		return window
	}

	if windowDur < minDur {
		return minimum
	}
	return window
}
