// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

func TestGetResourceMetricsReleaseLineDimensions(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		params MetricsQueryParams
		want   map[string]string
	}{
		"main line": {
			params: MetricsQueryParams{ComponentUID: testComponentUID},
			want:   map[string]string{DimensionComponentUID: testComponentUID},
		},
		"one line": {
			params: MetricsQueryParams{ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID, AllReleaseLines: true},
			want:   map[string]string{DimensionReleaseLineUID: testReleaseLineUID},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var captured *cloudwatch.GetMetricDataInput
			c := newTestClient(&stubCloudWatchAPI{getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
				captured = in
				return &cloudwatch.GetMetricDataOutput{}, nil
			}})
			p := tt.params
			p.Namespace, p.EnvironmentUID = "payments", testEnvironmentUID
			p.StartTime, p.EndTime, p.StepSeconds = now.Add(-time.Hour), now, 60
			if _, err := c.GetResourceMetrics(context.Background(), p); err != nil {
				t.Fatalf("GetResourceMetrics() error = %v", err)
			}
			if len(captured.MetricDataQueries) != len(resourceQuerySpecs) {
				t.Fatalf("expected %d queries, got %d", len(resourceQuerySpecs), len(captured.MetricDataQueries))
			}
			tt.want[DimensionEnvironmentUID] = testEnvironmentUID
			tt.want[DimensionNamespace] = "payments"
			for _, q := range captured.MetricDataQueries {
				if got := dimensionsAsMap(q.MetricStat.Metric.Dimensions); !mapEqual(got, tt.want) {
					t.Errorf("%s dimensions = %v, want %v", aws.ToString(q.Id), got, tt.want)
				}
			}
		})
	}
}

// Every line averages the samples of the main line's and the release lines' dimensions together.
func TestGetResourceMetricsAllReleaseLines(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Minute)
	var captured *cloudwatch.GetMetricDataInput
	c := newTestClient(&stubCloudWatchAPI{getMetricDataFunc: func(in *cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error) {
		captured = in
		return &cloudwatch.GetMetricDataOutput{MetricDataResults: []cwtypes.MetricDataResult{
			// Main line: two samples averaging 1 at now, one sample of 4 later.
			{Id: aws.String("q_cpu_usage_0_sum"), Timestamps: []time.Time{now, later}, Values: []float64{2, 4}},
			{Id: aws.String("q_cpu_usage_0_samplecount"), Timestamps: []time.Time{now, later}, Values: []float64{2, 1}},
			// Release lines: one sample of 4 at now, none later.
			{Id: aws.String("q_cpu_usage_1_sum"), Timestamps: []time.Time{now}, Values: []float64{4}},
			{Id: aws.String("q_cpu_usage_1_samplecount"), Timestamps: []time.Time{now}, Values: []float64{1}},
		}}, nil
	}})

	res, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "payments", ComponentUID: testComponentUID, EnvironmentUID: testEnvironmentUID, AllReleaseLines: true,
		StartTime: now.Add(-time.Hour), EndTime: later, StepSeconds: 60,
	})
	if err != nil {
		t.Fatalf("GetResourceMetrics() error = %v", err)
	}

	if want := len(resourceQuerySpecs) * 4; len(captured.MetricDataQueries) != want {
		t.Fatalf("expected %d queries, got %d", want, len(captured.MetricDataQueries))
	}
	lineDims := map[string]bool{}
	for _, q := range captured.MetricDataQueries {
		dims := dimensionsAsMap(q.MetricStat.Metric.Dimensions)
		switch {
		case dims[DimensionComponentUID] == testComponentUID:
			lineDims["main"] = true
		case dims[DimensionReleaseLineComponentUID] == testComponentUID:
			lineDims["release lines"] = true
		default:
			t.Errorf("%s dimensions = %v, want the component's main line or release lines", aws.ToString(q.Id), dims)
		}
		if stat := aws.ToString(q.MetricStat.Stat); stat != "Sum" && stat != "SampleCount" {
			t.Errorf("%s stat = %s, want Sum or SampleCount", aws.ToString(q.Id), stat)
		}
	}
	if !lineDims["main"] || !lineDims["release lines"] {
		t.Errorf("queried dimensions %v, want both the main line and the release lines", lineDims)
	}

	want := []TimeValuePoint{{Timestamp: now, Value: 2}, {Timestamp: later, Value: 4}}
	if len(res.CPUUsage) != len(want) || res.CPUUsage[0] != want[0] || res.CPUUsage[1] != want[1] {
		t.Errorf("CPUUsage = %v, want %v", res.CPUUsage, want)
	}
	if len(res.MemoryUsage) != 0 {
		t.Errorf("MemoryUsage = %v, want no points", res.MemoryUsage)
	}
}

