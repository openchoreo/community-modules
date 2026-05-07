// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

func TestBuildScopeDimensionsAlwaysIncludesNamespace(t *testing.T) {
	got := buildScopeDimensions("payments", "", "", "")
	want := map[string]string{DimensionNamespace: "payments"}
	if !mapEqual(dimensionsAsMap(got), want) {
		t.Fatalf("unexpected dimensions: %#v", got)
	}
}

func TestBuildScopeDimensionsIncludesPublishedUIDDimensions(t *testing.T) {
	got := buildScopeDimensions("payments", "comp-1", "proj-1", "env-1")
	want := map[string]string{
		DimensionComponentUID:   "comp-1",
		DimensionEnvironmentUID: "env-1",
		DimensionNamespace:      "payments",
	}
	if !mapEqual(dimensionsAsMap(got), want) {
		t.Fatalf("unexpected dimensions: %#v", got)
	}
	if _, has := dimensionsAsMap(got)[DimensionProjectUID]; has {
		t.Fatalf("did not expect project UID dimension")
	}
}

func TestBuildScopeDimensionsOmitsEmptyUIDs(t *testing.T) {
	got := buildScopeDimensions("payments", "comp-1", "", "env-1")
	want := map[string]string{
		DimensionComponentUID:   "comp-1",
		DimensionEnvironmentUID: "env-1",
		DimensionNamespace:      "payments",
	}
	if !mapEqual(dimensionsAsMap(got), want) {
		t.Fatalf("unexpected dimensions: %#v", got)
	}
	if _, has := dimensionsAsMap(got)[DimensionProjectUID]; has {
		t.Fatalf("did not expect project UID dimension")
	}
}

func TestGetResourceMetricsHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			if len(in.MetricDataQueries) != 6 {
				t.Fatalf("expected 6 queries, got %d", len(in.MetricDataQueries))
			}
			return &cloudwatch.GetMetricDataOutput{
				MetricDataResults: []cwtypes.MetricDataResult{
					{
						Id:         aws.String("q_cpu_usage"),
						Timestamps: []time.Time{now.Add(time.Minute), now},
						Values:     []float64{0.5, 0.3},
					},
					{Id: aws.String("q_cpu_request"), Timestamps: []time.Time{now}, Values: []float64{1}},
					{Id: aws.String("q_cpu_limit"), Timestamps: []time.Time{now}, Values: []float64{2}},
					{Id: aws.String("q_mem_usage"), Timestamps: []time.Time{now}, Values: []float64{1024}},
					{Id: aws.String("q_mem_request"), Timestamps: []time.Time{now}, Values: []float64{2048}},
					{Id: aws.String("q_mem_limit"), Timestamps: []time.Time{now}, Values: []float64{4096}},
				},
			}, nil
		},
	}
	c := newTestClient(api)

	res, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace:    "payments",
		ComponentUID: "comp-1",
		StartTime:    now.Add(-time.Hour),
		EndTime:      now,
		StepSeconds:  60,
	})
	if err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if len(res.CPUUsage) != 2 {
		t.Fatalf("expected 2 cpu usage points, got %d", len(res.CPUUsage))
	}
	// Should be sorted ascending.
	if !res.CPUUsage[0].Timestamp.Before(res.CPUUsage[1].Timestamp) {
		t.Fatalf("expected ascending timestamps, got %#v", res.CPUUsage)
	}
	if res.CPUUsage[0].Value != 0.3 || res.CPUUsage[1].Value != 0.5 {
		t.Fatalf("unexpected cpu values after sort: %#v", res.CPUUsage)
	}
	if len(res.CPURequests) != 1 || res.CPURequests[0].Value != 1 {
		t.Fatalf("unexpected cpu requests: %#v", res.CPURequests)
	}
	if len(res.MemoryLimits) != 1 || res.MemoryLimits[0].Value != 4096 {
		t.Fatalf("unexpected memory limits: %#v", res.MemoryLimits)
	}
}

