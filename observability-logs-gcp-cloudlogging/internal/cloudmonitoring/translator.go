// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	monitoringpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// RuleInput is the adapter-internal shape a handler passes into the CRUD
// layer, decoded from the generated AlertRuleRequest.
type RuleInput struct {
	Namespace      string
	RuleName       string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string

	Query     string  // free-text phrase or a raw Cloud Logging filter
	Operator  string  // gt|gte|lt|lte|eq|neq
	Threshold float64 // compared against the count over the window
	Window    string  // rolling window (ISO 8601 or Go duration)
	Enabled   bool
}

// RuleResult is what the CRUD layer returns to the handler.
type RuleResult struct {
	BackendID  string // full alert-policy resource name
	LogicalID  string // deterministic oc-<hash> anchor
	LastSynced string // RFC3339 timestamp
}

// NowRFC3339 is a thin wrapper kept here so tests can stub time.
var NowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }

// metricType returns the Cloud Monitoring metric type for a log-based metric
// ID (user-defined metrics live under the logging.googleapis.com/user prefix).
func metricType(metricID string) string {
	return "logging.googleapis.com/user/" + metricID
}

// buildAlertPolicy assembles the AlertPolicy proto for a rule. The condition
// is a metric-threshold over the log-based counter metric derived from the
// rule, aligned as a rate/count over the window, wired to the notification
// channel, and tagged with the OpenChoreo user_labels for later lookup.
func buildAlertPolicy(in RuleInput, cfg Config, metricID string) (*monitoringpb.AlertPolicy, error) {
	cmp, err := mapOperator(in.Operator)
	if err != nil {
		return nil, err
	}

	window, err := toDuration(in.Window)
	if err != nil {
		return nil, fmt.Errorf("window: %w", err)
	}

	filter := fmt.Sprintf(`resource.type="k8s_container" AND metric.type="%s"`, metricType(metricID))

	// A log-based counter metric is a DELTA whose value per interval already IS
	// the number of matching log entries. To alert on "how many matches in the
	// window", SUM those per-interval counts over the alignment period
	// (ALIGN_SUM), and SUM across series (e.g. multiple pods) so the condition
	// evaluates a single total. ALIGN_COUNT would count the number of sample
	// points, not the matches — the wrong quantity.
	//
	// Duration is 0: fire as soon as the summed count over the window breaches
	// the threshold. The window is already expressed by the alignment period;
	// a non-zero duration would additionally require the breach to persist for
	// that long, doubling the effective delay.
	condition := &monitoringpb.AlertPolicy_Condition{
		DisplayName: in.RuleName,
		Condition: &monitoringpb.AlertPolicy_Condition_ConditionThreshold{
			ConditionThreshold: &monitoringpb.AlertPolicy_Condition_MetricThreshold{
				Filter:         filter,
				Comparison:     cmp,
				ThresholdValue: in.Threshold,
				Duration:       durationpb.New(0),
				Aggregations: []*monitoringpb.Aggregation{{
					AlignmentPeriod:    window,
					PerSeriesAligner:   monitoringpb.Aggregation_ALIGN_SUM,
					CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_SUM,
				}},
			},
		},
	}

	policy := &monitoringpb.AlertPolicy{
		DisplayName: in.RuleName,
		Combiner:    monitoringpb.AlertPolicy_OR,
		Enabled:     &wrapperspb.BoolValue{Value: in.Enabled},
		Conditions:  []*monitoringpb.AlertPolicy_Condition{condition},
		UserLabels: map[string]string{
			UserLabelManagedBy: ManagedByValue,
			UserLabelNamespace: sanitizeLabelValue(in.Namespace),
			UserLabelRuleName:  sanitizeLabelValue(in.RuleName),
			UserLabelRuleID:    deriveResourceName(in.Namespace, in.RuleName),
		},
	}
	if cfg.NotificationChannelID != "" {
		policy.NotificationChannels = []string{cfg.NotificationChannelID}
	}
	return policy, nil
}

// mapOperator converts an OpenChoreo operator string to the Cloud Monitoring
// comparison enum. Per the OpenAPI contract operators are gt|gte|lt|lte|eq|neq.
func mapOperator(op string) (monitoringpb.ComparisonType, error) {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "gt", "greaterthan", ">":
		return monitoringpb.ComparisonType_COMPARISON_GT, nil
	case "gte", "greaterthanorequal", ">=":
		return monitoringpb.ComparisonType_COMPARISON_GE, nil
	case "lt", "lessthan", "<":
		return monitoringpb.ComparisonType_COMPARISON_LT, nil
	case "lte", "lessthanorequal", "<=":
		return monitoringpb.ComparisonType_COMPARISON_LE, nil
	case "eq", "equals", "=", "==":
		return monitoringpb.ComparisonType_COMPARISON_EQ, nil
	case "neq", "notequal", "!=", "<>":
		return monitoringpb.ComparisonType_COMPARISON_NE, nil
	default:
		return monitoringpb.ComparisonType_COMPARISON_UNSPECIFIED,
			fmt.Errorf("unsupported operator %q (expected gt|gte|lt|lte|eq|neq)", op)
	}
}

