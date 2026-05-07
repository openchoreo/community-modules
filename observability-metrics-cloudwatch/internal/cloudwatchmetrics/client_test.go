// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// stubCloudWatchAPI is a hand-rolled fake of the cloudwatchAPI interface for
// alarm and metric tests. The defaults are silent — pass slices via the public
// fields to set canned responses.
type stubCloudWatchAPI struct {
	getMetricDataFunc        func(*cloudwatch.GetMetricDataInput) (*cloudwatch.GetMetricDataOutput, error)
	listMetricsErr           error
	putMetricAlarmInput      *cloudwatch.PutMetricAlarmInput
	putMetricAlarmErr        error
	deleteAlarmsInput        *cloudwatch.DeleteAlarmsInput
	deleteAlarmsErr          error
	describeAlarmsCalls      int
	describeAlarmsOuts       []*cloudwatch.DescribeAlarmsOutput
	describeAlarmsErr        error
	listTagsForResourceOut   *cloudwatch.ListTagsForResourceOutput
	listTagsForResourceErr   error
	listTagsForResourceCalls int
}

func (s *stubCloudWatchAPI) GetMetricData(_ context.Context, in *cloudwatch.GetMetricDataInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error) {
	if s.getMetricDataFunc != nil {
		return s.getMetricDataFunc(in)
	}
	return &cloudwatch.GetMetricDataOutput{}, nil
}

func (s *stubCloudWatchAPI) ListMetrics(_ context.Context, _ *cloudwatch.ListMetricsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.ListMetricsOutput, error) {
	return &cloudwatch.ListMetricsOutput{}, s.listMetricsErr
}

func (s *stubCloudWatchAPI) PutMetricAlarm(_ context.Context, in *cloudwatch.PutMetricAlarmInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.PutMetricAlarmOutput, error) {
	s.putMetricAlarmInput = in
	return &cloudwatch.PutMetricAlarmOutput{}, s.putMetricAlarmErr
}

func (s *stubCloudWatchAPI) DescribeAlarms(_ context.Context, _ *cloudwatch.DescribeAlarmsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.DescribeAlarmsOutput, error) {
	s.describeAlarmsCalls++
	if s.describeAlarmsErr != nil {
		return nil, s.describeAlarmsErr
	}
	if len(s.describeAlarmsOuts) == 0 {
		return &cloudwatch.DescribeAlarmsOutput{}, nil
	}
	idx := s.describeAlarmsCalls - 1
	if idx >= len(s.describeAlarmsOuts) {
		idx = len(s.describeAlarmsOuts) - 1
	}
	return s.describeAlarmsOuts[idx], nil
}

func (s *stubCloudWatchAPI) DeleteAlarms(_ context.Context, in *cloudwatch.DeleteAlarmsInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.DeleteAlarmsOutput, error) {
	s.deleteAlarmsInput = in
	return &cloudwatch.DeleteAlarmsOutput{}, s.deleteAlarmsErr
}

func (s *stubCloudWatchAPI) ListTagsForResource(_ context.Context, _ *cloudwatch.ListTagsForResourceInput, _ ...func(*cloudwatch.Options)) (*cloudwatch.ListTagsForResourceOutput, error) {
	s.listTagsForResourceCalls++
	if s.listTagsForResourceErr != nil {
		return nil, s.listTagsForResourceErr
	}
	if s.listTagsForResourceOut == nil {
		return &cloudwatch.ListTagsForResourceOutput{}, nil
	}
	return s.listTagsForResourceOut, nil
}

type stubSTSAPI struct {
	err error
}

func (s *stubSTSAPI) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil
}

func newTestClient(cw *stubCloudWatchAPI) *Client {
	return NewClientWithAWS(cw, &stubSTSAPI{}, Config{
		ClusterName: "test-cluster",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestPingDelegatesToSTS(t *testing.T) {
	c := newTestClient(&stubCloudWatchAPI{})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestPingPropagatesError(t *testing.T) {
	c := NewClientWithAWS(&stubCloudWatchAPI{}, &stubSTSAPI{err: errors.New("sts boom")}, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected sts error to propagate")
	}
}

func TestNewClientWithAWSDefaultsNamespace(t *testing.T) {
	c := NewClientWithAWS(&stubCloudWatchAPI{}, &stubSTSAPI{}, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c.MetricNamespace() != DefaultMetricNamespace {
		t.Fatalf("expected default namespace, got %q", c.MetricNamespace())
	}
}

func TestNewClientWithAWSHonoursOverride(t *testing.T) {
	c := NewClientWithAWS(&stubCloudWatchAPI{}, &stubSTSAPI{}, Config{MetricNamespace: "Custom/NS"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c.MetricNamespace() != "Custom/NS" {
		t.Fatalf("expected override namespace, got %q", c.MetricNamespace())
	}
}

func TestTagMap(t *testing.T) {
	got := tagMap([]cwtypes.Tag{
		{Key: aws.String("a"), Value: aws.String("1")},
		{Key: aws.String("b"), Value: aws.String("2")},
	})
	if got["a"] != "1" || got["b"] != "2" || len(got) != 2 {
		t.Fatalf("unexpected tag map: %#v", got)
	}
}
