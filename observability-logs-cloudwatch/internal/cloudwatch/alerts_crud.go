// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/smithy-go"
)

// defaultMetricNamespace is used when the adapter config does not override it.
const defaultMetricNamespace = "OpenChoreo/Logs"

// CreateAlert reconciles a metric filter + metric alarm for the given rule.
// If the alarm put fails after the filter is created, the filter is deleted
// best-effort to avoid orphaned resources.
func (c *Client) CreateAlert(ctx context.Context, p LogAlertParams) (string, error) {
	if err := ValidateAlertParams(p); err != nil {
		return "", err
	}
	names := BuildAlertResourceNames(p.Namespace, p.Name)

	pattern, err := BuildAlertFilterPattern(p)
	if err != nil {
		return "", err
	}

	ns := c.alertMetricNamespace
	if strings.TrimSpace(ns) == "" {
		ns = defaultMetricNamespace
	}

	if err := c.putMetricFilter(ctx, names, pattern, ns); err != nil {
		return "", fmt.Errorf("put_metric_filter: %w", err)
	}

	arn, err := c.putMetricAlarm(ctx, names, ns, p)
	if err != nil {
		// Roll back the filter best-effort.
		if cleanupErr := c.deleteMetricFilter(ctx, names.MetricFilterName); cleanupErr != nil {
			c.logger.Warn("Failed to roll back metric filter after alarm creation error",
				slog.String("filter", names.MetricFilterName),
				slog.Any("error", cleanupErr),
			)
		}
		return "", fmt.Errorf("put_metric_alarm: %w", err)
	}
	return arn, nil
}

// GetAlert reconstructs an AlertDetail from the stored alarm + filter pair.
func (c *Client) GetAlert(ctx context.Context, ruleNamespace, ruleName string) (*AlertDetail, error) {
	names, resolvedNamespace, err := c.resolveAlertResourceNames(ctx, ruleNamespace, ruleName)
	if err != nil {
		return nil, err
	}

	alarmOut, err := c.alarms.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{names.AlarmName},
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil {
		return nil, fmt.Errorf("describe_alarms: %w", err)
	}
	if len(alarmOut.MetricAlarms) == 0 {
		return nil, ErrAlertNotFound
	}
	alarm := alarmOut.MetricAlarms[0]

	filterOut, err := c.logs.DescribeMetricFilters(ctx, &cloudwatchlogs.DescribeMetricFiltersInput{
		LogGroupName:     aws.String(c.applicationLogGroup()),
		FilterNamePrefix: aws.String(names.MetricFilterName),
	})
	if err != nil {
		return nil, fmt.Errorf("describe_metric_filters: %w", err)
	}
	var pattern string
	for _, f := range filterOut.MetricFilters {
		if aws.ToString(f.FilterName) == names.MetricFilterName {
			pattern = aws.ToString(f.FilterPattern)
			break
		}
	}
	if pattern == "" {
		return nil, ErrAlertNotFound
	}

	tagsOut, err := c.alarms.ListTagsForResource(ctx, &cloudwatch.ListTagsForResourceInput{
		ResourceARN: alarm.AlarmArn,
	})
	if err != nil {
		return nil, fmt.Errorf("list_tags_for_resource: %w", err)
	}
	tags := tagMap(tagsOut.Tags)

	detail := &AlertDetail{
		Name:           tags[TagRuleName],
		Namespace:      tags[TagRuleNamespace],
		ProjectUID:     tags[TagProjectUID],
		EnvironmentUID: tags[TagEnvironmentUID],
		ComponentUID:   tags[TagComponentUID],
		SearchPattern:  tags[TagSearchPattern],
		Operator:       firstNonEmpty(tags[TagOperator], ReverseMapOperator(alarm.ComparisonOperator)),
		Threshold:      aws.ToFloat64(alarm.Threshold),
		Interval:       time.Duration(aws.ToInt32(alarm.Period)) * time.Second,
		Window:         time.Duration(aws.ToInt32(alarm.Period)) * time.Duration(aws.ToInt32(alarm.EvaluationPeriods)) * time.Second,
		Enabled:        aws.ToBool(alarm.ActionsEnabled),
		AlarmARN:       aws.ToString(alarm.AlarmArn),
	}
	if detail.Name == "" {
		detail.Name = ruleName
	}
	if detail.Namespace == "" {
		detail.Namespace = resolvedNamespace
	}
	return detail, nil
}

