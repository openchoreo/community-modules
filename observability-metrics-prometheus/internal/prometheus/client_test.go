// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"log/slog"
	"os"
	"testing"

	"github.com/prometheus/common/model"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewClient(t *testing.T) {
	client, err := NewClient("http://localhost:9090", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.baseURL != "http://localhost:9090" {
		t.Errorf("expected baseURL %q, got %q", "http://localhost:9090", client.baseURL)
	}
}

func TestConvertTimeSeriesToTimeValuePoints(t *testing.T) {
	ts := TimeSeries{
		Metric: map[string]string{"__name__": "test"},
		Values: []DataPoint{
			{Timestamp: 1700000000, Value: "1.5"},
			{Timestamp: 1700000300, Value: "2.5"},
			{Timestamp: 1700000600, Value: "3.5"},
		},
	}

	points := ConvertTimeSeriesToTimeValuePoints(ts)
	if len(points) != 3 {
		t.Fatalf("expected 3 points, got %d", len(points))
	}

	if points[0].Value != 1.5 {
		t.Errorf("expected value 1.5, got %f", points[0].Value)
	}
	if points[1].Value != 2.5 {
		t.Errorf("expected value 2.5, got %f", points[1].Value)
	}
	if points[2].Value != 3.5 {
		t.Errorf("expected value 3.5, got %f", points[2].Value)
	}

	// Check that timestamps are in RFC3339 format
	for _, p := range points {
		if p.Time == "" {
			t.Error("expected non-empty time string")
		}
	}
}

func TestConvertTimeSeriesToTimeValuePoints_Empty(t *testing.T) {
	ts := TimeSeries{
		Metric: map[string]string{},
		Values: []DataPoint{},
	}

	points := ConvertTimeSeriesToTimeValuePoints(ts)
	if len(points) != 0 {
		t.Fatalf("expected 0 points, got %d", len(points))
	}
}

func TestConvertTimeSeriesToTimeValuePoints_InvalidValue(t *testing.T) {
	ts := TimeSeries{
		Values: []DataPoint{
			{Timestamp: 1700000000, Value: "not-a-number"},
		},
	}

	points := ConvertTimeSeriesToTimeValuePoints(ts)
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	// Invalid value should result in 0 since Sscanf fails
	if points[0].Value != 0 {
		t.Errorf("expected value 0 for invalid input, got %f", points[0].Value)
	}
}

func TestConvertToTimeSeriesResponse_Vector(t *testing.T) {
	// Create a mock model.Vector
	vector := model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"__name__": "test_metric",
				"label1":   "value1",
			},
			Timestamp: 1700000000000, // milliseconds
			Value:     42.5,
		},
	}

	resp := convertToTimeSeriesResponse(vector)

	if resp.Status != "success" {
		t.Errorf("expected status success, got %s", resp.Status)
	}
	if resp.Data.ResultType != "vector" {
		t.Errorf("expected resultType vector, got %s", resp.Data.ResultType)
	}
	if len(resp.Data.Result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Data.Result))
	}

	result := resp.Data.Result[0]
	if result.Metric["__name__"] != "test_metric" {
		t.Errorf("expected metric name test_metric, got %s", result.Metric["__name__"])
	}
	if result.Metric["label1"] != "value1" {
		t.Errorf("expected label1 value1, got %s", result.Metric["label1"])
	}
	if len(result.Values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(result.Values))
	}
	if result.Values[0].Value != "42.5" {
		t.Errorf("expected value 42.5, got %s", result.Values[0].Value)
	}
}

func TestConvertToTimeSeriesResponse_Matrix(t *testing.T) {
	// Create a mock model.Matrix
	matrix := model.Matrix{
		&model.SampleStream{
			Metric: model.Metric{
				"__name__": "test_metric",
				"job":      "prometheus",
			},
			Values: []model.SamplePair{
				{Timestamp: 1700000000000, Value: 10.0},
				{Timestamp: 1700000300000, Value: 20.0},
				{Timestamp: 1700000600000, Value: 30.0},
			},
		},
	}

	resp := convertToTimeSeriesResponse(matrix)

	if resp.Status != "success" {
		t.Errorf("expected status success, got %s", resp.Status)
	}
	if resp.Data.ResultType != "matrix" {
		t.Errorf("expected resultType matrix, got %s", resp.Data.ResultType)
	}
	if len(resp.Data.Result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Data.Result))
	}

	result := resp.Data.Result[0]
	if result.Metric["__name__"] != "test_metric" {
		t.Errorf("expected metric name test_metric, got %s", result.Metric["__name__"])
	}
	if len(result.Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(result.Values))
	}
	if result.Values[0].Value != "10" {
		t.Errorf("expected value 10, got %s", result.Values[0].Value)
	}
	if result.Values[1].Value != "20" {
		t.Errorf("expected value 20, got %s", result.Values[1].Value)
	}
	if result.Values[2].Value != "30" {
		t.Errorf("expected value 30, got %s", result.Values[2].Value)
	}
}

func TestConvertToTimeSeriesResponse_Scalar(t *testing.T) {
	// Create a mock model.Scalar
	scalar := &model.Scalar{
		Timestamp: 1700000000000,
		Value:     99.9,
	}

	resp := convertToTimeSeriesResponse(scalar)

	if resp.Status != "success" {
		t.Errorf("expected status success, got %s", resp.Status)
	}
	if resp.Data.ResultType != "scalar" {
		t.Errorf("expected resultType scalar, got %s", resp.Data.ResultType)
	}
	if len(resp.Data.Result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Data.Result))
	}

	result := resp.Data.Result[0]
	if len(result.Metric) != 0 {
		t.Errorf("expected empty metric map, got %d entries", len(result.Metric))
	}
	if len(result.Values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(result.Values))
	}
	if result.Values[0].Value != "99.9" {
		t.Errorf("expected value 99.9, got %s", result.Values[0].Value)
	}
}

func TestConvertToTimeSeriesResponse_EmptyMatrix(t *testing.T) {
	// Create an empty model.Matrix
	matrix := model.Matrix{}

	resp := convertToTimeSeriesResponse(matrix)

	if resp.Status != "success" {
		t.Errorf("expected status success, got %s", resp.Status)
	}
	if len(resp.Data.Result) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp.Data.Result))
	}
}
