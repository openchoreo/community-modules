// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import "time"

// OpenObserve column names for querying Kubernetes events.
//
// Kubernetes events are shipped by the observability-events-otel-collector module
// via the OTLP/HTTP exporter. OpenObserve flattens the OTLP record into columns,
// replacing dots in attribute/resource keys with underscores.
const (
	// evTimestamp is the event timestamp column (microseconds since epoch).
	evTimestamp = "_timestamp"
	// evMessage is the event message, stored as the OTEL log record body.
	evMessage = "body"
	// evType is the event type (e.g. Normal, Warning), stored as the OTEL severity text.
	evType = "severity"
	// evReason is the short, machine-readable reason for the event.
	evReason = "k8s_event_reason"
	// evObjectKind is the kind of the Kubernetes object the event involves.
	evObjectKind = "k8s_object_kind"
	// evObjectName is the name of the Kubernetes object the event involves.
	evObjectName = "k8s_object_name"
	// evObjectNamespace is the Kubernetes namespace the event was emitted in.
	evObjectNamespace = "k8s_namespace_name"

	// evLabelPrefix is the column prefix for OpenChoreo labels copied onto the
	// event's involved object by the k8seventenrich processor.
	evLabelPrefix = "k8s_object_label_openchoreo_dev_"

	// evComponentName is the OpenChoreo component name label column.
	evComponentName = evLabelPrefix + "component"
	// evComponentID is the OpenChoreo component UID label column.
	evComponentID = evLabelPrefix + "component_uid"
	// evProjectName is the OpenChoreo project name label column.
	evProjectName = evLabelPrefix + "project"
	// evProjectID is the OpenChoreo project UID label column.
	evProjectID = evLabelPrefix + "project_uid"
	// evEnvironmentName is the OpenChoreo environment name label column.
	evEnvironmentName = evLabelPrefix + "environment"
	// evEnvironmentID is the OpenChoreo environment UID label column.
	evEnvironmentID = evLabelPrefix + "environment_uid"
	// evNamespaceName is the OpenChoreo namespace name label column.
	evNamespaceName = evLabelPrefix + "namespace"
)

// EventEntry represents a parsed Kubernetes event from OpenObserve.
type EventEntry struct {
	Timestamp       time.Time `json:"timestamp"`
	Message         string    `json:"message"`
	Type            string    `json:"type"`
	Reason          string    `json:"reason"`
	ObjectKind      string    `json:"objectKind"`
	ObjectName      string    `json:"objectName"`
	ObjectNamespace string    `json:"objectNamespace"`
	ComponentName   string    `json:"componentName"`
	ComponentID     string    `json:"componentId"`
	ProjectName     string    `json:"projectName"`
	ProjectID       string    `json:"projectId"`
	EnvironmentName string    `json:"environmentName"`
	EnvironmentID   string    `json:"environmentId"`
	NamespaceName   string    `json:"namespaceName"`
}

// EventsQueryParams holds parameters for the component-scoped events query.
type EventsQueryParams struct {
	Namespace     string    `json:"namespace"`
	ProjectID     string    `json:"projectId,omitempty"`
	ComponentID   string    `json:"componentId,omitempty"`
	EnvironmentID string    `json:"environmentId,omitempty"`
	StartTime     time.Time `json:"startTime"`
	EndTime       time.Time `json:"endTime"`
	Limit         int       `json:"limit"`
	SortOrder     string    `json:"sortOrder"`
}

// WorkflowEventsQueryParams holds parameters for the workflow-scoped events query.
type WorkflowEventsQueryParams struct {
	Namespace       string    `json:"namespace"`
	WorkflowRunName string    `json:"workflowRunName"`
	TaskName        string    `json:"taskName,omitempty"`
	StartTime       time.Time `json:"startTime"`
	EndTime         time.Time `json:"endTime"`
	Limit           int       `json:"limit"`
	SortOrder       string    `json:"sortOrder"`
}

// EventsResult represents the result of an events query.
type EventsResult struct {
	Events     []EventEntry `json:"events"`
	TotalCount int          `json:"totalCount"`
	Took       int          `json:"took"`
}

// parseEventEntry parses a Kubernetes event from an OpenObserve search hit.
func parseEventEntry(timestamp int64, source map[string]interface{}) EventEntry {
	entry := EventEntry{
		Timestamp: time.UnixMicro(timestamp),
	}

	entry.Message = stringField(source, evMessage)
	entry.Type = stringField(source, evType)
	entry.Reason = stringField(source, evReason)
	entry.ObjectKind = stringField(source, evObjectKind)
	entry.ObjectName = stringField(source, evObjectName)
	entry.ObjectNamespace = stringField(source, evObjectNamespace)
	entry.ComponentName = stringField(source, evComponentName)
	entry.ComponentID = stringField(source, evComponentID)
	entry.ProjectName = stringField(source, evProjectName)
	entry.ProjectID = stringField(source, evProjectID)
	entry.EnvironmentName = stringField(source, evEnvironmentName)
	entry.EnvironmentID = stringField(source, evEnvironmentID)
	entry.NamespaceName = stringField(source, evNamespaceName)

	return entry
}

// stringField returns the string value at key, or "" if absent or not a string.
func stringField(source map[string]interface{}, key string) string {
	if v, ok := source[key].(string); ok {
		return v
	}
	return ""
}
