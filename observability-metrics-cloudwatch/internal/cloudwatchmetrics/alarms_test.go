// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/smithy-go"
)

func TestBuildAlarmNameRoundTrips(t *testing.T) {
	first := BuildAlarmName("payments", "high-cpu")
	second := BuildAlarmName("payments", "high-cpu")
	other := BuildAlarmName("billing", "high-cpu")

	if first != second {
		t.Fatalf("expected stable alarm name, got %q vs %q", first, second)
	}
	if first == other {
		t.Fatalf("expected namespace to influence alarm name")
	}
	if !strings.HasPrefix(first, alertAlarmPrefix) {
		t.Fatalf("missing prefix: %q", first)
	}
	if len(first) > maxCloudWatchResourceNameLen {
		t.Fatalf("alarm name too long: %d", len(first))
	}

	ns, name, err := ParseAlertIdentityFromAlarmName(first)
	if err != nil {
		t.Fatalf("ParseAlertIdentityFromAlarmName() error = %v", err)
	}
	if ns != "payments" || name != "high-cpu" {
		t.Fatalf("unexpected parsed identity: ns=%q name=%q", ns, name)
	}
}

func TestParseAlertIdentityRejectsBadInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"missing prefix", "foo-bar"},
		{"too few parts", "oc-metrics-alert-ns.cGF5bWVudHM"},
		{"wrong shape", "oc-metrics-alert-x.y.z.q.r"},
		{"empty hash", "oc-metrics-alert-ns.YQ.rn.Yg."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ParseAlertIdentityFromAlarmName(tc.input); err == nil {
				t.Fatalf("expected error for %q", tc.input)
			}
		})
	}
}