func TestGetResourceMetricsAppliesDefaultStep(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	var captured *cloudwatch.GetMetricDataInput
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			captured = in
			return &cloudwatch.GetMetricDataOutput{}, nil
		},
	}
	c := newTestClient(api)
	if _, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "payments",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	}); err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if captured == nil || len(captured.MetricDataQueries) == 0 {
		t.Fatal("expected GetMetricData to be called")
	}
	if got := aws.ToInt32(captured.MetricDataQueries[0].MetricStat.Period); got != 300 {
		t.Fatalf("expected default 300s period, got %d", got)
	}
}

func TestGetResourceMetricsNormalizesSubMinuteStep(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	var captured *cloudwatch.GetMetricDataInput
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			captured = in
			return &cloudwatch.GetMetricDataOutput{}, nil
		},
	}
	c := newTestClient(api)
	if _, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace:   "payments",
		StartTime:   now.Add(-10 * time.Minute),
		EndTime:     now,
		StepSeconds: 15,
	}); err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if captured == nil || len(captured.MetricDataQueries) == 0 {
		t.Fatal("expected GetMetricData to be called")
	}
	if got := aws.ToInt32(captured.MetricDataQueries[0].MetricStat.Period); got != 60 {
		t.Fatalf("expected 15s UI step to normalize to 60s period, got %d", got)
	}
}

func TestGetResourceMetricsRoundsPeriodToMinuteMultiple(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	var captured *cloudwatch.GetMetricDataInput
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			captured = in
			return &cloudwatch.GetMetricDataOutput{}, nil
		},
	}
	c := newTestClient(api)
	if _, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace:   "payments",
		StartTime:   now.Add(-time.Hour),
		EndTime:     now,
		StepSeconds: 61,
	}); err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if captured == nil || len(captured.MetricDataQueries) == 0 {
		t.Fatal("expected GetMetricData to be called")
	}
	if got := aws.ToInt32(captured.MetricDataQueries[0].MetricStat.Period); got != 120 {
		t.Fatalf("expected 61s step to normalize to 120s period, got %d", got)
	}
}

func TestGetResourceMetricsUsesFiveMinuteMinimumForDataOlderThanFifteenDays(t *testing.T) {
	now := time.Now().UTC()
	var captured *cloudwatch.GetMetricDataInput
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			captured = in
			return &cloudwatch.GetMetricDataOutput{}, nil
		},
	}
	c := newTestClient(api)
	if _, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace:   "payments",
		StartTime:   now.Add(-20 * 24 * time.Hour),
		EndTime:     now.Add(-20*24*time.Hour + time.Hour),
		StepSeconds: 60,
	}); err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if captured == nil || len(captured.MetricDataQueries) == 0 {
		t.Fatal("expected GetMetricData to be called")
	}
	if got := aws.ToInt32(captured.MetricDataQueries[0].MetricStat.Period); got != 300 {
		t.Fatalf("expected >15d old query to use minimum 300s period, got %d", got)
	}
}

func TestGetResourceMetricsUsesOneHourMinimumForDataOlderThanSixtyThreeDays(t *testing.T) {
	now := time.Now().UTC()
	var captured *cloudwatch.GetMetricDataInput
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			captured = in
			return &cloudwatch.GetMetricDataOutput{}, nil
		},
	}
	c := newTestClient(api)
	if _, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace:   "payments",
		StartTime:   now.Add(-70 * 24 * time.Hour),
		EndTime:     now.Add(-70*24*time.Hour + time.Hour),
		StepSeconds: 300,
	}); err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if captured == nil || len(captured.MetricDataQueries) == 0 {
		t.Fatal("expected GetMetricData to be called")
	}
	if got := aws.ToInt32(captured.MetricDataQueries[0].MetricStat.Period); got != 3600 {
		t.Fatalf("expected >63d old query to use minimum 3600s period, got %d", got)
	}
}

