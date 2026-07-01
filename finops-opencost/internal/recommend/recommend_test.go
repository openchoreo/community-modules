// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package recommend

import (
	"math"
	"testing"
)

func TestPercentile(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	tests := []struct {
		p    float64
		want float64
	}{
		{p: 0, want: 1},
		{p: 50, want: 5},
		{p: 90, want: 9},
		{p: 95, want: 10},
		{p: 100, want: 10},
	}
	for _, tt := range tests {
		if got := Percentile(values, tt.p); got != tt.want {
			t.Errorf("Percentile(p=%v) = %v, want %v", tt.p, got, tt.want)
		}
	}
	if got := Percentile(nil, 90); got != 0 {
		t.Errorf("Percentile(empty) = %v, want 0", got)
	}
}

func TestComputeOverprovisioned(t *testing.T) {
	usage := Usage{
		CPUSamples: []float64{0.1, 0.1, 0.12, 0.15, 0.1},
		MemSamples: []float64{100e6, 110e6, 120e6, 100e6, 105e6},
	}
	current := Profile{
		CPURequest: 1.0,
		CPULimit:   2.0,
		MemRequest: 512e6,
		MemLimit:   1024e6,
	}
	cfg := Config{RecommendationCPUPercentile: 95, RecommendationMemoryPercentile: 95, RecommendationCPUHeadroom: 0.2, RecommendationMemoryHeadroom: 0.2}

	rec := Compute(usage, current, cfg)

	if rec.CPURequest >= current.CPURequest {
		t.Errorf("expected CPU request to be right-sized down, got %v (current %v)", rec.CPURequest, current.CPURequest)
	}
	// P95 of CPU samples is 0.15, +20% headroom => 0.18
	if math.Abs(rec.CPURequest-0.18) > 1e-9 {
		t.Errorf("CPU request = %v, want 0.18", rec.CPURequest)
	}
	// Limit preserves the 2:1 current ratio.
	if math.Abs(rec.CPULimit-0.36) > 1e-9 {
		t.Errorf("CPU limit = %v, want 0.36", rec.CPULimit)
	}
	if rec.MemRequest >= current.MemRequest {
		t.Errorf("expected memory request to be right-sized down, got %v", rec.MemRequest)
	}
}

func TestComputeClampsToCurrent(t *testing.T) {
	// Usage exceeds current requests; recommendation must not exceed current.
	usage := Usage{
		CPUSamples: []float64{5, 6, 7},
		MemSamples: []float64{900e6, 950e6},
	}
	current := Profile{CPURequest: 1.0, CPULimit: 2.0, MemRequest: 512e6, MemLimit: 1024e6}
	cfg := Config{RecommendationCPUPercentile: 95, RecommendationMemoryPercentile: 95, RecommendationCPUHeadroom: 0.2, RecommendationMemoryHeadroom: 0.2}

	rec := Compute(usage, current, cfg)

	if rec.CPURequest != current.CPURequest {
		t.Errorf("CPU request = %v, want clamped to current %v", rec.CPURequest, current.CPURequest)
	}
	if rec.CPULimit != current.CPULimit {
		t.Errorf("CPU limit = %v, want clamped to current %v", rec.CPULimit, current.CPULimit)
	}
	if rec.MemRequest != current.MemRequest {
		t.Errorf("memory request = %v, want clamped to current %v", rec.MemRequest, current.MemRequest)
	}
}

func TestComputeNoUsageKeepsCurrent(t *testing.T) {
	current := Profile{CPURequest: 1.0, CPULimit: 2.0, MemRequest: 512e6, MemLimit: 1024e6}
	cfg := Config{RecommendationCPUPercentile: 95, RecommendationMemoryPercentile: 95, RecommendationCPUHeadroom: 0.2, RecommendationMemoryHeadroom: 0.2}

	rec := Compute(Usage{}, current, cfg)

	if rec != current {
		t.Errorf("with no usage samples, recommendation should equal current; got %+v", rec)
	}
}

func TestComputeAppliesMinRequestFloor(t *testing.T) {
	// Near-idle usage would size to ~0; the floor should hold it up.
	usage := Usage{
		CPUSamples: []float64{0.0001, 0.0002},
		MemSamples: []float64{10e6},
	}
	current := Profile{CPURequest: 1.0, CPULimit: 1.0, MemRequest: 256e6, MemLimit: 256e6}
	cfg := Config{
		RecommendationCPUPercentile: 95, RecommendationMemoryPercentile: 95,
		RecommendationCPUHeadroom: 0.2, RecommendationMemoryHeadroom: 0.2,
		RecommendationMinCPURequest: 0.01, RecommendationMinMemRequest: 16 * 1024 * 1024,
	}

	rec := Compute(usage, current, cfg)

	if math.Abs(rec.CPURequest-0.01) > 1e-9 {
		t.Errorf("CPU request = %v, want floor 0.01", rec.CPURequest)
	}
	if math.Abs(rec.MemRequest-16*1024*1024) > 1 {
		t.Errorf("memory request = %v, want floor %v", rec.MemRequest, 16*1024*1024)
	}
}

func TestComputeFloorNeverExceedsCurrent(t *testing.T) {
	// A current request below the floor must not be raised.
	usage := Usage{CPUSamples: []float64{0.0001}}
	current := Profile{CPURequest: 0.005, CPULimit: 0.005}
	cfg := Config{RecommendationCPUPercentile: 95, RecommendationCPUHeadroom: 0.2, RecommendationMinCPURequest: 0.01}

	rec := Compute(usage, current, cfg)

	if rec.CPURequest != 0.005 {
		t.Errorf("CPU request = %v, want current 0.005 (floor must not raise above current)", rec.CPURequest)
	}
}

func TestComputeLimitFallbackFactor(t *testing.T) {
	// No current limit set => use default factor.
	usage := Usage{CPUSamples: []float64{0.1}, MemSamples: []float64{100e6}}
	current := Profile{CPURequest: 0, CPULimit: 0, MemRequest: 0, MemLimit: 0}
	cfg := Config{RecommendationCPUPercentile: 95, RecommendationMemoryPercentile: 95, RecommendationCPUHeadroom: 0, RecommendationMemoryHeadroom: 0}

	rec := Compute(usage, current, cfg)

	if math.Abs(rec.CPULimit-0.1*defaultCPULimitFactor) > 1e-9 {
		t.Errorf("CPU limit = %v, want %v", rec.CPULimit, 0.1*defaultCPULimitFactor)
	}
	if math.Abs(rec.MemLimit-100e6*defaultMemoryLimitFactor) > 1 {
		t.Errorf("memory limit = %v, want %v", rec.MemLimit, 100e6*defaultMemoryLimitFactor)
	}
}
