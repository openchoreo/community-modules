// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"testing"
	"time"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func doublePoint(end time.Time, v float64) *monitoringpb.Point {
	return &monitoringpb.Point{
		Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(end)},
		Value:    &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: v}},
	}
}

func int64Point(end time.Time, v int64) *monitoringpb.Point {
	return &monitoringpb.Point{
		Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(end)},
		Value:    &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: v}},
	}
}

func TestTimeSeriesToPointsSortsAscending(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	series := []*monitoringpb.TimeSeries{{
		Points: []*monitoringpb.Point{
			doublePoint(t0.Add(10*time.Minute), 3),
			doublePoint(t0.Add(5*time.Minute), 2),
			doublePoint(t0, 1),
		},
	}}

	points := timeSeriesToPoints(series)
	if len(points) != 3 {
		t.Fatalf("got %d points", len(points))
	}
	for i := 1; i < len(points); i++ {
		if !points[i].Timestamp.After(points[i-1].Timestamp) {
			t.Errorf("points not ascending: %v then %v", points[i-1].Timestamp, points[i].Timestamp)
		}
	}
	if points[0].Value != 1 || points[2].Value != 3 {
		t.Errorf("values misordered: %+v", points)
	}
}

func TestTimeSeriesToPointsSumsAcrossSeries(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	series := []*monitoringpb.TimeSeries{
		{Points: []*monitoringpb.Point{doublePoint(t0, 1.5)}},
		{Points: []*monitoringpb.Point{doublePoint(t0, 2.5)}},
	}

	points := timeSeriesToPoints(series)
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1 merged", len(points))
	}
	if points[0].Value != 4.0 {
		t.Errorf("value = %v, want 4.0", points[0].Value)
	}
}

func TestTimeSeriesToPointsInt64Values(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	series := []*monitoringpb.TimeSeries{{
		Points: []*monitoringpb.Point{int64Point(t0, 268435456)},
	}}

	points := timeSeriesToPoints(series)
	if len(points) != 1 || points[0].Value != 268435456 {
		t.Errorf("int64 value not mapped: %+v", points)
	}
}

func TestTimeSeriesToPointsSkipsMalformed(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	series := []*monitoringpb.TimeSeries{{
		Points: []*monitoringpb.Point{
			{Value: &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 1}}}, // no interval
			{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(t0)}},                           // no value
			doublePoint(t0, 7),
		},
	}}

	points := timeSeriesToPoints(series)
	if len(points) != 1 || points[0].Value != 7 {
		t.Errorf("malformed points not skipped: %+v", points)
	}
}

func TestTimeSeriesToPointsEmpty(t *testing.T) {
	if points := timeSeriesToPoints(nil); len(points) != 0 {
		t.Errorf("expected empty result, got %+v", points)
	}
}