// toDuration normalizes a duration string to a protobuf Duration. Accepts
// ISO 8601 (PT5M, PT1H30M, P1D) and Go-style (5m, 1h30m, 90s). The Logs
// Adapter API marks the window as required, so an empty value is a request
// error rather than something to paper over with a default.
func toDuration(s string) (*durationpb.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("required, must be an ISO 8601 or Go duration")
	}
	d, err := parseFlexibleDuration(s)
	if err != nil {
		return nil, err
	}
	return durationpb.New(d), nil
}

// parseFlexibleDuration parses either an ISO 8601 time duration (PnDTnHnMnS,
// no year/month components) or a Go duration string.
func parseFlexibleDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if s[0] == 'P' || s[0] == 'p' {
		return parseISO8601Duration(s)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", s, err)
	}
	return d, nil
}

// parseISO8601Duration parses the time-oriented subset of ISO 8601 durations
// the OpenChoreo alert CR uses: an optional day component plus hours/minutes/
// seconds (e.g. PT5M, PT1H30M, P1DT2H, PT30S). Year/month are not supported
// (they are not expressible as a fixed time.Duration).
func parseISO8601Duration(s string) (time.Duration, error) {
	orig := s
	s = strings.ToUpper(s)
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q", orig)
	}
	s = s[1:] // drop leading P

	var total time.Duration
	datePart, timePart := s, ""
	if i := strings.IndexByte(s, 'T'); i >= 0 {
		datePart, timePart = s[:i], s[i+1:]
	}

	// Date part: only days (D) are supported here.
	if datePart != "" {
		if strings.ContainsAny(datePart, "YM") {
			return 0, fmt.Errorf("unsupported year/month component in duration %q", orig)
		}
		if d, ok := readUnit(&datePart, 'D'); ok {
			total += time.Duration(d) * 24 * time.Hour
		}
		if datePart != "" {
			return 0, fmt.Errorf("invalid ISO 8601 duration %q", orig)
		}
	}

	// Time part: hours (H), minutes (M), seconds (S).
	if h, ok := readUnit(&timePart, 'H'); ok {
		total += time.Duration(h) * time.Hour
	}
	if m, ok := readUnit(&timePart, 'M'); ok {
		total += time.Duration(m) * time.Minute
	}
	if sec, ok := readUnit(&timePart, 'S'); ok {
		total += time.Duration(sec) * time.Second
	}
	if timePart != "" {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q", orig)
	}
	if total <= 0 {
		return 0, fmt.Errorf("non-positive duration %q", orig)
	}
	return total, nil
}

// readUnit consumes leading digits followed by unit from *s. If the next
// component matches unit, it advances *s past it and returns (value, true).
func readUnit(s *string, unit byte) (int64, bool) {
	str := *s
	i := 0
	for i < len(str) && str[i] >= '0' && str[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(str) || str[i] != unit {
		return 0, false
	}
	var v int64
	for _, c := range str[:i] {
		v = v*10 + int64(c-'0')
	}
	*s = str[i+1:]
	return v, true
}

// sanitizeLabelValue makes a value safe for a GCP user_label. Values must be
// <=63 bytes; we truncate on a rune boundary so a multi-byte character is
// never split (GCP rejects labels containing invalid UTF-8).
func sanitizeLabelValue(v string) string {
	if len(v) <= 63 {
		return v
	}
	// Truncate to the largest rune boundary at or below 63 bytes.
	cut := 63
	for cut > 0 && !utf8.RuneStart(v[cut]) {
		cut--
	}
	return v[:cut]
}

// escapeFilterValue escapes a value for safe interpolation inside a
// double-quoted GCP list filter string (used for ListAlertPolicies filters).
// Without this, a value containing a quote or a boolean operator like
// `x" OR user_labels.managed-by="openchoreo` would break out of the quoted
// literal and broaden the match. Backslashes and quotes are escaped; newlines
// are flattened.
func escapeFilterValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.ReplaceAll(v, "\r", " ")
	return v
}
