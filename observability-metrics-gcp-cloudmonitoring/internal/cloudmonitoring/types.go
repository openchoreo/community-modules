// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import "time"

// MetricsQueryParams carries the validated inputs of a resource-metrics query.
type MetricsQueryParams struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string

	StartTime time.Time
	EndTime   time.Time
	Step      time.Duration
}

// TimeValuePoint is a single sample of an aggregated series.
type TimeValuePoint struct {
	Timestamp time.Time
	Value     float64
}

// ResourceMetricsResult holds the six aggregated series returned by a
// resource-metrics query. CPU values are cores, memory values are bytes.
type ResourceMetricsResult struct {
	CPUUsage       []TimeValuePoint
	CPURequests    []TimeValuePoint
	CPULimits      []TimeValuePoint
	MemoryUsage    []TimeValuePoint
	MemoryRequests []TimeValuePoint
	MemoryLimits   []TimeValuePoint
}
