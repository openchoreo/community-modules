// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import "time"

type MetricsQueryParams struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string

	StartTime time.Time
	EndTime   time.Time
	Step      time.Duration
}

type TimeValuePoint struct {
	Timestamp time.Time
	Value     float64
}

type ResourceMetricsResult struct {
	CPUUsage       []TimeValuePoint
	CPURequests    []TimeValuePoint
	CPULimits      []TimeValuePoint
	MemoryUsage    []TimeValuePoint
	MemoryRequests []TimeValuePoint
	MemoryLimits   []TimeValuePoint
}
