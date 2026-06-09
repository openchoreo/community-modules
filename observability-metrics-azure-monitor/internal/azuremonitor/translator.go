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

type RuleInput struct {
	Namespace      string
	RuleName       string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
	Metric         string
	Query          string
	Operator       string // "gt" | "gte" | "lt" | "lte" | "eq"
	Threshold      float64
	Interval       string // ISO 8601 duration
	Window         string // ISO 8601 duration
	Enabled        bool
}

type TranslatorConfig struct {
	Region                     string
	WorkspaceResourceID        string
	ActionGroupID              string
	DefaultEvaluationFrequency string
	DefaultWindowSize          string
}

func ToScheduledQueryRule(in RuleInput, cfg TranslatorConfig) (*armmonitor.ScheduledQueryRuleResource, error) {
	if err := validate(in, cfg); err != nil {
		return nil, err
	}

	op, err := mapOperator(in.Operator)
	if err != nil {
		return nil, err
	}

	counter, err := MetricNameForSource(in.Metric)
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

	window, err = snapWindowToAzureGranularity(window)
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
					Query:               to.Ptr(BuildAlertKQL(in, counter)),
					TimeAggregation:     to.Ptr(armmonitor.TimeAggregationAverage),
					MetricMeasureColumn: to.Ptr("AggregatedValue"),
					Operator:            &op,
					Threshold:           &thresh,
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
	if strings.TrimSpace(in.Metric) == "" {
		return errors.New("rule metric is required")
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

// azureWindowGranularitiesMin lists the WindowSize values (in minutes) that
// Azure scheduledQueryRules accept. Azure returns this exact set in its
// InvalidRequestContent error when an unsupported window is submitted:
// "Supported granularities are: 1, 5, 10, 15, 30, 45, 60, 120, 180, 240, 300, 360".
var azureWindowGranularitiesMin = []int{1, 5, 10, 15, 30, 45, 60, 120, 180, 240, 300, 360}

// snapWindowToAzureGranularity rounds an ISO 8601 window duration UP to the
// nearest WindowSize Azure supports. Rounding up (rather than down or erroring)
// keeps the evaluation window at least as wide as the developer requested, so a
// "2m" window becomes "5m" instead of being rejected. Windows larger than the
// maximum supported granularity (6h) are clamped to it.
func snapWindowToAzureGranularity(iso string) (string, error) {
	mins, err := iso8601DurationToMinutes(iso)
	if err != nil {
		return "", err
	}
	if mins <= 0 {
		mins = azureWindowGranularitiesMin[0]
	}
	snapped := azureWindowGranularitiesMin[len(azureWindowGranularitiesMin)-1]
	for _, g := range azureWindowGranularitiesMin {
		if mins <= g {
			snapped = g
			break
		}
	}
	return goDurationToISO8601(time.Duration(snapped) * time.Minute), nil
}

// iso8601DurationToMinutes parses the compact ISO 8601 durations this package
// emits (PT#H#M#S form) and returns the total whole minutes, rounding any
// trailing seconds up to the next minute. It is intentionally narrow: it only
// needs to understand the output of goDurationToISO8601.
func iso8601DurationToMinutes(iso string) (int, error) {
	d, err := time.ParseDuration(isoToGoDuration(iso))
	if err != nil {
		return 0, fmt.Errorf("parse window %q: %w", iso, err)
	}
	mins := int(d / time.Minute)
	if d%time.Minute != 0 {
		mins++
	}
	return mins, nil
}

// isoToGoDuration rewrites a PT#H#M#S ISO 8601 duration into a Go duration
// string ("1h30m"). Only the time component (PT...) is handled, which is all
// goDurationToISO8601 produces.
func isoToGoDuration(iso string) string {
	s := strings.TrimSpace(iso)
	s = strings.TrimPrefix(s, "PT")
	s = strings.TrimPrefix(s, "pt")
	return strings.ToLower(s)
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
