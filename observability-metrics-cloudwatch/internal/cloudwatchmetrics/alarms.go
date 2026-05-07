// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// MetricAlertParams is the input model for alert CRUD.
type MetricAlertParams struct {
	Name           string
	Namespace      string
	ProjectUID     string
	EnvironmentUID string
	ComponentUID   string
	Metric         string // cpu_usage | memory_usage
	Operator       string // gt|gte|lt|lte|eq|neq
	Threshold      float64
	Window         time.Duration
	Interval       time.Duration
	Enabled        bool
}

// AlertDetail is the reconstructed view of an existing alarm.
type AlertDetail struct {
	Name           string
	Namespace      string
	ProjectUID     string
	EnvironmentUID string
	ComponentUID   string
	Metric         string
	Operator       string
	Threshold      float64
	Window         time.Duration
	Interval       time.Duration
	Enabled        bool
	AlarmARN       string
}

const (
	alertAlarmPrefix             = "oc-metrics-alert-"
	maxCloudWatchResourceNameLen = 255
)

var alertIdentityEncoding = base64.RawURLEncoding

// Tag schema mirroring the logs module so the Observer can dispatch on
// `openchoreo.rule.source`.
const (
	TagRuleSource     = "openchoreo.rule.source"
	TagRuleSourceVal  = "metrics"
	TagRuleName       = "openchoreo.rule.name"
	TagRuleNamespace  = "openchoreo.rule.namespace"
	TagProjectUID     = "openchoreo.project.uid"
	TagEnvironmentUID = "openchoreo.environment.uid"
	TagComponentUID   = "openchoreo.component.uid"
	TagManagedBy      = "openchoreo.managed-by"
	TagManagedByValue = "observability-metrics-cloudwatch"
	TagMetric         = "openchoreo.rule.metric"
	TagOperator       = "openchoreo.rule.operator"
	TagWindow         = "openchoreo.rule.window"
	TagInterval       = "openchoreo.rule.interval"
	TagThreshold      = "openchoreo.rule.threshold"
)

// BuildAlarmName produces a deterministic, AWS-safe alarm name that round-trips
// back to (namespace, name) without a tags lookup.
func BuildAlarmName(namespace, name string) string {
	nsEnc := alertIdentityEncoding.EncodeToString([]byte(namespace))
	nameEnc := alertIdentityEncoding.EncodeToString([]byte(name))
	h := sha256.Sum256([]byte(namespace + "\x00" + name))
	short := hex.EncodeToString(h[:])[:12]
	return fmt.Sprintf("%sns.%s.rn.%s.%s", alertAlarmPrefix, nsEnc, nameEnc, short)
}

// ParseAlertIdentityFromAlarmName recovers (namespace, name) from a managed alarm name.
func ParseAlertIdentityFromAlarmName(alarmName string) (string, string, error) {
	rest, ok := strings.CutPrefix(alarmName, alertAlarmPrefix)
	if !ok {
		return "", "", fmt.Errorf("alarm name %q does not use the managed prefix", alarmName)
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 5 || parts[0] != "ns" || parts[2] != "rn" || parts[4] == "" {
		return "", "", fmt.Errorf("alarm name %q does not match the managed format", alarmName)
	}
	ns, err := alertIdentityEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", err
	}
	rn, err := alertIdentityEncoding.DecodeString(parts[3])
	if err != nil {
		return "", "", err
	}
	return string(ns), string(rn), nil
}

// MetricNameForSource maps the OpenAPI source.metric to the EMF metric name.
func MetricNameForSource(metric string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(metric)) {
	case "cpu_usage":
		return MetricPodCPUUsage, nil
	case "memory_usage":
		return MetricPodMemoryUsage, nil
	default:
		return "", fmt.Errorf("invalid: unsupported source.metric %q", metric)
	}
}

// usageAndLimitMetricsForSource returns the (usage, limit) EMF metric pair
// used to compute "% of pod limit" in a CloudWatch metric math alarm.
func usageAndLimitMetricsForSource(metric string) (usage, limit string, err error) {
	switch strings.ToLower(strings.TrimSpace(metric)) {
	case "cpu_usage":
		return MetricPodCPUUsage, MetricPodCPULimit, nil
	case "memory_usage":
		return MetricPodMemoryUsage, MetricPodMemoryLimit, nil
	default:
		return "", "", fmt.Errorf("invalid: unsupported source.metric %q", metric)
	}
}

