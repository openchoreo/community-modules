// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// MetricsQueryParams captures a single resource-metrics query.
type MetricsQueryParams struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
	StartTime      time.Time
	EndTime        time.Time
	StepSeconds    int32
}

// TimeValuePoint is a single (timestamp, value) pair.
type TimeValuePoint struct {
	Timestamp time.Time
	Value     float64
}

// ResourceMetricsResult aggregates the six required series.
type ResourceMetricsResult struct {
	CPUUsage       []TimeValuePoint
	CPURequests    []TimeValuePoint
	CPULimits      []TimeValuePoint
	MemoryUsage    []TimeValuePoint
	MemoryRequests []TimeValuePoint
	MemoryLimits   []TimeValuePoint
}

// resourceQuerySpec lists the (id, metric name) pairs for the 6 series.
var resourceQuerySpecs = []struct {
	id     string
	metric string
}{
	{"q_cpu_usage", MetricPodCPUUsage},
	{"q_cpu_request", MetricPodCPURequest},
	{"q_cpu_limit", MetricPodCPULimit},
	{"q_mem_usage", MetricPodMemoryUsage},
	{"q_mem_request", MetricPodMemoryRequest},
	{"q_mem_limit", MetricPodMemoryLimit},
}

// GetResourceMetrics issues one GetMetricData call covering all six series.
func (c *Client) GetResourceMetrics(ctx context.Context, p MetricsQueryParams) (*ResourceMetricsResult, error) {
	if p.StartTime.IsZero() || p.EndTime.IsZero() {
		return nil, errors.New("startTime and endTime are required")
	}
	if !p.EndTime.After(p.StartTime) {
		return nil, fmt.Errorf("endTime (%s) must be after startTime (%s)", p.EndTime, p.StartTime)
	}
	p.StepSeconds = normalizeStandardMetricPeriod(p.StepSeconds, p.StartTime, p.EndTime)

	dims := buildScopeDimensions(p.Namespace, p.ComponentUID, p.ProjectUID, p.EnvironmentUID)
	queries := make([]cwtypes.MetricDataQuery, 0, len(resourceQuerySpecs))
	for _, spec := range resourceQuerySpecs {
		queries = append(queries, cwtypes.MetricDataQuery{
			Id: aws.String(spec.id),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String(c.metricNamespace),
					MetricName: aws.String(spec.metric),
					Dimensions: dims,
				},
				Period: aws.Int32(p.StepSeconds),
				Stat:   aws.String("Average"),
			},
			ReturnData: aws.Bool(true),
		})
	}

	out, err := c.getMetricDataAll(ctx, queries, p.StartTime, p.EndTime)
	if err != nil {
		return nil, err
	}

	res := &ResourceMetricsResult{}
	for _, r := range out {
		points := toAscendingPoints(r.Timestamps, r.Values)
		switch aws.ToString(r.Id) {
		case "q_cpu_usage":
			res.CPUUsage = points
		case "q_cpu_request":
			res.CPURequests = points
		case "q_cpu_limit":
			res.CPULimits = points
		case "q_mem_usage":
			res.MemoryUsage = points
		case "q_mem_request":
			res.MemoryRequests = points
		case "q_mem_limit":
			res.MemoryLimits = points
		}
	}
	return res, nil
}

// getMetricDataAll issues GetMetricData with NextToken pagination.
func (c *Client) getMetricDataAll(ctx context.Context, queries []cwtypes.MetricDataQuery, start, end time.Time) ([]cwtypes.MetricDataResult, error) {
	var all []cwtypes.MetricDataResult
	var nextToken *string
	for {
		out, err := c.cw.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
			MetricDataQueries: queries,
			StartTime:         aws.Time(start),
			EndTime:           aws.Time(end),
			ScanBy:            cwtypes.ScanByTimestampAscending,
			NextToken:         nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("get_metric_data: %w", err)
		}
		all = append(all, out.MetricDataResults...)
		if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}
	return all, nil
}

// buildScopeDimensions returns the EMF dimensions for the requested scope.
// The dimension set must match what awsemfexporter is configured to emit so
// CloudWatch matches the alarm/series identity. The EMF declaration emits
// ComponentUID, EnvironmentUID, and Namespace only. ProjectUID is intentionally
// accepted by the API but not used as a CloudWatch dimension.
func buildScopeDimensions(namespace, componentUID, _ string, environmentUID string) []cwtypes.Dimension {
	dims := make([]cwtypes.Dimension, 0, 3)
	if componentUID != "" {
		dims = append(dims, cwtypes.Dimension{Name: aws.String(DimensionComponentUID), Value: aws.String(componentUID)})
	}
	if environmentUID != "" {
		dims = append(dims, cwtypes.Dimension{Name: aws.String(DimensionEnvironmentUID), Value: aws.String(environmentUID)})
	}
	dims = append(dims, cwtypes.Dimension{Name: aws.String(DimensionNamespace), Value: aws.String(namespace)})
	return dims
}

func normalizeStandardMetricPeriod(period int32, start, _ time.Time) int32 {
	if period <= 0 {
		period = 300
	}
	minPeriod := int32(60)
	age := time.Since(start)
	switch {
	case age > 63*24*time.Hour:
		minPeriod = 3600
	case age > 15*24*time.Hour:
		minPeriod = 300
	}
	if period < minPeriod {
		return minPeriod
	}
	return roundUpPeriod(period, minPeriod)
}

func roundUpPeriod(period, multiple int32) int32 {
	if multiple <= 0 {
		return period
	}
	if rem := period % multiple; rem != 0 {
		return period + (multiple - rem)
	}
	return period
}

func toAscendingPoints(timestamps []time.Time, values []float64) []TimeValuePoint {
	n := len(timestamps)
	if n > len(values) {
		n = len(values)
	}
	pts := make([]TimeValuePoint, n)
	for i := 0; i < n; i++ {
		pts[i] = TimeValuePoint{Timestamp: timestamps[i].UTC(), Value: values[i]}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Timestamp.Before(pts[j].Timestamp) })
	return pts
}
