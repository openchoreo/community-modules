// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

// SearchResponse represents the response from an OpenSearch search query.
type SearchResponse struct {
	Hits struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		Hits []Hit `json:"hits"`
	} `json:"hits"`
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
}

// Hit represents a single search result hit.
type Hit struct {
	ID     string                 `json:"_id"`
	Source map[string]interface{} `json:"_source"`
	Score  *float64               `json:"_score"`
}

// LogEntry represents a parsed log entry from OpenSearch.
type LogEntry struct {
	Timestamp       time.Time         `json:"timestamp"`
	Log             string            `json:"log"`
	LogLevel        string            `json:"logLevel"`
	ComponentID     string            `json:"componentId"`
	EnvironmentID   string            `json:"environmentId"`
	ProjectID       string            `json:"projectId"`
	Version         string            `json:"version"`
	VersionID       string            `json:"versionId"`
	Namespace       string            `json:"namespace"`
	PodID           string            `json:"podId"`
	ContainerName   string            `json:"containerName"`
	Labels          map[string]string `json:"labels"`
	ComponentName   string            `json:"componentName,omitempty"`
	EnvironmentName string            `json:"environmentName,omitempty"`
	ProjectName     string            `json:"projectName,omitempty"`
	NamespaceName   string            `json:"namespaceName,omitempty"`
	PodNamespace    string            `json:"podNamespace,omitempty"`
	PodName         string            `json:"podName,omitempty"`
}

// WorkflowRunLogEntry represents a log entry for workflow run logs.
type WorkflowRunLogEntry struct {
	Timestamp string `json:"timestamp"`
	Log       string `json:"log"`
}

// QueryParams holds common query parameters.
type QueryParams struct {
	StartTime     string   `json:"startTime"`
	EndTime       string   `json:"endTime"`
	SearchPhrase  string   `json:"searchPhrase"`
	LogLevels     []string `json:"logLevels"`
	Limit         int      `json:"limit"`
	SortOrder     string   `json:"sortOrder"`
	ComponentID   string   `json:"componentId,omitempty"`
	EnvironmentID string   `json:"environmentId,omitempty"`
	ProjectID     string   `json:"projectId,omitempty"`
	NamespaceName string   `json:"namespaceName,omitempty"`
	Namespace     string   `json:"namespace,omitempty"`
}

// ComponentLogsQueryParamsV1 holds query parameters for the API component logs query.
type ComponentLogsQueryParamsV1 struct {
	StartTime     string   `json:"startTime"`
	EndTime       string   `json:"endTime"`
	NamespaceName string   `json:"namespaceName"`
	ProjectID     string   `json:"projectId,omitempty"`
	ComponentID   string   `json:"componentId,omitempty"`
	EnvironmentID string   `json:"environmentId,omitempty"`
	SearchPhrase  string   `json:"searchPhrase,omitempty"`
	LogLevels     []string `json:"logLevels,omitempty"`
	Limit         int      `json:"limit"`
	SortOrder     string   `json:"sortOrder"`
}

// WorkflowRunQueryParams holds workflow run-specific query parameters.
type WorkflowRunQueryParams struct {
	QueryParams
	WorkflowRunID string `json:"workflowRunId"`
	StepName      string `json:"stepName,omitempty"`
}

// AlertingRuleRequest defines the request structure for creating/updating alerting rules.
type AlertingRuleRequest struct {
	Metadata  AlertingRuleMetadata  `json:"metadata"`
	Source    AlertingRuleSource    `json:"source"`
	Condition AlertingRuleCondition `json:"condition"`
}

// AlertingRuleMetadata contains metadata about an alerting rule.
type AlertingRuleMetadata struct {
	Name           string `json:"name"`
	Namespace      string `json:"namespace"`
	ComponentUID   string `json:"component-uid"`
	ProjectUID     string `json:"project-uid"`
	EnvironmentUID string `json:"environment-uid"`
}

// AlertingRuleSource defines the source of data for the alerting rule.
type AlertingRuleSource struct {
	Query string `json:"query"`
}

// AlertingRuleCondition defines the condition that triggers the alert.
type AlertingRuleCondition struct {
	Enabled   bool    `json:"enabled"`
	Window    string  `json:"window"`
	Interval  string  `json:"interval"`
	Operator  string  `json:"operator"`
	Threshold float64 `json:"threshold"`
}

// MonitorBody represents the structure of an OpenSearch monitor.
type MonitorBody struct {
	Type        string           `json:"type"`
	MonitorType string           `json:"monitor_type"`
	Name        string           `json:"name"`
	Enabled     bool             `json:"enabled"`
	Schedule    MonitorSchedule  `json:"schedule"`
	Inputs      []MonitorInput   `json:"inputs"`
	Triggers    []MonitorTrigger `json:"triggers"`
}

// MonitorSchedule defines the monitoring schedule.
type MonitorSchedule struct {
	Period MonitorSchedulePeriod `json:"period"`
}

// MonitorSchedulePeriod defines the time period for schedule.
type MonitorSchedulePeriod struct {
	Interval int `json:"interval"`
	Unit     string  `json:"unit"`
}

// MonitorInput defines the search input for the monitor.
type MonitorInput struct {
	Search MonitorInputSearch `json:"search"`
}

// MonitorInputSearch defines the search query and indices.
type MonitorInputSearch struct {
	Indices []string               `json:"indices"`
	Query   map[string]interface{} `json:"query"`
}

