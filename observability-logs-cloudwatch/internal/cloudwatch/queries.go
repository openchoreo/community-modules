// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"fmt"
	"regexp"
	"strings"
)

// Standard OpenChoreo labels — these match what the controllers stamp onto
// workload pods and therefore onto every log record Fluent Bit ships.
const (
	labelNamespace       = "openchoreo.dev/namespace"
	labelComponentUID    = "openchoreo.dev/component-uid"
	labelComponentName   = "openchoreo.dev/component-name"
	labelEnvironmentUID  = "openchoreo.dev/environment-uid"
	labelEnvironmentName = "openchoreo.dev/environment-name"
	labelProjectUID      = "openchoreo.dev/project-uid"
	labelProjectName     = "openchoreo.dev/project-name"

	// Argo Workflows annotation carrying the workflow node (pod-scoped step) name.
	annotationWorkflowNodeName = "workflows.argoproj.io/node-name"
)

var logLevelFieldNames = []string{
	"logLevel",
	"level",
	"severity",
	"severityText",
	"severity_text",
	"log_processed.logLevel",
	"log_processed.level",
	"log_processed.severity",
	"log_processed.severityText",
	"log_processed.severity_text",
}

var logLevelResultFields = []struct {
	source string
	alias  string
}{
	{"logLevel", "logLevel"},
	{"level", "level"},
	{"severity", "severity"},
	{"severityText", "severityText"},
	{"severity_text", "severity_text"},
	{"log_processed.logLevel", "logProcessedLogLevel"},
	{"log_processed.level", "logProcessedLevel"},
	{"log_processed.severity", "logProcessedSeverity"},
	{"log_processed.severityText", "logProcessedSeverityText"},
	{"log_processed.severity_text", "logProcessedSeverity_text"},
}

// labelField renders a CloudWatch Insights accessor for a Kubernetes label key.
// Insights flattens nested JSON into dotted field names; when the flattened name
// contains non-alphanumeric characters (dots or slashes in the label key), the
// entire flattened path must be wrapped in backticks.
func labelField(key string) string {
	return fmt.Sprintf("`kubernetes.labels.%s`", key)
}

// annotationField renders a CloudWatch Insights accessor for a Kubernetes annotation key.
func annotationField(key string) string {
	return fmt.Sprintf("`kubernetes.annotations.%s`", key)
}

// escapeInsights escapes a user-provided literal for inclusion inside a double-quoted
// Logs Insights string. Insights treats `\` as the escape character.
func escapeInsights(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
}

func escapeInsightsRegex(v string) string {
	return regexp.QuoteMeta(v)
}

// buildComponentQuery constructs a CloudWatch Logs Insights query that emits the
// columns the adapter maps back into the Observer's ComponentLogEntry shape.
func buildComponentQuery(p ComponentLogsParams) string {
	var b strings.Builder

	b.WriteString("fields @timestamp, @message,")
	b.WriteString(" kubernetes.namespace_name as namespace,")
	b.WriteString(" kubernetes.pod_name as podName,")
	b.WriteString(" kubernetes.container_name as containerName,")
	fmt.Fprintf(&b, " %s as componentUid,", labelField(labelComponentUID))
	fmt.Fprintf(&b, " %s as componentName,", labelField(labelComponentName))
	fmt.Fprintf(&b, " %s as environmentUid,", labelField(labelEnvironmentUID))
	fmt.Fprintf(&b, " %s as environmentName,", labelField(labelEnvironmentName))
	fmt.Fprintf(&b, " %s as projectUid,", labelField(labelProjectUID))
	fmt.Fprintf(&b, " %s as projectName", labelField(labelProjectName))
	for _, field := range logLevelResultFields {
		fmt.Fprintf(&b, ", %s as %s", field.source, field.alias)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "| filter %s = \"%s\"\n", labelField(labelNamespace), escapeInsights(p.Namespace))

	if p.ProjectID != "" {
		fmt.Fprintf(&b, "| filter %s = \"%s\"\n", labelField(labelProjectUID), escapeInsights(p.ProjectID))
	}
	if p.EnvironmentID != "" {
		fmt.Fprintf(&b, "| filter %s = \"%s\"\n", labelField(labelEnvironmentUID), escapeInsights(p.EnvironmentID))
	}
	if len(p.ComponentIDs) > 0 {
		b.WriteString("| filter ")
		for i, id := range p.ComponentIDs {
			if i > 0 {
				b.WriteString(" or ")
			}
			fmt.Fprintf(&b, "%s = \"%s\"", labelField(labelComponentUID), escapeInsights(id))
		}
		b.WriteString("\n")
	}
	if p.SearchPhrase != "" {
		fmt.Fprintf(&b, "| filter @message like \"%s\"\n", escapeInsights(p.SearchPhrase))
	}
	writeLogLevelFilter(&b, p.LogLevels)

	fmt.Fprintf(&b, "| sort @timestamp %s\n", normaliseSortOrder(p.SortOrder))
	fmt.Fprintf(&b, "| limit %d", insightsLimit(p.Limit))

	return b.String()
}

// buildWorkflowQuery constructs a CloudWatch Logs Insights query for workflow-scoped logs.
func buildWorkflowQuery(p WorkflowLogsParams) string {
	var b strings.Builder

	b.WriteString("fields @timestamp, @message\n")
	fmt.Fprintf(&b, "| filter kubernetes.namespace_name = \"%s\"\n", escapeInsights(p.Namespace))
	if p.WorkflowRunName != "" {
		fmt.Fprintf(&b, "| filter %s = \"%s\"\n", annotationField(annotationWorkflowNodeName), escapeInsights(p.WorkflowRunName))
	}
	if p.SearchPhrase != "" {
		fmt.Fprintf(&b, "| filter @message like \"%s\"\n", escapeInsights(p.SearchPhrase))
	}
	writeLogLevelFilter(&b, p.LogLevels)

	fmt.Fprintf(&b, "| sort @timestamp %s\n", normaliseSortOrder(p.SortOrder))
	fmt.Fprintf(&b, "| limit %d", insightsLimit(p.Limit))

	return b.String()
}

func normaliseSortOrder(s string) string {
	if strings.EqualFold(s, "asc") {
		return "asc"
	}
	return "desc"
}

// insightsLimit clamps the requested limit into the CloudWatch-safe range.
// The API hard-caps Insights queries at 10000 rows; the Observer caps at 1000.
func insightsLimit(n int) int {
	if n <= 0 {
		return 1000
	}
	if n > 10000 {
		return 10000
	}
	return n
}

func writeLogLevelFilter(b *strings.Builder, levels []string) {
	if len(levels) == 0 {
		return
	}

	var conditions []string
	for _, level := range levels {
		level = strings.TrimSpace(level)
		if level == "" {
			continue
		}
		var levelConditions []string
		for _, field := range logLevelFieldNames {
			levelConditions = append(levelConditions, fmt.Sprintf("%s = \"%s\"", field, escapeInsights(level)))
		}
		levelConditions = append(levelConditions, fmt.Sprintf("@message like /(?i)%s/", escapeInsightsRegex(level)))
		conditions = append(conditions, "("+strings.Join(levelConditions, " or ")+")")
	}
	if len(conditions) == 0 {
		return
	}

	b.WriteString("| filter ")
	b.WriteString(strings.Join(conditions, " or "))
	b.WriteString("\n")
}