func TestParseAlertIdentityRejectsBadBase64(t *testing.T) {
	if _, _, err := ParseAlertIdentityFromAlarmName("oc-metrics-alert-ns.???.rn.???.deadbeef0000"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestMetricNameForSource(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{"cpu_usage", MetricPodCPUUsage, true},
		{"CPU_USAGE", MetricPodCPUUsage, true},
		{"  memory_usage  ", MetricPodMemoryUsage, true},
		{"latency", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := MetricNameForSource(tc.input)
			if tc.ok {
				if err != nil {
					t.Fatalf("MetricNameForSource() error = %v", err)
				}
				if got != tc.want {
					t.Fatalf("MetricNameForSource(%q) = %q, want %q", tc.input, got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %q", tc.input)
			}
		})
	}
}

func TestMapComparisonOperator(t *testing.T) {
	tests := []struct {
		input string
		want  cwtypes.ComparisonOperator
		ok    bool
	}{
		{"gt", cwtypes.ComparisonOperatorGreaterThanThreshold, true},
		{"gte", cwtypes.ComparisonOperatorGreaterThanOrEqualToThreshold, true},
		{"lt", cwtypes.ComparisonOperatorLessThanThreshold, true},
		{"lte", cwtypes.ComparisonOperatorLessThanOrEqualToThreshold, true},
		{"GT", cwtypes.ComparisonOperatorGreaterThanThreshold, true},
		{"eq", "", false},
		{"neq", "", false},
		{"wat", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := MapComparisonOperator(tc.input)
			if tc.ok {
				if err != nil {
					t.Fatalf("MapComparisonOperator() error = %v", err)
				}
				if got != tc.want {
					t.Fatalf("MapComparisonOperator(%q) = %q, want %q", tc.input, got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for %q", tc.input)
			}
		})
	}
}

func TestMapComparisonOperatorEqAndNeqReturnValidationError(t *testing.T) {
	for _, op := range []string{"eq", "neq"} {
		_, err := MapComparisonOperator(op)
		if err == nil {
			t.Fatalf("expected error for %q", op)
		}
		if !strings.HasPrefix(err.Error(), "invalid:") {
			t.Fatalf("expected validation prefix in %q, got %v", op, err)
		}
	}
}

func TestReverseMapOperatorAllVariants(t *testing.T) {
	tests := []struct {
		input cwtypes.ComparisonOperator
		want  string
	}{
		{cwtypes.ComparisonOperatorGreaterThanThreshold, "gt"},
		{cwtypes.ComparisonOperatorGreaterThanOrEqualToThreshold, "gte"},
		{cwtypes.ComparisonOperatorLessThanThreshold, "lt"},
		{cwtypes.ComparisonOperatorLessThanOrEqualToThreshold, "lte"},
		{cwtypes.ComparisonOperator("unknown"), ""},
	}
	for _, tc := range tests {
		t.Run(string(tc.input), func(t *testing.T) {
			if got := ReverseMapOperator(tc.input); got != tc.want {
				t.Fatalf("ReverseMapOperator(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestComputePeriodAndEvaluationPeriods(t *testing.T) {
	tests := []struct {
		name      string
		window    time.Duration
		interval  time.Duration
		wantP     int32
		wantEval  int32
		expectErr bool
	}{
		{"exact division", 5 * time.Minute, time.Minute, 60, 5, false},
		{"rounds up", 5*time.Minute + 30*time.Second, 2 * time.Minute, 120, 3, false},
		{"sub minute interval", time.Minute, 30 * time.Second, 0, 0, true},
		{"non multiple of minute", 2 * time.Minute, 90 * time.Second, 0, 0, true},
		{"sub hourly over a day", 25 * time.Hour, 30 * time.Minute, 0, 0, true},
		{"hourly over 7 days", 8 * 24 * time.Hour, time.Hour, 0, 0, true},
		{"hourly under 7 days", 6 * time.Hour, time.Hour, 3600, 6, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotP, gotEval, err := ComputePeriodAndEvaluationPeriods(tc.window, tc.interval)
			if tc.expectErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ComputePeriodAndEvaluationPeriods() error = %v", err)
			}
			if gotP != tc.wantP || gotEval != tc.wantEval {
				t.Fatalf("got (%d,%d), want (%d,%d)", gotP, gotEval, tc.wantP, tc.wantEval)
			}
		})
	}
}

func TestParseDurationStrict(t *testing.T) {
	if _, err := ParseDurationStrict(""); err == nil {
		t.Fatal("expected empty to fail")
	}
	if _, err := ParseDurationStrict("???"); err == nil {
		t.Fatal("expected garbage to fail")
	}
	got, err := ParseDurationStrict("90s")
	if err != nil {
		t.Fatalf("ParseDurationStrict() error = %v", err)
	}
	if got != 90*time.Second {
		t.Fatalf("ParseDurationStrict() = %s", got)
	}
}

func TestFormatDurationRoundTrips(t *testing.T) {
	formatted := FormatDuration(90 * time.Second)
	parsed, err := ParseDurationStrict(formatted)
	if err != nil || parsed != 90*time.Second {
		t.Fatalf("round trip failed: %q parsed=%s err=%v", formatted, parsed, err)
	}
}

func TestValidateAlertParamsHappyPath(t *testing.T) {
	if err := ValidateAlertParams(validAlertParams()); err != nil {
		t.Fatalf("ValidateAlertParams() error = %v", err)
	}
}

func TestValidateAlertParamsRejectsInvalidInputs(t *testing.T) {
	cases := map[string]MetricAlertParams{
		"missing name":       withParams(func(p *MetricAlertParams) { p.Name = "" }),
		"missing namespace":  withParams(func(p *MetricAlertParams) { p.Namespace = "" }),
		"unknown metric":     withParams(func(p *MetricAlertParams) { p.Metric = "unknown" }),
		"zero window":        withParams(func(p *MetricAlertParams) { p.Window = 0 }),
		"zero interval":      withParams(func(p *MetricAlertParams) { p.Interval = 0 }),
		"window < interval":  withParams(func(p *MetricAlertParams) { p.Window = 30 * time.Second; p.Interval = time.Minute }),
		"sub minute":         withParams(func(p *MetricAlertParams) { p.Interval = 30 * time.Second }),
		"unsupported eq":     withParams(func(p *MetricAlertParams) { p.Operator = "eq" }),
		"bad operator":       withParams(func(p *MetricAlertParams) { p.Operator = "??" }),
		"overlong identity":  withParams(func(p *MetricAlertParams) { p.Name = strings.Repeat("a", 200); p.Namespace = strings.Repeat("b", 200) }),
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateAlertParams(params); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestCreateAlertHappyPath(t *testing.T) {
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName: aws.String(BuildAlarmName("payments", "high-cpu")),
				AlarmArn:  aws.String("arn:aws:cloudwatch:eu-north-1:123456789012:alarm:high-cpu"),
			}},
		}},
	}
	client := newTestClient(alarms)

	arn, err := client.CreateAlert(context.Background(), validAlertParams())
	if err != nil {
		t.Fatalf("CreateAlert() error = %v", err)
	}
	if arn == "" {
		t.Fatal("expected non-empty ARN")
	}
	if alarms.putMetricAlarmInput == nil {
		t.Fatal("expected PutMetricAlarm to be called")
	}
	got := alarms.putMetricAlarmInput

	// Math-expression alarms leave top-level MetricName/Namespace/Period empty.
	if aws.ToString(got.MetricName) != "" {
		t.Fatalf("expected empty top-level MetricName for math alarm, got %q", aws.ToString(got.MetricName))
	}
	if aws.ToInt32(got.EvaluationPeriods) != 5 {
		t.Fatalf("unexpected eval periods: %d", aws.ToInt32(got.EvaluationPeriods))
	}
	if aws.ToFloat64(got.Threshold) != 0.5 {
		t.Fatalf("unexpected threshold: %v", aws.ToFloat64(got.Threshold))
	}
	if got.ComparisonOperator != cwtypes.ComparisonOperatorGreaterThanThreshold {
		t.Fatalf("unexpected operator: %q", got.ComparisonOperator)
	}

	if len(got.Metrics) != 3 {
		t.Fatalf("expected 3 MetricDataQuery entries (m1,m2,e1), got %d", len(got.Metrics))
	}
	m1, m2, e1 := got.Metrics[0], got.Metrics[1], got.Metrics[2]
	if aws.ToString(m1.Id) != "m1" || aws.ToString(m2.Id) != "m2" || aws.ToString(e1.Id) != "e1" {
		t.Fatalf("unexpected metric ids: %q/%q/%q", aws.ToString(m1.Id), aws.ToString(m2.Id), aws.ToString(e1.Id))
	}
	if aws.ToBool(m1.ReturnData) || aws.ToBool(m2.ReturnData) || !aws.ToBool(e1.ReturnData) {
		t.Fatalf("expected only e1 to return data, got m1=%v m2=%v e1=%v",
			aws.ToBool(m1.ReturnData), aws.ToBool(m2.ReturnData), aws.ToBool(e1.ReturnData))
	}
	if aws.ToString(e1.Expression) != "(m1 / m2) * 100" {
		t.Fatalf("unexpected math expression: %q", aws.ToString(e1.Expression))
	}
	if m1.MetricStat == nil || m1.MetricStat.Metric == nil ||
		aws.ToString(m1.MetricStat.Metric.MetricName) != MetricPodCPUUsage {
		t.Fatalf("m1 should reference pod_cpu_usage")
	}
	if m2.MetricStat == nil || m2.MetricStat.Metric == nil ||
		aws.ToString(m2.MetricStat.Metric.MetricName) != MetricPodCPULimit {
		t.Fatalf("m2 should reference pod_cpu_limit")
	}
	if aws.ToString(m1.MetricStat.Metric.Namespace) != DefaultMetricNamespace {
		t.Fatalf("unexpected m1 namespace: %q", aws.ToString(m1.MetricStat.Metric.Namespace))
	}
	if aws.ToInt32(m1.MetricStat.Period) != 60 || aws.ToInt32(m2.MetricStat.Period) != 60 {
		t.Fatalf("unexpected period: m1=%d m2=%d",
			aws.ToInt32(m1.MetricStat.Period), aws.ToInt32(m2.MetricStat.Period))
	}

	wantDims := map[string]string{
		DimensionComponentUID:   "comp-1",
		DimensionEnvironmentUID: "env-1",
		DimensionNamespace:      "payments",
	}
	if got := dimensionsAsMap(m1.MetricStat.Metric.Dimensions); !mapEqual(got, wantDims) {
		t.Fatalf("unexpected m1 dimensions: %#v", got)
	}
	if got := dimensionsAsMap(m2.MetricStat.Metric.Dimensions); !mapEqual(got, wantDims) {
		t.Fatalf("unexpected m2 dimensions: %#v", got)
	}

	if !hasTag(got.Tags, TagRuleSource, TagRuleSourceVal) {
		t.Fatalf("expected rule source tag, got %#v", got.Tags)
	}
	if !hasTag(got.Tags, TagManagedBy, TagManagedByValue) {
		t.Fatalf("expected managed-by tag, got %#v", got.Tags)
	}
}

func TestCreateAlertMemoryUsesMemoryLimitPair(t *testing.T) {
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName: aws.String(BuildAlarmName("payments", "high-cpu")),
				AlarmArn:  aws.String("arn:test"),
			}},
		}},
	}
	client := newTestClient(alarms)

	p := validAlertParams()
	p.Metric = "memory_usage"
	if _, err := client.CreateAlert(context.Background(), p); err != nil {
		t.Fatalf("CreateAlert() error = %v", err)
	}
	got := alarms.putMetricAlarmInput
	if got == nil || len(got.Metrics) != 3 {
		t.Fatalf("expected 3 metric queries")
	}
	if name := aws.ToString(got.Metrics[0].MetricStat.Metric.MetricName); name != MetricPodMemoryUsage {
		t.Fatalf("m1 should be pod_memory_usage, got %q", name)
	}
	if name := aws.ToString(got.Metrics[1].MetricStat.Metric.MetricName); name != MetricPodMemoryLimit {
		t.Fatalf("m2 should be pod_memory_limit, got %q", name)
	}
}

