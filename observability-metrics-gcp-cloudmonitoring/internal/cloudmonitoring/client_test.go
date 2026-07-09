// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestGetResourceMetricsFansOutAllSpecs(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	var (
		mu      sync.Mutex
		filters []string
	)
	c := newClientWithLister(Config{ProjectID: "my-project"}, testLogger(), func(_ context.Context, req *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error) {
		mu.Lock()
		filters = append(filters, req.Filter)
		mu.Unlock()
		return []*monitoringpb.TimeSeries{{
			Points: []*monitoringpb.Point{{
				Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(t0)},
				Value:    &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 1}},
			}},
		}}, nil
	})

	result, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "default",
		StartTime: t0.Add(-time.Hour),
		EndTime:   t0,
		Step:      5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(filters) != len(resourceMetricSpecs) {
		t.Errorf("ran %d queries, want %d", len(filters), len(resourceMetricSpecs))
	}
	for _, spec := range resourceMetricSpecs {
		found := false
		for _, f := range filters {
			if strings.Contains(f, spec.metricType) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no query issued for %s", spec.metricType)
		}
	}

	for name, series := range map[string][]TimeValuePoint{
		"cpuUsage":       result.CPUUsage,
		"cpuRequests":    result.CPURequests,
		"cpuLimits":      result.CPULimits,
		"memoryUsage":    result.MemoryUsage,
		"memoryRequests": result.MemoryRequests,
		"memoryLimits":   result.MemoryLimits,
	} {
		if len(series) != 1 || series[0].Value != 1 {
			t.Errorf("%s = %+v, want single point of 1", name, series)
		}
	}
}

func TestGetResourceMetricsPropagatesError(t *testing.T) {
	boom := errors.New("permission denied")
	c := newClientWithLister(Config{ProjectID: "my-project"}, testLogger(), func(_ context.Context, _ *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error) {
		return nil, boom
	})

	_, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{Namespace: "default"})
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
}

func TestPingWarnsButSucceedsOnEmpty(t *testing.T) {
	c := newClientWithLister(Config{ProjectID: "my-project"}, testLogger(), func(_ context.Context, _ *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error) {
		return nil, nil
	})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping on empty project must succeed, got %v", err)
	}
}

func TestPingPropagatesError(t *testing.T) {
	c := newClientWithLister(Config{ProjectID: "my-project"}, testLogger(), func(_ context.Context, _ *monitoringpb.ListTimeSeriesRequest) ([]*monitoringpb.TimeSeries, error) {
		return nil, errors.New("unauthenticated")
	})
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error")
	}
}
