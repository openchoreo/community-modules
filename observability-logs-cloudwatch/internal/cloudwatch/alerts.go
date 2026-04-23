// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// ErrAlertNotFound signals that either the metric filter or the metric alarm
// for a given logical rule name was not present in CloudWatch.
var ErrAlertNotFound = errors.New("alert not found")

// LogAlertParams is the input model the adapter passes into the CRUD layer.
type LogAlertParams struct {
	Name           string
	Namespace      string
	ProjectUID     string
	EnvironmentUID string
	ComponentUID   string
	SearchPattern  string
	Operator       string // gt|gte|lt|lte|eq|neq
	Threshold      float64
	Window         time.Duration
	Interval       time.Duration
	Enabled        bool
}

// AlertDetail is the adapter's reconstructed view of an existing CloudWatch alarm.
type AlertDetail struct {
	Name           string
	Namespace      string
	ProjectUID     string
	EnvironmentUID string
	ComponentUID   string
	SearchPattern  string
	Operator       string
	Threshold      float64
	Window         time.Duration
	Interval       time.Duration
	Enabled        bool
	AlarmARN       string
}

// AlertResourceNames holds the deterministic CloudWatch resource identifiers
// that the adapter derives from `namespace/name`.
type AlertResourceNames struct {
	MetricFilterName string
	AlarmName        string
	MetricName       string
}

const (
	alertAlarmPrefix             = "oc-logs-alert-"
	alertMetricPrefix            = "oc_logs_alert_"
	maxCloudWatchResourceNameLen = 255
)

var alertIdentityEncoding = base64.RawURLEncoding

// BuildAlertResourceNames produces deterministic, AWS-safe identifiers that
// carry the rule namespace + name in the alarm/filter name itself so an
// EventBridge state-change event can be mapped back to the originating rule
// without a second AWS lookup. Dots are used as separators because they are
// outside the base64url alphabet and keep parsing unambiguous.
func BuildAlertResourceNames(namespace, name string) AlertResourceNames {
	nsEnc := encodeAlertIdentitySegment(namespace)
	nameEnc := encodeAlertIdentitySegment(name)
	h := sha256.Sum256([]byte(namespace + "\x00" + name))
	short := hex.EncodeToString(h[:])[:12]
	alarm := fmt.Sprintf("%sns.%s.rn.%s.%s", alertAlarmPrefix, nsEnc, nameEnc, short)
	return AlertResourceNames{
		MetricFilterName: alarm,
		AlarmName:        alarm,
		MetricName:       alertMetricPrefix + short,
	}
}

// ParseAlertIdentityFromAlarmName recovers the OpenChoreo rule namespace/name
// from the CloudWatch alarm name emitted by BuildAlertResourceNames.
func ParseAlertIdentityFromAlarmName(alarmName string) (string, string, error) {
	rest, ok := strings.CutPrefix(alarmName, alertAlarmPrefix)
	if !ok {
		return "", "", fmt.Errorf("alarm name %q does not use the managed prefix", alarmName)
	}

	parts := strings.Split(rest, ".")
	if len(parts) != 5 || parts[0] != "ns" || parts[2] != "rn" || parts[4] == "" {
		return "", "", fmt.Errorf("alarm name %q does not match the managed base64url format", alarmName)
	}

	namespace, err := decodeAlertIdentitySegment(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("decode namespace from alarm name %q: %w", alarmName, err)
	}
	name, err := decodeAlertIdentitySegment(parts[3])
	if err != nil {
		return "", "", fmt.Errorf("decode rule name from alarm name %q: %w", alarmName, err)
	}
	return namespace, name, nil
}

// Tag keys on the CloudWatch alarm used to round-trip OpenChoreo metadata.
const (
	TagRuleName       = "openchoreo.rule.name"
	TagRuleNamespace  = "openchoreo.rule.namespace"
	TagProjectUID     = "openchoreo.project.uid"
	TagEnvironmentUID = "openchoreo.environment.uid"
	TagComponentUID   = "openchoreo.component.uid"
	TagManagedBy      = "openchoreo.managed-by"
	TagManagedByValue = "observability-logs-cloudwatch"
	TagSearchPattern  = "openchoreo.rule.searchpattern"
	TagOperator       = "openchoreo.rule.operator"
)