func TestUsageAndLimitMetricsForSource(t *testing.T) {
	tests := []struct {
		input      string
		wantUsage  string
		wantLimit  string
		expectFail bool
	}{
		{"cpu_usage", MetricPodCPUUsage, MetricPodCPULimit, false},
		{"  CPU_USAGE  ", MetricPodCPUUsage, MetricPodCPULimit, false},
		{"memory_usage", MetricPodMemoryUsage, MetricPodMemoryLimit, false},
		{"latency", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			gotU, gotL, err := usageAndLimitMetricsForSource(tc.input)
			if tc.expectFail {
				if err == nil {
					t.Fatalf("expected error for %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("usageAndLimitMetricsForSource() error = %v", err)
			}
			if gotU != tc.wantUsage || gotL != tc.wantLimit {
				t.Fatalf("got (%q,%q), want (%q,%q)", gotU, gotL, tc.wantUsage, tc.wantLimit)
			}
		})
	}
}

func TestCreateAlertReturnsValidationError(t *testing.T) {
	client := newTestClient(&stubCloudWatchAPI{})
	bad := validAlertParams()
	bad.Operator = "eq"
	if _, err := client.CreateAlert(context.Background(), bad); err == nil {
		t.Fatal("expected eq operator to fail")
	}
}

func TestCreateAlertPropagatesPutAlarmError(t *testing.T) {
	alarms := &stubCloudWatchAPI{putMetricAlarmErr: errors.New("aws boom")}
	client := newTestClient(alarms)
	if _, err := client.CreateAlert(context.Background(), validAlertParams()); err == nil {
		t.Fatal("expected put_metric_alarm error")
	}
}

func TestGetAlertReturnsErrAlertNotFoundWhenAlarmMissing(t *testing.T) {
	client := newTestClient(&stubCloudWatchAPI{})
	if _, err := client.GetAlert(context.Background(), "payments", "high-cpu"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("GetAlert() error = %v, want ErrAlertNotFound", err)
	}
}

func TestGetAlertReconstructsDetailFromAlarmAndTags(t *testing.T) {
	name := BuildAlarmName("payments", "high-cpu")
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName:          aws.String(name),
				AlarmArn:           aws.String("arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test"),
				ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
				Threshold:          aws.Float64(80),
				EvaluationPeriods:  aws.Int32(5),
				ActionsEnabled:     aws.Bool(true),
				Metrics: []cwtypes.MetricDataQuery{
					{
						Id:         aws.String("m1"),
						ReturnData: aws.Bool(false),
						MetricStat: &cwtypes.MetricStat{
							Metric: &cwtypes.Metric{MetricName: aws.String(MetricPodCPUUsage)},
							Period: aws.Int32(60),
							Stat:   aws.String("Average"),
						},
					},
					{
						Id:         aws.String("e1"),
						Expression: aws.String("(m1 / m2) * 100"),
						ReturnData: aws.Bool(true),
					},
				},
			}},
		}},
		listTagsForResourceOut: &cloudwatch.ListTagsForResourceOutput{
			Tags: []cwtypes.Tag{
				{Key: aws.String(TagRuleSource), Value: aws.String(TagRuleSourceVal)},
				{Key: aws.String(TagRuleName), Value: aws.String("high-cpu")},
				{Key: aws.String(TagRuleNamespace), Value: aws.String("payments")},
				{Key: aws.String(TagMetric), Value: aws.String("cpu_usage")},
				{Key: aws.String(TagOperator), Value: aws.String("gt")},
				{Key: aws.String(TagWindow), Value: aws.String("5m0s")},
				{Key: aws.String(TagInterval), Value: aws.String("1m0s")},
				{Key: aws.String(TagComponentUID), Value: aws.String("comp-1")},
			},
		},
	}
	client := newTestClient(alarms)

	got, err := client.GetAlert(context.Background(), "payments", "high-cpu")
	if err != nil {
		t.Fatalf("GetAlert() error = %v", err)
	}
	if got.Name != "high-cpu" || got.Namespace != "payments" {
		t.Fatalf("unexpected identity: %#v", got)
	}
	if got.Operator != "gt" || got.Metric != "cpu_usage" || got.Threshold != 80 {
		t.Fatalf("unexpected fields: %#v", got)
	}
	if got.Window != 5*time.Minute || got.Interval != time.Minute {
		t.Fatalf("unexpected durations: %s/%s", got.Window, got.Interval)
	}
	if got.ComponentUID != "comp-1" {
		t.Fatalf("unexpected component uid: %q", got.ComponentUID)
	}
}