func TestDestinationLineFilters(t *testing.T) {
	tests := []struct {
		name   string
		params HTTPMetricsQueryParams
		want   []string
	}{
		{name: "no component or line", want: []string{"ispresent(DestinationComponentUID)"}},
		{name: "every line without a component", params: HTTPMetricsQueryParams{AllReleaseLines: true},
			want: []string{"ispresent(DestinationComponentUID)"}},
		{name: "main line", params: HTTPMetricsQueryParams{ComponentUID: "comp"},
			want: []string{"ispresent(DestinationComponentUID)", `DestinationComponentUID = "comp"`, "not ispresent(DestinationReleaseLineUID)"}},
		{name: "every line", params: HTTPMetricsQueryParams{ComponentUID: "comp", AllReleaseLines: true},
			want: []string{`(DestinationComponentUID = "comp" or DestinationReleaseLineComponentUID = "comp")`}},
		{name: "one line matches on its UID alone", params: HTTPMetricsQueryParams{ComponentUID: "comp", ReleaseLineUID: "line", AllReleaseLines: true},
			want: []string{`DestinationReleaseLineUID = "line"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := destinationLineFilters(tt.params); strings.Join(got, " and ") != strings.Join(tt.want, " and ") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTTPMetricsQueriesReleaseLine(t *testing.T) {
	p := HTTPMetricsQueryParams{ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID, StepSeconds: 300}
	for name, query := range map[string]string{
		"requests": BuildHTTPMetricsLogsInsightsQuery(p),
		"buckets":  BuildHTTPMetricsBucketLogsInsightsQuery(p),
	} {
		if !strings.Contains(query, `DestinationReleaseLineUID = "`+testReleaseLineUID+`"`) ||
			strings.Contains(query, "DestinationComponentUID") {
			t.Errorf("%s: a release line query must match the line UID alone:\n%s", name, query)
		}
	}
}

func TestCreateAlertReleaseLine(t *testing.T) {
	for name, lineUID := range map[string]string{"main line": "", "release line": testReleaseLineUID} {
		t.Run(name, func(t *testing.T) {
			alarms := &stubCloudWatchAPI{}
			if _, err := newTestClient(alarms).CreateAlert(context.Background(), withParams(func(p *MetricAlertParams) {
				p.ReleaseLineUID = lineUID
			})); err != nil {
				t.Fatalf("CreateAlert() error = %v", err)
			}
			want := map[string]string{DimensionEnvironmentUID: testEnvironmentUID, DimensionNamespace: "payments"}
			if lineUID != "" {
				want[DimensionReleaseLineUID] = lineUID
			} else {
				want[DimensionComponentUID] = testComponentUID
			}
			for _, q := range alarms.putMetricAlarmInput.Metrics {
				if q.MetricStat == nil {
					continue
				}
				if got := dimensionsAsMap(q.MetricStat.Metric.Dimensions); !mapEqual(got, want) {
					t.Errorf("%s dimensions = %v, want %v", aws.ToString(q.Id), got, want)
				}
			}
			tags := tagMap(alarms.putMetricAlarmInput.Tags)
			if got, ok := tags[TagReleaseLineUID]; got != lineUID || ok != (lineUID != "") {
				t.Errorf("%s tag = %q (present %v), want %q", TagReleaseLineUID, got, ok, lineUID)
			}
			if got := tags[TagComponentUID]; got != testComponentUID {
				t.Errorf("%s tag = %q, want %q", TagComponentUID, got, testComponentUID)
			}
		})
	}
}

func TestResolveNamespaceDimensionReleaseLine(t *testing.T) {
	for name, lineUID := range map[string]string{"main line": "", "release line": testReleaseLineUID} {
		t.Run(name, func(t *testing.T) {
			api := &stubCloudWatchAPI{}
			if _, err := newTestClient(api).ResolveNamespaceDimension(context.Background(), testComponentUID, testEnvironmentUID, lineUID); err != nil {
				t.Fatalf("ResolveNamespaceDimension() error = %v", err)
			}
			want := map[string]string{DimensionEnvironmentUID: testEnvironmentUID}
			if lineUID != "" {
				want[DimensionReleaseLineUID] = lineUID
			} else {
				want[DimensionComponentUID] = testComponentUID
			}
			got := make(map[string]string, len(api.listMetricsInput.Dimensions))
			for _, d := range api.listMetricsInput.Dimensions {
				got[aws.ToString(d.Name)] = aws.ToString(d.Value)
			}
			if !mapEqual(got, want) {
				t.Errorf("ListMetrics dimension filters = %v, want %v", got, want)
			}
		})
	}
}