// MapComparisonOperator maps the API operator vocabulary to CloudWatch's.
// `eq` and `neq` are unsupported by CloudWatch and return a validation error.
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
		return "", errors.New("invalid: operator 'eq' not supported by CloudWatch metric alarms")
	case "neq":
		return "", errors.New("invalid: operator 'neq' not supported by CloudWatch metric alarms")
	default:
		return "", fmt.Errorf("invalid: unknown operator %q", op)
	}
}

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

// ComputePeriodAndEvaluationPeriods derives Period+EvaluationPeriods from the
// API's window/interval. CloudWatch demands period >= 60s and a multiple of 60.
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
			return 0, 0, fmt.Errorf("invalid: period*evaluationPeriods (%ds) exceeds 86400s limit", total)
		}
	} else if total > 604800 {
		return 0, 0, fmt.Errorf("invalid: period*evaluationPeriods (%ds) exceeds 604800s (7d)", total)
	}
	return int32(intervalSec), int32(evalPeriods), nil
}

// ParseDurationStrict wraps time.ParseDuration but rejects empty.
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

func FormatDuration(d time.Duration) string {
	return d.String()
}

// ValidateAlertParams sanity-checks the inputs before any AWS call.
func ValidateAlertParams(p MetricAlertParams) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("invalid: rule name is required")
	}
	if strings.TrimSpace(p.Namespace) == "" {
		return errors.New("invalid: rule namespace is required")
	}
	if _, err := MetricNameForSource(p.Metric); err != nil {
		return err
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
	if len(BuildAlarmName(p.Namespace, p.Name)) > maxCloudWatchResourceNameLen {
		return errors.New("invalid: generated alarm name exceeds CloudWatch length limit")
	}
	return nil
}

// CreateAlert installs (or upserts) the alarm.
func (c *Client) CreateAlert(ctx context.Context, p MetricAlertParams) (string, error) {
	if err := ValidateAlertParams(p); err != nil {
		return "", err
	}
	return c.putMetricAlarm(ctx, p)
}

// UpdateAlert is an idempotent upsert. Returns ErrAlertNotFound if it doesn't exist.
func (c *Client) UpdateAlert(ctx context.Context, ruleNamespace, ruleName string, p MetricAlertParams) (string, error) {
	if _, err := c.GetAlert(ctx, ruleNamespace, ruleName); err != nil {
		return "", err
	}
	p.Name = ruleName
	if ruleNamespace != "" {
		p.Namespace = ruleNamespace
	}
	return c.CreateAlert(ctx, p)
}

// DeleteAlert removes an alarm. Returns ErrAlertNotFound if it doesn't exist.
func (c *Client) DeleteAlert(ctx context.Context, ruleNamespace, ruleName string) (string, error) {
	alarmName, _, err := c.resolveAlarmName(ctx, ruleNamespace, ruleName)
	if err != nil {
		return "", err
	}
	out, err := c.cw.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{alarmName},
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil && !isAWSNotFound(err) {
		return "", fmt.Errorf("describe_alarms: %w", err)
	}
	if err != nil || len(out.MetricAlarms) == 0 {
		return "", ErrAlertNotFound
	}
	arn := aws.ToString(out.MetricAlarms[0].AlarmArn)
	if _, err := c.cw.DeleteAlarms(ctx, &cloudwatch.DeleteAlarmsInput{
		AlarmNames: []string{alarmName},
	}); err != nil && !isAWSNotFound(err) {
		return "", fmt.Errorf("delete_alarms: %w", err)
	}
	return arn, nil
}