func TestGetAlertSkipsLogsModuleAlarms(t *testing.T) {
	name := BuildAlarmName("payments", "high-cpu")
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName: aws.String(name),
				AlarmArn:  aws.String("arn:test"),
			}},
		}},
		listTagsForResourceOut: &cloudwatch.ListTagsForResourceOutput{
			Tags: []cwtypes.Tag{
				{Key: aws.String(TagRuleSource), Value: aws.String("logs")},
			},
		},
	}
	client := newTestClient(alarms)
	if _, err := client.GetAlert(context.Background(), "payments", "high-cpu"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("expected logs-source alarm to be ignored, got %v", err)
	}
}

func TestGetAlertRecoversMetricFromMathQuery(t *testing.T) {
	name := BuildAlarmName("payments", "high-mem")
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName:          aws.String(name),
				AlarmArn:           aws.String("arn:test"),
				ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
				Threshold:          aws.Float64(70),
				EvaluationPeriods:  aws.Int32(5),
				ActionsEnabled:     aws.Bool(true),
				Metrics: []cwtypes.MetricDataQuery{
					{
						Id:         aws.String("m1"),
						ReturnData: aws.Bool(false),
						MetricStat: &cwtypes.MetricStat{
							Metric: &cwtypes.Metric{MetricName: aws.String(MetricPodMemoryUsage)},
							Period: aws.Int32(60),
							Stat:   aws.String("Average"),
						},
					},
				},
			}},
		}},
		listTagsForResourceOut: &cloudwatch.ListTagsForResourceOutput{},
	}
	client := newTestClient(alarms)

	got, err := client.GetAlert(context.Background(), "payments", "high-mem")
	if err != nil {
		t.Fatalf("GetAlert() error = %v", err)
	}
	if got.Metric != "memory_usage" {
		t.Fatalf("expected metric to be recovered from m1 metric name, got %q", got.Metric)
	}
}

