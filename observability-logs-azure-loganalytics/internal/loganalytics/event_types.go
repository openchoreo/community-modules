// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import "time"

const (
	// DefaultEventsLimit is the contract's default page size.
	DefaultEventsLimit = 100

	// MaxEventBoundaryGroup bounds how far a page may be extended to finish the
	// group of events sharing its final timestamp. It matches the OpenSearch
	// adapter; a group this large means a runaway emitter, not a real window.
	MaxEventBoundaryGroup = 10000
)

// ComponentEventScope selects events of objects labelled with an OpenChoreo
// namespace and, optionally, a project, component and environment.
type ComponentEventScope struct {
	Namespace      string
	ComponentUID   string
	ProjectUID     string
	EnvironmentUID string
}

// WorkflowEventScope selects events of a workflow run's objects, optionally
// narrowed to one task.
type WorkflowEventScope struct {
	Namespace       string
	WorkflowRunName string
	TaskName        string
}

// EventsParams describes one events query. At most one scope is set; with
// neither, the query is an unscoped sweep and Reasons is required.
type EventsParams struct {
	Component *ComponentEventScope
	Workflow  *WorkflowEventScope
	Reasons   []string
	StartTime time.Time // inclusive
	EndTime   time.Time // exclusive
	Limit     int
	SortOrder SortOrder
}

type EventEntry struct {
	Timestamp       time.Time
	Message         string
	Type            string
	Reason          string
	ObjectKind      string
	ObjectName      string
	ObjectNamespace string
	NamespaceName   string
	ComponentName   string
	ComponentUID    string
	ProjectName     string
	ProjectUID      string
	EnvironmentName string
	EnvironmentUID  string
}

// EventsResult is one page of events. Events may exceed the requested limit
// when the page was extended to its timestamp boundary. Total counts every
// event in the window, so a caller can tell a truncated read from a complete one.
type EventsResult struct {
	Events []EventEntry
	Total  int
	TookMs int
}