// MonitorTrigger defines the conditions and actions for the monitor.
type MonitorTrigger struct {
	QueryLevelTrigger *MonitorTriggerQueryLevelTrigger `json:"query_level_trigger,omitempty"`
}

// MonitorTriggerQueryLevelTrigger defines a query-level trigger.
type MonitorTriggerQueryLevelTrigger struct {
	Name      string                  `json:"name"`
	Severity  string                  `json:"severity"`
	Condition MonitorTriggerCondition `json:"condition"`
	Actions   []MonitorTriggerAction  `json:"actions"`
}

// MonitorTriggerCondition defines the trigger condition.
type MonitorTriggerCondition struct {
	Script MonitorTriggerConditionScript `json:"script"`
}

// MonitorTriggerConditionScript defines the script for evaluation.
type MonitorTriggerConditionScript struct {
	Source string `json:"source"`
	Lang   string `json:"lang"`
}

// MonitorTriggerAction defines the action to take when triggered.
type MonitorTriggerAction struct {
	Name                  string                              `json:"name"`
	DestinationID         string                              `json:"destination_id"`
	MessageTemplate       MonitorMessageTemplate              `json:"message_template"`
	ThrottleEnabled       bool                                `json:"throttle_enabled"`
	Throttle              MonitorTriggerActionThrottle        `json:"throttle"`
	SubjectTemplate       MonitorMessageTemplate              `json:"subject_template"`
	ActionExecutionPolicy MonitorTriggerActionExecutionPolicy `json:"action_execution_policy"`
}

// MonitorMessageTemplate defines the message template.
type MonitorMessageTemplate struct {
	Source string `json:"source"`
	Lang   string `json:"lang"`
}

// MonitorTriggerActionThrottle defines the throttle settings.
type MonitorTriggerActionThrottle struct {
	Value int    `json:"value"`
	Unit  string `json:"unit"`
}

// MonitorTriggerActionExecutionPolicy defines when actions should be executed.
type MonitorTriggerActionExecutionPolicy struct {
	ActionExecutionScope MonitorTriggerActionExecutionScope `json:"action_execution_scope"`
}

// MonitorTriggerActionExecutionScope defines the scope of action execution.
type MonitorTriggerActionExecutionScope struct {
	PerAlert MonitorActionExecutionScopePerAlert `json:"per_alert"`
}

// MonitorActionExecutionScopePerAlert defines per-alert action settings.
type MonitorActionExecutionScopePerAlert struct {
	ActionableAlerts []string `json:"actionable_alerts"`
}

// buildSearchBody converts a query map to an io.Reader for the search request.
func buildSearchBody(query map[string]interface{}) io.Reader {
	body, _ := json.Marshal(query)
	return strings.NewReader(string(body))
}

// parseSearchResponse parses the search response from OpenSearch.
func parseSearchResponse(body io.Reader) (*SearchResponse, error) {
	var response SearchResponse
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&response); err != nil {
		return nil, err
	}
	return &response, nil
}

// ParseLogEntry converts a search hit to a LogEntry struct.
func ParseLogEntry(hit Hit) LogEntry {
	source := hit.Source
	entry := LogEntry{
		Labels: make(map[string]string),
	}

	if ts, ok := source["@timestamp"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			entry.Timestamp = parsed
		}
	}

	if log, ok := source["log"].(string); ok {
		entry.Log = log
		entry.LogLevel = extractLogLevel(log)
	}

	if k8s, ok := source["kubernetes"].(map[string]interface{}); ok {
		if labelMap, ok := k8s["labels"].(map[string]interface{}); ok {
			entry.ComponentID = getStringValue(labelMap, ReplaceDots(ComponentID))
			entry.EnvironmentID = getStringValue(labelMap, ReplaceDots(EnvironmentID))
			entry.ProjectID = getStringValue(labelMap, ReplaceDots(ProjectID))
			entry.Version = getStringValue(labelMap, ReplaceDots(Version))
			entry.VersionID = getStringValue(labelMap, ReplaceDots(VersionID))
			entry.ComponentName = getStringValue(labelMap, ReplaceDots(ComponentName))
			entry.EnvironmentName = getStringValue(labelMap, ReplaceDots(EnvironmentName))
			entry.ProjectName = getStringValue(labelMap, ReplaceDots(ProjectName))
			entry.NamespaceName = getStringValue(labelMap, ReplaceDots(NamespaceName))

			for k, v := range labelMap {
				if str, ok := v.(string); ok {
					entry.Labels[k] = str
				}
			}
		}

		entry.Namespace = getStringValue(k8s, "namespace_name")
		entry.PodNamespace = getStringValue(k8s, "namespace_name")
		entry.PodID = getStringValue(k8s, "pod_id")
		entry.PodName = getStringValue(k8s, "pod_name")
		entry.ContainerName = getStringValue(k8s, "container_name")
	}

	return entry
}

// getStringValue safely extracts a string value from a map.
func getStringValue(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// extractLogLevel extracts log level from log content using common patterns.
func extractLogLevel(log string) string {
	log = strings.ToUpper(log)

	logLevels := []string{"ERROR", "FATAL", "SEVERE", "WARN", "WARNING", "INFO", "DEBUG", "UNDEFINED"}

	for _, level := range logLevels {
		if strings.Contains(log, level) {
			if level == "WARNING" {
				return "WARN"
			}
			return level
		}
	}

	return "INFO"
}