func TestGetAlertRecoversPeriodFromMathAlarm(t *testing.T) {
	name := BuildAlarmName("payments", "high-cpu")
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName:          aws.String(name),
				AlarmArn:           aws.String("arn:test"),
				ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
				Threshold:          aws.Float64(80),
				EvaluationPeriods:  aws.Int32(5),
				ActionsEnabled:     aws.Bool(true),
				// Math alarms have no top-level MetricName/Period; they live
				// inside the Metrics list.
				Metrics: []cwtypes.MetricDataQuery{
					{
						Id:         aws.String("m1"),
						ReturnData: aws.Bool(false),
						MetricStat: &cwtypes.MetricStat{
							Metric: &cwtypes.Metric{MetricName: aws.String(MetricPodCPUUsage)},
							Period: aws.Int32(60),
							Stat:   aws.String("Average"),
						},
					},
					{
						Id:         aws.String("e1"),
						Expression: aws.String("(m1 / m2) * 100"),
						ReturnData: aws.Bool(true),
					},
				},
			}},
		}},
		listTagsForResourceOut: &cloudwatch.ListTagsForResourceOutput{
			Tags: []cwtypes.Tag{
				{Key: aws.String(TagRuleSource), Value: aws.String(TagRuleSourceVal)},
				{Key: aws.String(TagRuleName), Value: aws.String("high-cpu")},
				{Key: aws.String(TagRuleNamespace), Value: aws.String("payments")},
				{Key: aws.String(TagMetric), Value: aws.String("cpu_usage")},
			},
		},
	}
	client := newTestClient(alarms)

	got, err := client.GetAlert(context.Background(), "payments", "high-cpu")
	if err != nil {
		t.Fatalf("GetAlert() error = %v", err)
	}
	if got.Interval != time.Minute {
		t.Fatalf("expected interval recovered from m1.Period=60, got %s", got.Interval)
	}
	if got.Window != 5*time.Minute {
		t.Fatalf("expected window = period * evalPeriods = 5m, got %s", got.Window)
	}
	if got.Threshold != 80 {
		t.Fatalf("expected threshold 80, got %v", got.Threshold)
	}
	if got.Metric != "cpu_usage" {
		t.Fatalf("expected metric cpu_usage, got %q", got.Metric)
	}
}