func TestGetResourceMetricsRoundsOldDataPeriodToRetentionMultiple(t *testing.T) {
	now := time.Now().UTC()
	var captured *cloudwatch.GetMetricDataInput
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			captured = in
			return &cloudwatch.GetMetricDataOutput{}, nil
		},
	}
	c := newTestClient(api)
	if _, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace:   "payments",
		StartTime:   now.Add(-70 * 24 * time.Hour),
		EndTime:     now.Add(-70*24*time.Hour + 2*time.Hour),
		StepSeconds: 3700,
	}); err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if captured == nil || len(captured.MetricDataQueries) == 0 {
		t.Fatal("expected GetMetricData to be called")
	}
	if got := aws.ToInt32(captured.MetricDataQueries[0].MetricStat.Period); got != 7200 {
		t.Fatalf("expected >63d old 3700s step to round to 7200s period, got %d", got)
	}
}

func TestGetResourceMetricsRequiresStartAndEnd(t *testing.T) {
	c := newTestClient(&stubCloudWatchAPI{})
	if _, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{Namespace: "x"}); err == nil {
		t.Fatal("expected missing start/end to error")
	}
}

func TestGetResourceMetricsRejectsEndBeforeStart(t *testing.T) {
	c := newTestClient(&stubCloudWatchAPI{})
	now := time.Now()
	_, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "x",
		StartTime: now,
		EndTime:   now.Add(-time.Minute),
	})
	if err == nil {
		t.Fatal("expected end-before-start error")
	}
}

func TestGetResourceMetricsPropagatesAPIError(t *testing.T) {
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(*cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			return nil, errors.New("aws boom")
		},
	}
	c := newTestClient(api)
	now := time.Now()
	_, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "x",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err == nil {
		t.Fatal("expected API error to propagate")
	}
}

func TestGetResourceMetricsPaginatesNextToken(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	calls := 0
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			calls++
			if calls == 1 {
				return &cloudwatch.GetMetricDataOutput{
					MetricDataResults: []cwtypes.MetricDataResult{
						{Id: aws.String("q_cpu_usage"), Timestamps: []time.Time{now}, Values: []float64{0.1}},
					},
					NextToken: aws.String("page-2"),
				}, nil
			}
			if aws.ToString(in.NextToken) != "page-2" {
				t.Fatalf("expected NextToken=page-2 on second call, got %q", aws.ToString(in.NextToken))
			}
			return &cloudwatch.GetMetricDataOutput{
				MetricDataResults: []cwtypes.MetricDataResult{
					{Id: aws.String("q_mem_usage"), Timestamps: []time.Time{now}, Values: []float64{1024}},
				},
			}, nil
		},
	}
	c := newTestClient(api)
	res, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "x",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
	if len(res.CPUUsage) != 1 || len(res.MemoryUsage) != 1 {
		t.Fatalf("expected pagination to keep both series, got cpu=%d mem=%d", len(res.CPUUsage), len(res.MemoryUsage))
	}
}

func TestToAscendingPointsHandlesUnequalLengths(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	pts := toAscendingPoints([]time.Time{now, now.Add(time.Minute), now.Add(time.Hour)}, []float64{1, 2})
	if len(pts) != 2 {
		t.Fatalf("expected min(timestamps,values) = 2, got %d", len(pts))
	}
}

func TestGetResourceMetricsScopeDimensionsMatchEMFOrder(t *testing.T) {
	// awsemf and the alarm side both emit dimensions in the same order, so the
	// query side must use the same builder. This guards against drift.
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	var captured *cloudwatch.GetMetricDataInput
	api := &stubCloudWatchAPI{
		getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
			captured = in
			return &cloudwatch.GetMetricDataOutput{}, nil
		},
	}
	c := newTestClient(api)
	if _, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace:      "payments",
		ComponentUID:   "comp-1",
		EnvironmentUID: "env-1",
		ProjectUID:     "proj-1",
		StartTime:      now.Add(-time.Hour),
		EndTime:        now,
	}); err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}
	if captured == nil {
		t.Fatal("expected captured input")
	}
	got := dimensionsAsMap(captured.MetricDataQueries[0].MetricStat.Metric.Dimensions)
	want := map[string]string{
		DimensionComponentUID:   "comp-1",
		DimensionEnvironmentUID: "env-1",
		DimensionNamespace:      "payments",
	}
	if !mapEqual(got, want) {
		t.Fatalf("unexpected dimensions: %#v", got)
	}
}
