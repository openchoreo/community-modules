// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// RuleInput is the adapter's internal, decoupled representation of an
// alert rule. The generated OpenAPI types are mapped to this shape in
// the handler layer to keep this package free of OpenAPI imports.
type RuleInput struct {
	Namespace      string
	RuleName       string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string

	Query    string
	Operator string  // "gt" | "gte" | "lt" | "lte" | "eq"
	Threshold float64
	Interval string  // ISO 8601 duration
	Window   string  // ISO 8601 duration
	Enabled  bool
}

// TranslatorConfig holds settings that don't vary per rule.
type TranslatorConfig struct {
	Region                     string
	WorkspaceResourceID        string
	ActionGroupID              string
	DefaultEvaluationFrequency string
	DefaultWindowSize          string
}

// ToScheduledQueryRule converts a RuleInput into the SDK resource. Defaults
// are applied for evaluationFrequency and windowSize when empty.
func ToScheduledQueryRule(in RuleInput, cfg TranslatorConfig) (*armmonitor.ScheduledQueryRuleResource, error) {
	if err := validate(in, cfg); err != nil {
		return nil, err
	}

	op, err := mapOperator(in.Operator)
	if err != nil {
		return nil, err
	}

	freq, err := toISO8601Duration(in.Interval, cfg.DefaultEvaluationFrequency)
	if err != nil {
		return nil, fmt.Errorf("interval: %w", err)
	}
	window, err := toISO8601Duration(in.Window, cfg.DefaultWindowSize)
	if err != nil {
		return nil, fmt.Errorf("window: %w", err)
	}

	severity := armmonitor.AlertSeverityThree

	kind := armmonitor.KindLogAlert

	thresh := in.Threshold

	customProps := map[string]*string{
		CustomPropOpenChoreoNamespace: to.Ptr(in.Namespace),
		CustomPropOpenChoreoRuleName:  to.Ptr(in.RuleName),
	}
	if uid := normaliseUID(in.ComponentUID); uid != "" {
		customProps["openchoreo-component-uid"] = to.Ptr(uid)
	}
	if uid := normaliseUID(in.ProjectUID); uid != "" {
		customProps["openchoreo-project-uid"] = to.Ptr(uid)
	}
	if uid := normaliseUID(in.EnvironmentUID); uid != "" {
		customProps["openchoreo-environment-uid"] = to.Ptr(uid)
	}

	res := &armmonitor.ScheduledQueryRuleResource{
		Location: to.Ptr(cfg.Region),
		Kind:     &kind,
		Properties: &armmonitor.ScheduledQueryRuleProperties{
			DisplayName:         to.Ptr(fmt.Sprintf("%s/%s", in.Namespace, in.RuleName)),
			Enabled:             to.Ptr(in.Enabled),
			Severity:            &severity,
			Scopes:              []*string{to.Ptr(cfg.WorkspaceResourceID)},
			EvaluationFrequency: to.Ptr(freq),
			WindowSize:          to.Ptr(window),
			Criteria: &armmonitor.ScheduledQueryRuleCriteria{
				AllOf: []*armmonitor.Condition{{
					Query:           to.Ptr(BuildAlertKQL(in)),
					TimeAggregation: to.Ptr(armmonitor.TimeAggregationCount),
					Operator:        &op,
					Threshold:       &thresh,
				}},
			},
			Actions: &armmonitor.Actions{
				ActionGroups:     []*string{to.Ptr(cfg.ActionGroupID)},
				CustomProperties: customProps,
			},
		},
		Tags: map[string]*string{
			"managed-by":           to.Ptr("openchoreo"),
			"openchoreo-namespace": to.Ptr(in.Namespace),
			"openchoreo-rule-name": to.Ptr(in.RuleName),
		},
	}
	return res, nil
}

func validate(in RuleInput, cfg TranslatorConfig) error {
	if strings.TrimSpace(in.Namespace) == "" {
		return errors.New("rule namespace is required")
	}
	if strings.TrimSpace(in.RuleName) == "" {
		return errors.New("rule name is required")
	}
	if strings.TrimSpace(in.Query) == "" {
		return errors.New("rule query is required")
	}
	if cfg.WorkspaceResourceID == "" {
		return errors.New("workspaceResourceID is required")
	}
	if cfg.ActionGroupID == "" {
		return errors.New("actionGroupID is required")
	}
	if cfg.Region == "" {
		return errors.New("region is required")
	}
	return nil
}

// mapOperator converts an OpenChoreo operator string to the Azure enum.
// Per the OpenAPI contract operator values are gt|gte|lt|lte|eq.
func mapOperator(op string) (armmonitor.ConditionOperator, error) {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "gt", "greaterthan", ">":
		return armmonitor.ConditionOperatorGreaterThan, nil
	case "gte", "greaterthanorequal", ">=":
		return armmonitor.ConditionOperatorGreaterThanOrEqual, nil
	case "lt", "lessthan", "<":
		return armmonitor.ConditionOperatorLessThan, nil
	case "lte", "lessthanorequal", "<=":
		return armmonitor.ConditionOperatorLessThanOrEqual, nil
	case "eq", "equals", "=", "==":
		return armmonitor.ConditionOperatorEquals, nil
	default:
		return "", fmt.Errorf("unsupported operator %q (expected gt|gte|lt|lte|eq)", op)
	}
}

// NowRFC3339 is a thin wrapper kept here so tests can stub time.
var NowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }

// toISO8601Duration normalizes a duration string to ISO 8601 (e.g. PT5M).
// Accepts:
//   - Already ISO 8601: "PT5M", "PT1H", "P1D" → passed through
//   - Go-style: "5m", "1h", "2h30m", "90s" → converted via time.ParseDuration
//   - Empty: returns the supplied fallback (already ISO 8601)
//
// Azure's scheduledQueryRules.evaluationFrequency and .windowSize fields
// require ISO 8601. OpenChoreo's ObservabilityAlertRule CR carries Go-style
// duration strings, so the adapter must convert.
func toISO8601Duration(s, fallback string) (string, error) {
	if s == "" {
		return fallback, nil
	}
	// Already ISO 8601 (starts with P or PT).
	if len(s) >= 2 && (s[0] == 'P' || s[0] == 'p') {
		return s, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return "", fmt.Errorf("parse duration %q: %w", s, err)
	}
	return goDurationToISO8601(d), nil
}

// goDurationToISO8601 formats a time.Duration as a compact ISO 8601 duration.
// Examples: 1m → PT1M, 5m → PT5M, 1h30m → PT1H30M, 2h → PT2H, 24h → PT24H.
// Days are not produced (P1D etc.) because Azure schedule durations are
// time-of-day scoped; PT24H is equally valid and easier to map back.
func goDurationToISO8601(d time.Duration) string {
	if d <= 0 {
		return "PT0S"
	}
	hours := int64(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int64(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	seconds := int64(d / time.Second)

	out := "PT"
	if hours > 0 {
		out += fmt.Sprintf("%dH", hours)
	}
	if minutes > 0 {
		out += fmt.Sprintf("%dM", minutes)
	}
	if seconds > 0 {
		out += fmt.Sprintf("%dS", seconds)
	}
	if out == "PT" {
		out = "PT0S"
	}
	return out
}
