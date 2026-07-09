// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// ErrValidation wraps input errors that must surface as HTTP 400 rather than
// 500. Handlers test with errors.Is(err, ErrValidation).
var ErrValidation = errors.New("validation error")

func validationErrf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// User-label keys stamped on every managed alert policy. These identify a
// policy as OpenChoreo-managed and carry the (namespace, rule name) so the
// policy can be found again without relying on the (non-unique) display name.
const (
	policyLabelManagedBy      = "managed_by"
	policyLabelManagedByValue = "openchoreo"
	policyLabelNamespace      = "openchoreo_namespace"
	policyLabelRuleName       = "openchoreo_rule_name"

	// Cloud Monitoring user-label VALUES must match [a-z0-9_-]{0,63} and keys
	// are similarly constrained, so raw namespace/rule-name strings cannot be
	// stored verbatim. The de-facto identity therefore rides on a hash label
	// plus the human-readable values best-effort sanitised for display.
	policyLabelRuleHash = "openchoreo_rule_hash"
)

// RuleInput is the backend-neutral view of an alert rule, mapped from the
// generated AlertRuleRequest by the handler layer.
type RuleInput struct {
	Namespace      string
	RuleName       string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
	Metric         string  // cpu_usage | memory_usage
	Operator       string  // gt | gte | lt | lte | eq | neq
	Threshold      float64 // cores (cpu) or bytes (memory)
	Interval       string  // Go duration; condition duration
	Window         string  // Go duration; alignment period
	Enabled        bool
}

// TranslatorConfig carries the process-level defaults and the pre-configured
// notification channel attached to every managed policy.
type TranslatorConfig struct {
	ProjectID             string
	NotificationChannelID string
	DefaultInterval       time.Duration
	DefaultWindow         time.Duration
}

// ruleHash is the stable identity of a rule: sha256(namespace \x00 ruleName),
// hex-truncated. It is filter-safe (lowercase hex) and used both as the
// dedup user-label value and for FindRuleByName lookups.
func ruleHash(namespace, ruleName string) string {
	h := sha256.Sum256([]byte(namespace + "\x00" + ruleName))
	return hex.EncodeToString(h[:16])
}

// ToAlertPolicy renders a RuleInput as a Cloud Monitoring AlertPolicy scoped to
// the OpenChoreo identity labels. The condition is a MetricThreshold over the
// same GKE system metric the resource-metrics query uses, reusing the
// milestone-1 filter builder so scoping stays consistent.
func ToAlertPolicy(in RuleInput, cfg TranslatorConfig) (*monitoringpb.AlertPolicy, error) {
	if err := validateRule(in, cfg); err != nil {
		return nil, err
	}

	comparison, err := mapComparison(in.Operator)
	if err != nil {
		return nil, err
	}

	usageSpec, limitSpec, err := alertMetricSpecs(in.Metric)
	if err != nil {
		return nil, err
	}

	interval, err := durationOrDefault(in.Interval, cfg.DefaultInterval)
	if err != nil {
		return nil, fmt.Errorf("%w: interval: %v", ErrValidation, err)
	}
	window, err := durationOrDefault(in.Window, cfg.DefaultWindow)
	if err != nil {
		return nil, fmt.Errorf("%w: window: %v", ErrValidation, err)
	}

	scope := MetricsQueryParams{
		Namespace:      in.Namespace,
		ComponentUID:   in.ComponentUID,
		ProjectUID:     in.ProjectUID,
		EnvironmentUID: in.EnvironmentUID,
	}

	// cpu_usage / memory_usage thresholds are a PERCENTAGE of the pod's
	// limit (e.g. threshold 80 == "usage > 80% of limit"), matching the
	// OpenChoreo alert semantics used by the Azure/AWS siblings. Cloud
	// Monitoring's MetricThreshold computes usage÷limit natively via the
	// denominator filter, so the threshold is converted percent→fraction
	// (80 → 0.80) and compared against the ratio.
	numeratorFilter := BuildResourceMetricsFilter(usageSpec, scope)
	denominatorFilter := BuildResourceMetricsFilter(limitSpec, scope)
	alignPeriod := durationpb.New(alignmentPeriod(window))

	condition := &monitoringpb.AlertPolicy_Condition{
		DisplayName: fmt.Sprintf("%s/%s", in.Namespace, in.RuleName),
		Condition: &monitoringpb.AlertPolicy_Condition_ConditionThreshold{
			ConditionThreshold: &monitoringpb.AlertPolicy_Condition_MetricThreshold{
				Filter:         numeratorFilter,
				Comparison:     comparison,
				ThresholdValue: in.Threshold / 100.0,
				Duration:       durationpb.New(interval),
				Aggregations: []*monitoringpb.Aggregation{{
					AlignmentPeriod:    alignPeriod,
					PerSeriesAligner:   usageSpec.aligner,
					CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_SUM,
				}},
				DenominatorFilter: denominatorFilter,
				DenominatorAggregations: []*monitoringpb.Aggregation{{
					AlignmentPeriod:    alignPeriod,
					PerSeriesAligner:   limitSpec.aligner,
					CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_SUM,
				}},
				Trigger: &monitoringpb.AlertPolicy_Condition_Trigger{
					Type: &monitoringpb.AlertPolicy_Condition_Trigger_Count{Count: 1},
				},
			},
		},
	}

	policy := &monitoringpb.AlertPolicy{
		DisplayName: fmt.Sprintf("%s/%s", in.Namespace, in.RuleName),
		Combiner:    monitoringpb.AlertPolicy_OR,
		Enabled:     &wrapperspb.BoolValue{Value: in.Enabled},
		Conditions:  []*monitoringpb.AlertPolicy_Condition{condition},
		UserLabels:  policyUserLabels(in),
	}
	if cfg.NotificationChannelID != "" {
		policy.NotificationChannels = []string{cfg.NotificationChannelID}
	}
	return policy, nil
}