// UpdateAlert is an idempotent overwrite of the existing filter + alarm. It
// returns ErrAlertNotFound if the underlying resources are missing.
func (c *Client) UpdateAlert(ctx context.Context, ruleNamespace, ruleName string, p LogAlertParams) (string, error) {
	if _, err := c.GetAlert(ctx, ruleNamespace, ruleName); err != nil {
		return "", err
	}
	// Preserve the canonical name / namespace derived from the path.
	p.Name = ruleName
	p.Namespace = ruleNamespace
	return c.CreateAlert(ctx, p)
}

// DeleteAlert removes both the metric alarm and the metric filter. Missing
// resources are treated as a successful no-op.
func (c *Client) DeleteAlert(ctx context.Context, ruleNamespace, ruleName string) (string, error) {
	names, _, err := c.resolveAlertResourceNames(ctx, ruleNamespace, ruleName)
	if err != nil {
		return "", err
	}

	var arn string
	alarmOut, err := c.alarms.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{names.AlarmName},
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil && !isAWSNotFound(err) {
		return "", fmt.Errorf("describe_alarms: %w", err)
	}
	alarmFound := err == nil && len(alarmOut.MetricAlarms) > 0
	if alarmFound {
		arn = aws.ToString(alarmOut.MetricAlarms[0].AlarmArn)
		if _, err := c.alarms.DeleteAlarms(ctx, &cloudwatch.DeleteAlarmsInput{
			AlarmNames: []string{names.AlarmName},
		}); err != nil && !isAWSNotFound(err) {
			return "", fmt.Errorf("delete_alarms: %w", err)
		}
	}

	if err := c.deleteMetricFilter(ctx, names.MetricFilterName); err != nil {
		return "", err
	}

	if !alarmFound {
		return "", ErrAlertNotFound
	}
	return arn, nil
}

func (c *Client) putMetricFilter(ctx context.Context, names AlertResourceNames, pattern, metricNamespace string) error {
	_, err := c.logs.PutMetricFilter(ctx, &cloudwatchlogs.PutMetricFilterInput{
		LogGroupName:  aws.String(c.applicationLogGroup()),
		FilterName:    aws.String(names.MetricFilterName),
		FilterPattern: aws.String(pattern),
		MetricTransformations: []cwltypes.MetricTransformation{{
			MetricName:      aws.String(names.MetricName),
			MetricNamespace: aws.String(metricNamespace),
			MetricValue:     aws.String("1"),
			DefaultValue:    aws.Float64(0),
			Unit:            cwltypes.StandardUnitCount,
		}},
	})
	return err
}

func (c *Client) putMetricAlarm(ctx context.Context, names AlertResourceNames, metricNamespace string, p LogAlertParams) (string, error) {
	operator, err := MapComparisonOperator(p.Operator)
	if err != nil {
		return "", err
	}
	period, evalPeriods, err := ComputePeriodAndEvaluationPeriods(p.Window, p.Interval)
	if err != nil {
		return "", err
	}

	tags := []cwtypes.Tag{
		{Key: aws.String(TagRuleName), Value: aws.String(p.Name)},
		{Key: aws.String(TagRuleNamespace), Value: aws.String(p.Namespace)},
		{Key: aws.String(TagProjectUID), Value: aws.String(p.ProjectUID)},
		{Key: aws.String(TagEnvironmentUID), Value: aws.String(p.EnvironmentUID)},
		{Key: aws.String(TagComponentUID), Value: aws.String(p.ComponentUID)},
		{Key: aws.String(TagManagedBy), Value: aws.String(TagManagedByValue)},
		{Key: aws.String(TagSearchPattern), Value: aws.String(truncateTag(p.SearchPattern))},
		{Key: aws.String(TagOperator), Value: aws.String(strings.ToLower(p.Operator))},
	}

	input := &cloudwatch.PutMetricAlarmInput{
		AlarmName:          aws.String(names.AlarmName),
		AlarmDescription:   aws.String(fmt.Sprintf("OpenChoreo log alert %s/%s", p.Namespace, p.Name)),
		MetricName:         aws.String(names.MetricName),
		Namespace:          aws.String(metricNamespace),
		Statistic:          cwtypes.StatisticSum,
		Period:             aws.Int32(period),
		EvaluationPeriods:  aws.Int32(evalPeriods),
		DatapointsToAlarm:  aws.Int32(evalPeriods),
		Threshold:          aws.Float64(p.Threshold),
		ComparisonOperator: operator,
		TreatMissingData:   aws.String("notBreaching"),
		ActionsEnabled:     aws.Bool(p.Enabled),
		AlarmActions:       c.alarmActionARNs,
		OKActions:          c.okActionARNs,
		InsufficientDataActions: c.insufficientDataActionARNs,
		Tags:               tags,
	}
	if _, err := c.alarms.PutMetricAlarm(ctx, input); err != nil {
		return "", err
	}

	// Re-describe to fetch the ARN.
	out, err := c.alarms.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{names.AlarmName},
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil {
		return names.AlarmName, nil
	}
	if len(out.MetricAlarms) == 0 {
		return names.AlarmName, nil
	}
	if arn := aws.ToString(out.MetricAlarms[0].AlarmArn); arn != "" {
		return arn, nil
	}
	return names.AlarmName, nil
}