// ValidateAlertParams sanity-checks the inputs before any AWS call is made.
func ValidateAlertParams(p LogAlertParams) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("invalid: rule name is required")
	}
	if strings.TrimSpace(p.Namespace) == "" {
		return errors.New("invalid: rule namespace is required")
	}
	if p.Interval <= 0 || p.Window <= 0 {
		return errors.New("invalid: window and interval must be positive durations")
	}
	if p.Window < p.Interval {
		return errors.New("invalid: window must be >= interval")
	}
	if _, _, err := ComputePeriodAndEvaluationPeriods(p.Window, p.Interval); err != nil {
		return err
	}
	if _, err := MapComparisonOperator(p.Operator); err != nil {
		return err
	}
	names := BuildAlertResourceNames(p.Namespace, p.Name)
	if len(names.AlarmName) > maxCloudWatchResourceNameLen {
		return fmt.Errorf(
			"invalid: generated alarm name length %d exceeds CloudWatch limit of %d; shorten rule namespace/name",
			len(names.AlarmName),
			maxCloudWatchResourceNameLen,
		)
	}
	if len(names.MetricName) > maxCloudWatchResourceNameLen {
		return fmt.Errorf(
			"invalid: generated metric name length %d exceeds CloudWatch limit of %d",
			len(names.MetricName),
			maxCloudWatchResourceNameLen,
		)
	}
	return nil
}

// MapComparisonOperator translates the API operator vocabulary to CloudWatch's.
func MapComparisonOperator(op string) (cwtypes.ComparisonOperator, error) {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "gt":
		return cwtypes.ComparisonOperatorGreaterThanThreshold, nil
	case "gte":
		return cwtypes.ComparisonOperatorGreaterThanOrEqualToThreshold, nil
	case "lt":
		return cwtypes.ComparisonOperatorLessThanThreshold, nil
	case "lte":
		return cwtypes.ComparisonOperatorLessThanOrEqualToThreshold, nil
	case "eq":
		return "", fmt.Errorf("invalid: operator eq is not supported by CloudWatch metric alarms")
	case "neq":
		return "", fmt.Errorf("invalid: operator neq is not supported by CloudWatch metric alarms")
	default:
		return "", fmt.Errorf("invalid: unknown operator %q", op)
	}
}

// ReverseMapOperator converts a CloudWatch ComparisonOperator back into the API vocabulary.
func ReverseMapOperator(op cwtypes.ComparisonOperator) string {
	switch op {
	case cwtypes.ComparisonOperatorGreaterThanThreshold:
		return "gt"
	case cwtypes.ComparisonOperatorGreaterThanOrEqualToThreshold:
		return "gte"
	case cwtypes.ComparisonOperatorLessThanThreshold:
		return "lt"
	case cwtypes.ComparisonOperatorLessThanOrEqualToThreshold:
		return "lte"
	}
	return ""
}

// ComputePeriodAndEvaluationPeriods derives the CloudWatch metric-alarm Period
// and EvaluationPeriods from the API window/interval.
func ComputePeriodAndEvaluationPeriods(window, interval time.Duration) (int32, int32, error) {
	if interval < time.Minute {
		return 0, 0, fmt.Errorf("invalid: interval must be >= 60s for v1 (got %s)", interval)
	}
	intervalSec := int64(interval.Seconds())
	if intervalSec%60 != 0 {
		return 0, 0, fmt.Errorf("invalid: interval must be a multiple of 60s (got %ds)", intervalSec)
	}
	evalPeriods := int64(math.Ceil(float64(window) / float64(interval)))
	if evalPeriods < 1 {
		evalPeriods = 1
	}

	total := intervalSec * evalPeriods
	if intervalSec < 3600 {
		if total > 86400 {
			return 0, 0, fmt.Errorf("invalid: period*evaluationPeriods (%ds) exceeds 86400s limit for sub-hourly periods", total)
		}
	} else {
		if total > 604800 {
			return 0, 0, fmt.Errorf("invalid: period*evaluationPeriods (%ds) exceeds 604800s (7d) limit", total)
		}
	}
	return int32(intervalSec), int32(evalPeriods), nil
}

// ParseDurationStrict wraps time.ParseDuration but rejects the empty string.
func ParseDurationStrict(s string) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return 0, errors.New("invalid: duration is empty")
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid: %w", err)
	}
	return d, nil
}

// FormatDuration renders a seconds count as a Go duration string that round-trips
// through time.ParseDuration.
func FormatDuration(d time.Duration) string {
	return d.String()
}

func encodeAlertIdentitySegment(v string) string {
	return alertIdentityEncoding.EncodeToString([]byte(v))
}

func decodeAlertIdentitySegment(v string) (string, error) {
	decoded, err := alertIdentityEncoding.DecodeString(v)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}