func TestUpdateAlertReturnsErrAlertNotFoundWhenMissing(t *testing.T) {
	client := newTestClient(&stubCloudWatchAPI{})
	if _, err := client.UpdateAlert(context.Background(), "payments", "high-cpu", validAlertParams()); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("UpdateAlert() error = %v", err)
	}
}

func TestDeleteAlertHappyPath(t *testing.T) {
	name := BuildAlarmName("payments", "high-cpu")
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{
				AlarmName: aws.String(name),
				AlarmArn:  aws.String("arn:test"),
			}},
		}},
	}
	client := newTestClient(alarms)
	arn, err := client.DeleteAlert(context.Background(), "payments", "high-cpu")
	if err != nil {
		t.Fatalf("DeleteAlert() error = %v", err)
	}
	if arn != "arn:test" {
		t.Fatalf("unexpected ARN: %q", arn)
	}
	if alarms.deleteAlarmsInput == nil {
		t.Fatal("expected DeleteAlarms to be called")
	}
}

func TestDeleteAlertReturnsErrAlertNotFoundWhenAlarmMissing(t *testing.T) {
	client := newTestClient(&stubCloudWatchAPI{})
	if _, err := client.DeleteAlert(context.Background(), "payments", "high-cpu"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("DeleteAlert() error = %v", err)
	}
}

func TestResolveAlarmNameByTagsFallback(t *testing.T) {
	mathQueries := []cwtypes.MetricDataQuery{
		{
			Id:         aws.String("m1"),
			ReturnData: aws.Bool(false),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{MetricName: aws.String(MetricPodCPUUsage)},
				Period: aws.Int32(60),
				Stat:   aws.String("Average"),
			},
		},
	}
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{
			{
				MetricAlarms: []cwtypes.MetricAlarm{{
					AlarmName: aws.String("oc-metrics-alert-legacy"),
					AlarmArn:  aws.String("arn:legacy"),
				}},
			},
			{
				MetricAlarms: []cwtypes.MetricAlarm{{
					AlarmName:          aws.String("oc-metrics-alert-legacy"),
					AlarmArn:           aws.String("arn:legacy"),
					ComparisonOperator: cwtypes.ComparisonOperatorGreaterThanThreshold,
					Threshold:          aws.Float64(80),
					EvaluationPeriods:  aws.Int32(5),
					ActionsEnabled:     aws.Bool(true),
					Metrics:            mathQueries,
				}},
			},
		},
		listTagsForResourceOut: &cloudwatch.ListTagsForResourceOutput{
			Tags: []cwtypes.Tag{
				{Key: aws.String(TagRuleSource), Value: aws.String(TagRuleSourceVal)},
				{Key: aws.String(TagRuleName), Value: aws.String("legacy-rule")},
				{Key: aws.String(TagRuleNamespace), Value: aws.String("legacy-ns")},
			},
		},
	}
	client := newTestClient(alarms)

	got, err := client.GetAlert(context.Background(), "", "legacy-rule")
	if err != nil {
		t.Fatalf("GetAlert() error = %v", err)
	}
	if got.Namespace != "legacy-ns" {
		t.Fatalf("expected namespace to come from tag fallback, got %q", got.Namespace)
	}
}