func (c *Client) deleteMetricFilter(ctx context.Context, filterName string) error {
	_, err := c.logs.DeleteMetricFilter(ctx, &cloudwatchlogs.DeleteMetricFilterInput{
		LogGroupName: aws.String(c.applicationLogGroup()),
		FilterName:   aws.String(filterName),
	})
	if err != nil && !isAWSNotFound(err) {
		return fmt.Errorf("delete_metric_filter: %w", err)
	}
	return nil
}

// GetAlarmTagsByName fetches the tag map of an alarm by name. Used by the
// webhook handler to recover ruleName / ruleNamespace when SNS Notifications
// do not carry alarm tags in their payload.
func (c *Client) GetAlarmTagsByName(ctx context.Context, alarmName string) (map[string]string, error) {
	out, err := c.alarms.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNames: []string{alarmName},
		AlarmTypes: []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil {
		return nil, fmt.Errorf("describe_alarms: %w", err)
	}
	if len(out.MetricAlarms) == 0 {
		return nil, ErrAlertNotFound
	}
	tagsOut, err := c.alarms.ListTagsForResource(ctx, &cloudwatch.ListTagsForResourceInput{
		ResourceARN: out.MetricAlarms[0].AlarmArn,
	})
	if err != nil {
		return nil, fmt.Errorf("list_tags_for_resource: %w", err)
	}
	return tagMap(tagsOut.Tags), nil
}

func tagMap(tags []cwtypes.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return m
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncateTag(v string) string {
	const maxTagValue = 256
	if len(v) <= maxTagValue {
		return v
	}
	return v[:maxTagValue]
}

func isAWSNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ResourceNotFoundException", "ResourceNotFound", "ValidationError", "NotFound":
			return strings.Contains(strings.ToLower(apiErr.ErrorMessage()), "not") ||
				apiErr.ErrorCode() == "ResourceNotFoundException" ||
				apiErr.ErrorCode() == "ResourceNotFound" ||
				apiErr.ErrorCode() == "NotFound"
		}
	}
	return false
}

func (c *Client) resolveAlertResourceNames(ctx context.Context, ruleNamespace, ruleName string) (AlertResourceNames, string, error) {
	if strings.TrimSpace(ruleNamespace) != "" {
		return BuildAlertResourceNames(ruleNamespace, ruleName), ruleNamespace, nil
	}

	out, err := c.alarms.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{
		AlarmNamePrefix: aws.String(alertAlarmPrefix),
		AlarmTypes:      []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm},
	})
	if err != nil {
		return AlertResourceNames{}, "", fmt.Errorf("describe_alarms: %w", err)
	}

	for _, alarm := range out.MetricAlarms {
		if aws.ToString(alarm.AlarmName) == "" {
			continue
		}

		namespace, name, parseErr := ParseAlertIdentityFromAlarmName(aws.ToString(alarm.AlarmName))
		if parseErr == nil && name == ruleName {
			return AlertResourceNames{
				AlarmName:        aws.ToString(alarm.AlarmName),
				MetricFilterName: aws.ToString(alarm.AlarmName),
				MetricName:       aws.ToString(alarm.MetricName),
			}, namespace, nil
		}

		if alarm.AlarmArn == nil {
			continue
		}
		tagsOut, err := c.alarms.ListTagsForResource(ctx, &cloudwatch.ListTagsForResourceInput{
			ResourceARN: alarm.AlarmArn,
		})
		if err != nil {
			continue
		}
		tags := tagMap(tagsOut.Tags)
		if tags[TagRuleName] != ruleName {
			continue
		}
		return AlertResourceNames{
			AlarmName:        aws.ToString(alarm.AlarmName),
			MetricFilterName: aws.ToString(alarm.AlarmName),
			MetricName:       aws.ToString(alarm.MetricName),
		}, tags[TagRuleNamespace], nil
	}

	return AlertResourceNames{}, "", ErrAlertNotFound
}
