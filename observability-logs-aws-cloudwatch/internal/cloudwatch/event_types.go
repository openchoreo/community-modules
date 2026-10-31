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
	ReleaseLineID string
	// AllReleaseLines covers every line of the component instead of its main line.
	AllReleaseLines bool
	StartTime       time.Time
	EndTime         time.Time
	Limit           int
	SortOrder       string
	// Reasons keeps only events whose reason exactly matches one of the values.
	Reasons []string
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
	// Reasons keeps only events whose reason exactly matches one of the values.
	Reasons []string
}

// EventEntry is one CloudWatch event record normalised for the Observer response. ComponentUID
// falls back to the release line component UID label, which a release line's objects carry instead.
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
