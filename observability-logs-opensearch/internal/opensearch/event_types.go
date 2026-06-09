// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"strings"
	"time"
)

// EventEntry represents a parsed Kubernetes event from OpenSearch.
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

// EventsQueryParams holds query parameters for the component-scoped events query.
type EventsQueryParams struct {
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
	NamespaceName string `json:"namespaceName"`
	ProjectID     string `json:"projectId,omitempty"`
	ComponentID   string `json:"componentId,omitempty"`
	EnvironmentID string `json:"environmentId,omitempty"`
	Limit         int    `json:"limit"`
	SortOrder     string `json:"sortOrder"`
}

// WorkflowEventsQueryParams holds query parameters for the workflow-scoped events query.
type WorkflowEventsQueryParams struct {
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
	NamespaceName string `json:"namespaceName"`
	WorkflowRunID string `json:"workflowRunId"`
	TaskName      string `json:"taskName,omitempty"`
	Limit         int    `json:"limit"`
	SortOrder     string `json:"sortOrder"`
}

// ParseEventHit converts a search hit to an EventEntry struct.
//
// Event documents store nested data under "resource" and "attributes" maps whose
// leaf keys contain literal dots (e.g. "k8s.object.kind"). The Ev* field-path
// constants include the "resource." / "attributes." prefixes used in queries, so
// they are trimmed here to index into the corresponding sub-map.
func ParseEventHit(hit Hit) EventEntry {
	source := hit.Source
	entry := EventEntry{}

	if ts, ok := source["@timestamp"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			entry.Timestamp = parsed
		}
	}

	entry.Message = getStringValue(source, EvMessage)

	// Type is sourced from the OTEL severity.text (e.g. "Normal", "Warning").
	if severity, ok := source["severity"].(map[string]interface{}); ok {
		entry.Type = getStringValue(severity, strings.TrimPrefix(EvSeverityText, "severity."))
	}

	if attrs, ok := source["attributes"].(map[string]interface{}); ok {
		entry.Reason = getStringValue(attrs, strings.TrimPrefix(EvReason, "attributes."))
		entry.ObjectNamespace = getStringValue(attrs, strings.TrimPrefix(EvObjectNamespace, "attributes."))
	}

	if res, ok := source["resource"].(map[string]interface{}); ok {
		const resPrefix = "resource."
		entry.ObjectKind = getStringValue(res, strings.TrimPrefix(EvObjectKind, resPrefix))
		entry.ObjectName = getStringValue(res, strings.TrimPrefix(EvObjectName, resPrefix))
		entry.ComponentName = getStringValue(res, strings.TrimPrefix(EvComponentName, resPrefix))
		entry.ComponentID = getStringValue(res, strings.TrimPrefix(EvComponentID, resPrefix))
		entry.ProjectName = getStringValue(res, strings.TrimPrefix(EvProjectName, resPrefix))
		entry.ProjectID = getStringValue(res, strings.TrimPrefix(EvProjectID, resPrefix))
		entry.EnvironmentName = getStringValue(res, strings.TrimPrefix(EvEnvironmentName, resPrefix))
		entry.EnvironmentID = getStringValue(res, strings.TrimPrefix(EvEnvironmentID, resPrefix))
		entry.NamespaceName = getStringValue(res, strings.TrimPrefix(EvNamespaceName, resPrefix))
	}

	return entry
}
