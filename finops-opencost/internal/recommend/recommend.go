// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package recommend

import (
	"math"
	"sort"
)

// Default limit-to-request multipliers used when the current profile has no
// usable request/limit ratio to preserve.
const (
	defaultCPULimitFactor    = 2.0
	defaultMemoryLimitFactor = 1.5
)

// Config holds the percentile and headroom tunables for the algorithm.
// RecommendationMinCPURequest is in cores and RecommendationMinMemRequest is in bytes.
type Config struct {
	RecommendationCPUPercentile    float64
	RecommendationMemoryPercentile float64
	RecommendationCPUHeadroom      float64
	RecommendationMemoryHeadroom   float64
	RecommendationMinCPURequest    float64
	RecommendationMinMemRequest    float64
}

// Usage holds the observed usage samples (CPU in cores, memory in bytes).
type Usage struct {
	CPUSamples []float64
	MemSamples []float64
}

// Profile is a set of resource requests and limits (CPU in cores, memory in bytes).
type Profile struct {
	CPURequest float64
	CPULimit   float64
	MemRequest float64
	MemLimit   float64
}

// Percentile returns the value at the given percentile (0-100) using the
// nearest-rank method. Returns 0 for an empty input.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// Compute derives a recommended resource profile from observed usage. Requests
// are set to the configured usage percentile plus headroom; limits preserve the
// current request-to-limit ratio when available. Recommendations only ever
// right-size downwards, so a value is never raised above its current setting.
func Compute(usage Usage, current Profile, cfg Config) Profile {
	rec := current

	if len(usage.CPUSamples) > 0 {
		recReq := Percentile(usage.CPUSamples, cfg.RecommendationCPUPercentile) * (1 + cfg.RecommendationCPUHeadroom)
		recReq = applyFloor(recReq, cfg.RecommendationMinCPURequest, current.CPURequest)
		rec.CPURequest = recReq
		rec.CPULimit = clampDown(scaleLimit(recReq, current.CPURequest, current.CPULimit, defaultCPULimitFactor), current.CPULimit)
	}

	if len(usage.MemSamples) > 0 {
		recReq := Percentile(usage.MemSamples, cfg.RecommendationMemoryPercentile) * (1 + cfg.RecommendationMemoryHeadroom)
		recReq = applyFloor(recReq, cfg.RecommendationMinMemRequest, current.MemRequest)
		rec.MemRequest = recReq
		rec.MemLimit = clampDown(scaleLimit(recReq, current.MemRequest, current.MemLimit, defaultMemoryLimitFactor), current.MemLimit)
	}

	return rec
}

// scaleLimit sizes a recommended limit from a recommended request, preserving
// the current request-to-limit ratio when both current values are present.
func scaleLimit(recRequest, curRequest, curLimit, defaultFactor float64) float64 {
	if curRequest > 0 && curLimit > 0 {
		return recRequest * (curLimit / curRequest)
	}
	return recRequest * defaultFactor
}

// applyFloor raises a recommended request to the configured minimum, then caps
// it at the current value so a floor never pushes a recommendation above what is
// already configured.
func applyFloor(value, floor, current float64) float64 {
	if value < floor {
		value = floor
	}
	return clampDown(value, current)
}

// clampDown caps value at limit when limit is set (> 0), keeping recommendations
// at or below the current configuration.
func clampDown(value, limit float64) float64 {
	if limit > 0 && value > limit {
		return limit
	}
	return value
}
