// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import "time"

// ComponentEventsParams captures the component-scoped event query options.
type ComponentEventsParams struct {
	Namespace     string
	ProjectID     string
	EnvironmentID string
	ComponentID   string
	StartTime     time.Time
	EndTime       time.Time
	Limit         int
	SortOrder     string
}

// WorkflowEventsParams captures the workflow-scoped event query options.
type WorkflowEventsParams struct {
	Namespace       string
	WorkflowRunName string
	TaskName        string
	StartTime       time.Time
	EndTime         time.Time
	Limit           int
	SortOrder       string
}

// EventEntry is one CloudWatch event record normalised for the Observer response.
type EventEntry struct {
	Timestamp       time.Time
	Message         string
	Type            string
	Reason          string
	ObjectKind      string
	ObjectName      string
	ObjectNamespace string
	ComponentUID    string
	ComponentName   string
	EnvironmentUID  string
	EnvironmentName string
	ProjectUID      string
	ProjectName     string
	NamespaceName   string
}

// EventsResult wraps the entries and query metadata.
type EventsResult struct {
	Events     []EventEntry
	TotalCount int
	Took       int
}
