// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import "time"

// MetricsQueryParams captures a single resource-metrics query before it is
// rendered to KQL. Only Namespace is required; the UID filters are applied
// when non-empty.
type MetricsQueryParams struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string

	StartTime time.Time
	EndTime   time.Time

	// Step is the time-bin width for the summarize. Defaults are applied by
	// the handler; the builder trusts a positive value.
	Step time.Duration
}

// TimeValuePoint is a single (timestamp, value) pair in a series.
type TimeValuePoint struct {
	Timestamp time.Time
	Value     float64
}

// ResourceMetricsResult aggregates the six required series. Each slice is
// ordered ascending by timestamp. Empty slices are valid (no data in window).
type ResourceMetricsResult struct {
	CPUUsage       []TimeValuePoint
	CPURequests    []TimeValuePoint
	CPULimits      []TimeValuePoint
	MemoryUsage    []TimeValuePoint
	MemoryRequests []TimeValuePoint
	MemoryLimits   []TimeValuePoint
}
