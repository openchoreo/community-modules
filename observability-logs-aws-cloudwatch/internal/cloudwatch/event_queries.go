// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"fmt"
	"strings"
)

// eventResultFields maps each Insights field path to the alias used as the row key.
var eventResultFields = []struct {
	source string
	alias  string
}{
	{evMessage, "message"},
	{evSeverityText, "type"},
	{evReason, "reason"},
	{evObjectKind, "objectKind"},
	{evObjectName, "objectName"},
	{evObjectNamespace, "objectNamespace"},
	{evComponentUID, "componentUid"},
	{evComponentName, "componentName"},
	{evEnvironmentUID, "environmentUid"},
	{evEnvironmentName, "environmentName"},
	{evProjectUID, "projectUid"},
	{evProjectName, "projectName"},
	{evNamespaceName, "namespaceName"},
}

// writeEventFields writes the shared `fields @timestamp, <event columns>` projection.
func writeEventFields(b *strings.Builder) {
	b.WriteString("fields @timestamp")
	for _, f := range eventResultFields {
		fmt.Fprintf(b, ", %s as %s", eventField(f.source), f.alias)
	}
	b.WriteString("\n")
}

// buildComponentEventsQuery builds the Insights query for component-scoped events,
// filtering on the OpenChoreo scope labels.
func buildComponentEventsQuery(p ComponentEventsParams) string {
	var b strings.Builder

	writeEventFields(&b)

	fmt.Fprintf(&b, "| filter %s = \"%s\"\n", eventField(evNamespaceName), escapeInsights(p.Namespace))
	if p.ProjectID != "" {
		fmt.Fprintf(&b, "| filter %s = \"%s\"\n", eventField(evProjectUID), escapeInsights(p.ProjectID))
	}
	if p.EnvironmentID != "" {
		fmt.Fprintf(&b, "| filter %s = \"%s\"\n", eventField(evEnvironmentUID), escapeInsights(p.EnvironmentID))
	}
	if p.ComponentID != "" {
		fmt.Fprintf(&b, "| filter %s = \"%s\"\n", eventField(evComponentUID), escapeInsights(p.ComponentID))
	}

	fmt.Fprintf(&b, "| sort @timestamp %s\n", normaliseSortOrder(p.SortOrder))
	fmt.Fprintf(&b, "| limit %d", insightsLimit(p.Limit))

	return b.String()
}

// buildWorkflowEventsQuery builds the Insights query for workflow-scoped events,
// matching by object name and the "workflows-<namespace>" namespace.
func buildWorkflowEventsQuery(p WorkflowEventsParams) string {
	var b strings.Builder

	writeEventFields(&b)

	fmt.Fprintf(&b, "| filter %s = \"workflows-%s\"\n", eventField(evObjectNamespace), escapeInsights(p.Namespace))
	// TaskName only narrows within a run, so it applies only alongside WorkflowRunName
	// (the handler guarantees WorkflowRunName is set whenever TaskName is).
	if p.WorkflowRunName != "" {
		// Match the workflow object ("<run>") and its pods/jobs ("<run>-<...>").
		fmt.Fprintf(&b, "| filter %s like /^%s(-|$)/\n", eventField(evObjectName), escapeInsightsRegex(p.WorkflowRunName))

		if p.TaskName != "" {
			// Task name may appear in the message body or the object name.
			taskName := escapeInsights(p.TaskName)
			fmt.Fprintf(&b, "| filter %s like \"%s\" or %s like \"%s\"",
				eventField(evObjectName), taskName,
				eventField(evMessage), taskName,
			)
			for _, objectPrefix := range workflowTaskObjectPrefixes(p.TaskName) {
				fmt.Fprintf(&b, " or %s like /^%s-%s(-|$)/",
					eventField(evObjectName),
					escapeInsightsRegex(p.WorkflowRunName),
					escapeInsightsRegex(objectPrefix),
				)
			}
			b.WriteString("\n")
		}
	}

	fmt.Fprintf(&b, "| sort @timestamp %s\n", normaliseSortOrder(p.SortOrder))
	fmt.Fprintf(&b, "| limit %d", insightsLimit(p.Limit))

	return b.String()
}

func workflowTaskObjectPrefixes(taskName string) []string {
	prefixes := []string{taskName}
	if cut, _, ok := strings.Cut(taskName, "-"); ok && cut != "" && cut != taskName {
		prefixes = append(prefixes, cut)
	}
	return prefixes
}
