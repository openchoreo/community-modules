// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatchmetrics

import (
	"math"
	"testing"
)

func TestParseLE(t *testing.T) {
	cases := []struct {
		in       string
		want     float64
		wantOK   bool
		infinite bool
	}{
		{"0.005", 0.005, true, false},
		{"+Inf", 0, true, true},
		{"Inf", 0, true, true},
		{"", 0, false, false},
		{"abc", 0, false, false},
	}
	for _, tc := range cases {
		got, ok := parseLE(tc.in)
		if ok != tc.wantOK {
			t.Fatalf("parseLE(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
		}
		if !ok {
			continue
		}
		if tc.infinite {
			if !math.IsInf(got, 1) {
				t.Fatalf("parseLE(%q) = %v, want +Inf", tc.in, got)
			}
			continue
		}
		if got != tc.want {
			t.Fatalf("parseLE(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestHistogramQuantileInterpolates(t *testing.T) {
	// Cumulative buckets: <=0.1 -> 5, <=0.2 -> 8, <=0.5 -> 10, +Inf -> 10.
	buckets := []leBucket{
		{upperBound: 0.1, cumulative: 5},
		{upperBound: 0.2, cumulative: 8},
		{upperBound: 0.5, cumulative: 10},
		{upperBound: math.Inf(1), cumulative: 10},
	}
	cases := []struct {
		phi  float64
		want float64
	}{
		{0.50, 0.1},   // rank 5 lands exactly at the 0.1 bucket edge
		{0.90, 0.35},  // rank 9: interpolate 0.2..0.5, (9-8)/(10-8)
		{0.99, 0.485}, // rank 9.9: interpolate 0.2..0.5, (9.9-8)/(10-8)
	}
	for _, tc := range cases {
		got, ok := histogramQuantile(tc.phi, cloneBuckets(buckets))
		if !ok {
			t.Fatalf("histogramQuantile(%v) not ok", tc.phi)
		}
		if math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("histogramQuantile(%v) = %v, want %v", tc.phi, got, tc.want)
		}
	}
}

func TestHistogramQuantileRankBeyondFiniteReturnsLargestFiniteLE(t *testing.T) {
	// Almost all mass is in the +Inf bucket; a high quantile can't interpolate an
	// upper bound and must fall back to the largest finite le (matches Prometheus).
	buckets := []leBucket{
		{upperBound: 0.1, cumulative: 5},
		{upperBound: math.Inf(1), cumulative: 10},
	}
	got, ok := histogramQuantile(0.90, cloneBuckets(buckets))
	if !ok {
		t.Fatal("expected ok")
	}
	if got != 0.1 {
		t.Fatalf("got %v, want 0.1 (largest finite le)", got)
	}
}

func TestHistogramQuantileNoData(t *testing.T) {
	if _, ok := histogramQuantile(0.5, nil); ok {
		t.Fatal("expected not ok for empty buckets")
	}
	zero := []leBucket{{upperBound: 0.1, cumulative: 0}, {upperBound: math.Inf(1), cumulative: 0}}
	if _, ok := histogramQuantile(0.5, zero); ok {
		t.Fatal("expected not ok for zero total")
	}
}

func TestPercentilesFromLECounts(t *testing.T) {
	leCounts := map[string]float64{
		"0.1":  5,
		"0.2":  8,
		"0.5":  10,
		"+Inf": 10,
	}
	p50, p90, p99 := percentilesFromLECounts(leCounts)
	if p50 == nil || p90 == nil || p99 == nil {
		t.Fatalf("expected all percentiles present, got %v %v %v", p50, p90, p99)
	}
	if math.Abs(*p50-0.1) > 1e-9 || math.Abs(*p90-0.35) > 1e-9 {
		t.Fatalf("p50=%v p90=%v", *p50, *p90)
	}
	// Invalid le values are skipped; no valid data -> nil.
	empty50, _, _ := percentilesFromLECounts(map[string]float64{"bad": 3})
	if empty50 != nil {
		t.Fatalf("expected nil for unparseable le, got %v", *empty50)
	}
}

func cloneBuckets(in []leBucket) []leBucket {
	out := make([]leBucket, len(in))
	copy(out, in)
	return out
}
