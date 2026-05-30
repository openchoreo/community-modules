// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import "time"

// SortOrder for query results ordered by TimeGenerated.
type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

// ComponentLogsParams captures a single component-log query before it's
// rendered to KQL. All fields are validated by the handler; the builder
// trusts the inputs but quotes/escapes them for KQL safety.
type ComponentLogsParams struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string

	StartTime time.Time
	EndTime   time.Time

	Limit        int
	SortOrder    SortOrder
	SearchPhrase string
	LogLevels    []string
}

// WorkflowLogsParams captures a workflow-log query.
type WorkflowLogsParams struct {
	Namespace       string
	WorkflowRunName string

	StartTime time.Time
	EndTime   time.Time

	Limit        int
	SortOrder    SortOrder
	SearchPhrase string
	LogLevels    []string
}

// ComponentLogEntry is the projected row shape from ContainerLogV2 for
// the component-log query. It maps 1:1 onto the OpenChoreo API
// ComponentLogEntry response model (handlers.go does that final map).
type ComponentLogEntry struct {
	Timestamp           time.Time
	LogMessage          string
	LogLevel            string
	PodName             string
	ContainerName       string
	PodNamespace        string
	ComponentUID        string
	ProjectUID          string
	EnvironmentUID      string
	OpenChoreoNamespace string
}

// WorkflowLogEntry is the simpler projection for workflow logs.
type WorkflowLogEntry struct {
	Timestamp  time.Time
	LogMessage string
}

// ComponentLogsResult is what the adapter returns to the handler before
// the handler shapes it into the OpenAPI response.
type ComponentLogsResult struct {
	Logs       []ComponentLogEntry
	TotalCount int
	TookMs     int
}

// WorkflowLogsResult is the workflow-logs equivalent.
type WorkflowLogsResult struct {
	Logs       []WorkflowLogEntry
	TotalCount int
	TookMs     int
}