// GetAlert fetches and reconstructs the AlertDetail.
func (c *Client) GetAlert(ctx context.Context, ruleNamespace, ruleName string) (*AlertDetail, error) {
	alarmName, resolvedNamespace, err := c.resolveAlarmName(ctx, ruleNamespace, ruleName)
	if err != nil {
		return nil, err
	}
	out, err := c.cw.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{alarmName},
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil {
		return nil, fmt.Errorf("describe_alarms: %w", err)
	}
	if len(out.MetricAlarms) == 0 {
		return nil, ErrAlertNotFound
	}
	alarm := out.MetricAlarms[0]
	tagsOut, err := c.cw.ListTagsForResource(ctx, &cloudwatch.ListTagsForResourceInput{
		ResourceARN: alarm.AlarmArn,
	})
	if err != nil {
		return nil, fmt.Errorf("list_tags_for_resource: %w", err)
	}
	tags := tagMap(tagsOut.Tags)

	// Source/discriminator gate — ignore non-metrics alarms.
	if v := tags[TagRuleSource]; v != "" && v != TagRuleSourceVal {
		return nil, ErrAlertNotFound
	}

	// Math-expression alarms keep Period inside the m1 MetricStat, not at
	// the alarm top level.
	var periodSec int32
	for _, q := range alarm.Metrics {
		if q.MetricStat != nil && aws.ToInt32(q.MetricStat.Period) > 0 {
			periodSec = aws.ToInt32(q.MetricStat.Period)
			break
		}
	}
	period := time.Duration(periodSec) * time.Second
	evalPeriods := time.Duration(aws.ToInt32(alarm.EvaluationPeriods))
	detail := &AlertDetail{
		Name:           firstNonEmpty(tags[TagRuleName], ruleName),
		Namespace:      firstNonEmpty(tags[TagRuleNamespace], resolvedNamespace),
		ProjectUID:     tags[TagProjectUID],
		EnvironmentUID: tags[TagEnvironmentUID],
		ComponentUID:   tags[TagComponentUID],
		Metric:         tags[TagMetric],
		Operator:       firstNonEmpty(tags[TagOperator], ReverseMapOperator(alarm.ComparisonOperator)),
		Threshold:      aws.ToFloat64(alarm.Threshold),
		Interval:       period,
		Window:         period * evalPeriods,
		Enabled:        aws.ToBool(alarm.ActionsEnabled),
		AlarmARN:       aws.ToString(alarm.AlarmArn),
	}
	if w, err := ParseDurationStrict(tags[TagWindow]); err == nil {
		detail.Window = w
	}
	if i, err := ParseDurationStrict(tags[TagInterval]); err == nil {
		detail.Interval = i
	}
	if detail.Metric == "" {
		// Recover from m1's metric name when the tag is missing.
		for _, q := range alarm.Metrics {
			if q.MetricStat == nil || q.MetricStat.Metric == nil {
				continue
			}
			switch aws.ToString(q.MetricStat.Metric.MetricName) {
			case MetricPodCPUUsage:
				detail.Metric = "cpu_usage"
			case MetricPodMemoryUsage:
				detail.Metric = "memory_usage"
			}
			if detail.Metric != "" {
				break
			}
		}
	}
	return detail, nil
}

// GetAlarmTagsByName returns the tag map for an alarm by name. Used by the
// webhook handler to recover ruleName/ruleNamespace when the SNS payload does
// not carry tags.
func (c *Client) GetAlarmTagsByName(ctx context.Context, alarmName string) (map[string]string, error) {
	out, err := c.cw.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{alarmName},
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil {
		return nil, fmt.Errorf("describe_alarms: %w", err)
	}
	if len(out.MetricAlarms) == 0 {
		return nil, ErrAlertNotFound
	}
	tagsOut, err := c.cw.ListTagsForResource(ctx, &cloudwatch.ListTagsForResourceInput{
		ResourceARN: out.MetricAlarms[0].AlarmArn,
	})
	if err != nil {
		return nil, fmt.Errorf("list_tags_for_resource: %w", err)
	}
	return tagMap(tagsOut.Tags), nil
}