func TestGetAlarmTagsByName(t *testing.T) {
	alarms := &stubCloudWatchAPI{
		describeAlarmsOuts: []*cloudwatch.DescribeAlarmsOutput{{
			MetricAlarms: []cwtypes.MetricAlarm{{AlarmArn: aws.String("arn:test")}},
		}},
		listTagsForResourceOut: &cloudwatch.ListTagsForResourceOutput{
			Tags: []cwtypes.Tag{
				{Key: aws.String(TagRuleName), Value: aws.String("rule")},
				{Key: aws.String(TagRuleNamespace), Value: aws.String("ns")},
			},
		},
	}
	client := newTestClient(alarms)
	tags, err := client.GetAlarmTagsByName(context.Background(), "alarm-x")
	if err != nil {
		t.Fatalf("GetAlarmTagsByName() error = %v", err)
	}
	if tags[TagRuleName] != "rule" || tags[TagRuleNamespace] != "ns" {
		t.Fatalf("unexpected tags: %v", tags)
	}
}

func TestGetAlarmTagsByNameReturnsErrAlertNotFound(t *testing.T) {
	client := newTestClient(&stubCloudWatchAPI{})
	if _, err := client.GetAlarmTagsByName(context.Background(), "missing"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("GetAlarmTagsByName() error = %v, want ErrAlertNotFound", err)
	}
}

// --- helpers --------------------------------------------------------------

func validAlertParams() MetricAlertParams {
	return MetricAlertParams{
		Name:           "high-cpu",
		Namespace:      "payments",
		ProjectUID:     "proj-1",
		EnvironmentUID: "env-1",
		ComponentUID:   "comp-1",
		Metric:         "cpu_usage",
		Operator:       "gt",
		Threshold:      0.5,
		Window:         5 * time.Minute,
		Interval:       time.Minute,
		Enabled:        true,
	}
}

func withParams(mutate func(*MetricAlertParams)) MetricAlertParams {
	p := validAlertParams()
	mutate(&p)
	return p
}

func hasTag(tags []cwtypes.Tag, key, value string) bool {
	for _, t := range tags {
		if aws.ToString(t.Key) == key && aws.ToString(t.Value) == value {
			return true
		}
	}
	return false
}

func dimensionsAsMap(dims []cwtypes.Dimension) map[string]string {
	out := make(map[string]string, len(dims))
	for _, d := range dims {
		out[aws.ToString(d.Name)] = aws.ToString(d.Value)
	}
	return out
}

func mapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

type fakeAPIError struct {
	code    string
	message string
}

func (e *fakeAPIError) Error() string                 { return e.code + ": " + e.message }
func (e *fakeAPIError) ErrorCode() string             { return e.code }
func (e *fakeAPIError) ErrorMessage() string          { return e.message }
func (e *fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestIsAWSNotFound(t *testing.T) {
	if isAWSNotFound(nil) {
		t.Fatal("nil error must not be NotFound")
	}
	if !isAWSNotFound(&fakeAPIError{code: "ResourceNotFoundException"}) {
		t.Fatal("ResourceNotFoundException must be NotFound")
	}
	if !isAWSNotFound(&fakeAPIError{code: "ResourceNotFound"}) {
		t.Fatal("ResourceNotFound must be NotFound")
	}
	if !isAWSNotFound(&fakeAPIError{code: "NotFound"}) {
		t.Fatal("NotFound must be NotFound")
	}
	if isAWSNotFound(&fakeAPIError{code: "AccessDenied"}) {
		t.Fatal("AccessDenied must not be NotFound")
	}
	if isAWSNotFound(errors.New("plain")) {
		t.Fatal("plain error must not be NotFound")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "value", "another"); got != "value" {
		t.Fatalf("firstNonEmpty() = %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty() empty case = %q", got)
	}
}