// policyUserLabels stamps the identity labels used for dedup and lookup. UID
// labels are only added when non-zero, mirroring the query-scoping behaviour.
func policyUserLabels(in RuleInput) map[string]string {
	labels := map[string]string{
		policyLabelManagedBy: policyLabelManagedByValue,
		policyLabelRuleHash:  ruleHash(in.Namespace, in.RuleName),
		policyLabelNamespace: sanitizeLabelValue(in.Namespace),
		policyLabelRuleName:  sanitizeLabelValue(in.RuleName),
	}
	if uid := normalizeUID(in.ComponentUID); uid != "" {
		labels["openchoreo_component_uid"] = sanitizeLabelValue(uid)
	}
	if uid := normalizeUID(in.ProjectUID); uid != "" {
		labels["openchoreo_project_uid"] = sanitizeLabelValue(uid)
	}
	if uid := normalizeUID(in.EnvironmentUID); uid != "" {
		labels["openchoreo_environment_uid"] = sanitizeLabelValue(uid)
	}
	return labels
}

func validateRule(in RuleInput, cfg TranslatorConfig) error {
	if strings.TrimSpace(in.Namespace) == "" {
		return validationErrf("metadata.namespace is required")
	}
	if strings.TrimSpace(in.RuleName) == "" {
		return validationErrf("metadata.name is required")
	}
	if strings.TrimSpace(in.Metric) == "" {
		return validationErrf("source.metric is required")
	}
	if cfg.ProjectID == "" {
		return errors.New("projectID is required") // config error, not user input → 500
	}
	return nil
}

// mapComparison maps an OpenChoreo operator to the Cloud Monitoring enum.
// Unlike CloudWatch (which rejects eq/neq), Cloud Monitoring supports all six.
func mapComparison(op string) (monitoringpb.ComparisonType, error) {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "gt", ">":
		return monitoringpb.ComparisonType_COMPARISON_GT, nil
	case "gte", ">=":
		return monitoringpb.ComparisonType_COMPARISON_GE, nil
	case "lt", "<":
		return monitoringpb.ComparisonType_COMPARISON_LT, nil
	case "lte", "<=":
		return monitoringpb.ComparisonType_COMPARISON_LE, nil
	case "eq", "==":
		return monitoringpb.ComparisonType_COMPARISON_EQ, nil
	case "neq", "!=":
		return monitoringpb.ComparisonType_COMPARISON_NE, nil
	default:
		return monitoringpb.ComparisonType_COMPARISON_UNSPECIFIED,
			validationErrf("unsupported operator %q (expected gt|gte|lt|lte|eq|neq)", op)
	}
}

// alertMetricSpecs resolves the source metric to the (usage, limit) metricSpec
// pair, reusing the milestone-1 specs so the alert numerator/denominator
// filters and aligners match the query path exactly. The threshold is compared
// against usage÷limit.
func alertMetricSpecs(metric string) (usage, limit metricSpec, err error) {
	switch strings.ToLower(strings.TrimSpace(metric)) {
	case "cpu_usage":
		return specByKeyOrPanic("cpuUsage"), specByKeyOrPanic("cpuLimits"), nil
	case "memory_usage":
		return specByKeyOrPanic("memoryUsage"), specByKeyOrPanic("memoryLimits"), nil
	default:
		return metricSpec{}, metricSpec{}, validationErrf("unsupported source.metric %q (expected cpu_usage|memory_usage)", metric)
	}
}

// specByKeyOrPanic returns the resource metric spec for a key. The keys are
// compile-time constants from resourceMetricSpecs, so a miss is a programmer
// error, not a runtime condition.
func specByKeyOrPanic(key string) metricSpec {
	for _, s := range resourceMetricSpecs {
		if s.key == key {
			return s
		}
	}
	panic("unknown metric spec key: " + key)
}

func durationOrDefault(s string, def time.Duration) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration %q must be positive", s)
	}
	return d, nil
}

// sanitizeLabelValue coerces an arbitrary string into a Cloud Monitoring
// user-label value: lowercase, [a-z0-9_-], max 63 chars. Values that cannot be
// represented are best-effort mangled; the authoritative identity lives in the
// rule-hash label, so a lossy display value here is acceptable.
func sanitizeLabelValue(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}
