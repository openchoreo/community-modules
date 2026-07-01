// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package observer

import "time"

type MetricsQueryRequest struct {
	Metric      string               `json:"metric"`
	StartTime   time.Time            `json:"startTime"`
	EndTime     time.Time            `json:"endTime"`
	Step        string               `json:"step,omitempty"`
	SearchScope ComponentSearchScope `json:"searchScope"`
}

type ComponentSearchScope struct {
	Namespace   string `json:"namespace"`
	Project     string `json:"project,omitempty"`
	Component   string `json:"component,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type TimeSeriesItem struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type ResourceMetrics struct {
	CPUUsage       []TimeSeriesItem `json:"cpuUsage"`
	CPURequests    []TimeSeriesItem `json:"cpuRequests"`
	CPULimits      []TimeSeriesItem `json:"cpuLimits"`
	MemoryUsage    []TimeSeriesItem `json:"memoryUsage"`
	MemoryRequests []TimeSeriesItem `json:"memoryRequests"`
	MemoryLimits   []TimeSeriesItem `json:"memoryLimits"`
}