func (c *Client) putMetricAlarm(ctx context.Context, p MetricAlertParams) (string, error) {
	operator, err := MapComparisonOperator(p.Operator)
	if err != nil {
		return "", err
	}
	period, evalPeriods, err := ComputePeriodAndEvaluationPeriods(p.Window, p.Interval)
	if err != nil {
		return "", err
	}
	usageMetric, limitMetric, err := usageAndLimitMetricsForSource(p.Metric)
	if err != nil {
		return "", err
	}
	alarmName := BuildAlarmName(p.Namespace, p.Name)

	dims := buildScopeDimensions(p.Namespace, p.ComponentUID, p.ProjectUID, p.EnvironmentUID)

	tags := []cwtypes.Tag{
		{Key: aws.String(TagRuleSource), Value: aws.String(TagRuleSourceVal)},
		{Key: aws.String(TagRuleName), Value: aws.String(p.Name)},
		{Key: aws.String(TagRuleNamespace), Value: aws.String(p.Namespace)},
		{Key: aws.String(TagProjectUID), Value: aws.String(p.ProjectUID)},
		{Key: aws.String(TagEnvironmentUID), Value: aws.String(p.EnvironmentUID)},
		{Key: aws.String(TagComponentUID), Value: aws.String(p.ComponentUID)},
		{Key: aws.String(TagManagedBy), Value: aws.String(TagManagedByValue)},
		{Key: aws.String(TagMetric), Value: aws.String(strings.ToLower(p.Metric))},
		{Key: aws.String(TagOperator), Value: aws.String(strings.ToLower(p.Operator))},
		{Key: aws.String(TagWindow), Value: aws.String(p.Window.String())},
		{Key: aws.String(TagInterval), Value: aws.String(p.Interval.String())},
		{Key: aws.String(TagThreshold), Value: aws.String(strconv.FormatFloat(p.Threshold, 'f', -1, 64))},
	}

	// Threshold is a percentage (0-100) of the pod's CPU/memory limit.
	// CloudWatch evaluates the math expression e1 = (usage / limit) * 100;
	// when the pod has no limit, the limit series is missing, the expression
	// returns no data, and the alarm sits in INSUFFICIENT_DATA (with
	// TreatMissingData=notBreaching it does not fire).
	metrics := []cwtypes.MetricDataQuery{
		{
			Id:         aws.String("m1"),
			ReturnData: aws.Bool(false),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String(c.metricNamespace),
					MetricName: aws.String(usageMetric),
					Dimensions: dims,
				},
				Period: aws.Int32(period),
				Stat:   aws.String(string(cwtypes.StatisticAverage)),
			},
		},
		{
			Id:         aws.String("m2"),
			ReturnData: aws.Bool(false),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String(c.metricNamespace),
					MetricName: aws.String(limitMetric),
					Dimensions: dims,
				},
				Period: aws.Int32(period),
				Stat:   aws.String(string(cwtypes.StatisticAverage)),
			},
		},
		{
			Id:         aws.String("e1"),
			Expression: aws.String("(m1 / m2) * 100"),
			Label:      aws.String(p.Metric + " % of limit"),
			ReturnData: aws.Bool(true),
		},
	}

	input := &cloudwatch.PutMetricAlarmInput{
		AlarmName:               aws.String(alarmName),
		AlarmDescription:        aws.String(fmt.Sprintf("OpenChoreo metric alert %s/%s (%% of pod limit)", p.Namespace, p.Name)),
		Metrics:                 metrics,
		EvaluationPeriods:       aws.Int32(evalPeriods),
		DatapointsToAlarm:       aws.Int32(evalPeriods),
		Threshold:               aws.Float64(p.Threshold),
		ComparisonOperator:      operator,
		TreatMissingData:        aws.String("notBreaching"),
		ActionsEnabled:          aws.Bool(p.Enabled),
		AlarmActions:            c.alarmActionARNs,
		OKActions:               c.okActionARNs,
		InsufficientDataActions: c.insufficientDataActionARNs,
		Tags:                    tags,
	}
	if _, err := c.cw.PutMetricAlarm(ctx, input); err != nil {
		return "", fmt.Errorf("put_metric_alarm: %w", err)
	}

	out, err := c.cw.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{alarmName},
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil || len(out.MetricAlarms) == 0 {
		return alarmName, nil
	}
	if arn := aws.ToString(out.MetricAlarms[0].AlarmArn); arn != "" {
		return arn, nil
	}
	return alarmName, nil
}

// resolveAlarmName returns the deterministic alarm name for the rule,
// recovering namespace from tags when not supplied.
func (c *Client) resolveAlarmName(ctx context.Context, ruleNamespace, ruleName string) (string, string, error) {
	if strings.TrimSpace(ruleNamespace) != "" {
		return BuildAlarmName(ruleNamespace, ruleName), ruleNamespace, nil
	}

	out, err := c.cw.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNamePrefix: aws.String(alertAlarmPrefix),
		AlarmTypes:      []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil {
		return "", "", fmt.Errorf("describe_alarms: %w", err)
	}
	for _, alarm := range out.MetricAlarms {
		name := aws.ToString(alarm.AlarmName)
		if name == "" {
			continue
		}
		ns, rn, parseErr := ParseAlertIdentityFromAlarmName(name)
		if parseErr == nil && rn == ruleName {
			return name, ns, nil
		}
		if alarm.AlarmArn == nil {
			continue
		}
		tagsOut, err := c.cw.ListTagsForResource(ctx, &cloudwatch.ListTagsForResourceInput{
			ResourceARN: alarm.AlarmArn,
		})
		if err != nil {
			continue
		}
		tags := tagMap(tagsOut.Tags)
		if tags[TagRuleSource] != "" && tags[TagRuleSource] != TagRuleSourceVal {
			continue
		}
		if tags[TagRuleName] == ruleName {
			return name, tags[TagRuleNamespace], nil
		}
	}
	return "", "", ErrAlertNotFound
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
