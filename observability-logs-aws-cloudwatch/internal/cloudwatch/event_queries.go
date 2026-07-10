// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"fmt"
	"strings"
)

// eventResultFields lists the aliased columns every events query projects. The
// aliases become the keys of the row maps runQuery returns, so the client mapping
// can read them by clean name instead of the raw flattened JSON path.
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

// buildComponentEventsQuery constructs a CloudWatch Logs Insights query for
// component-scoped Kubernetes events. It filters on the OpenChoreo scope labels the
// k8seventenrich processor copied onto each event's involved object — mirroring
// buildComponentQuery, but against the OTEL/awscloudwatchlogsexporter field shape.
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

// buildWorkflowEventsQuery constructs a CloudWatch Logs Insights query for
// workflow-scoped events. Workflow pods/jobs are named "<workflowRunName>-<...>" and
// run in the "workflows-<namespace>" Kubernetes namespace, so — like the workflow
// log query — events are matched by the involved object's name and namespace rather
// than the OpenChoreo component labels.
func buildWorkflowEventsQuery(p WorkflowEventsParams) string {
	var b strings.Builder

	writeEventFields(&b)

	fmt.Fprintf(&b, "| filter %s = \"workflows-%s\"\n", eventField(evObjectNamespace), escapeInsights(p.Namespace))
	if p.WorkflowRunName != "" {
		// Match the workflow object itself ("<run>") and its pods/jobs ("<run>-<...>").
		fmt.Fprintf(&b, "| filter %s like /^%s(-|$)/\n", eventField(evObjectName), escapeInsightsRegex(p.WorkflowRunName))
	}
	if p.TaskName != "" {
		// Workflow controller events keep the step name in the message body, while
		// pod/job events may include either the step name or the referenced template
		// name in the object name. For example, the checkout-source step creates
		// pods named "<workflow>-checkout-<hash>".
		taskName := escapeInsights(p.TaskName)
		fmt.Fprintf(&b, "| filter %s like \"%s\" or %s like \"%s\"",
			eventField(evObjectName), taskName,
			eventField(evMessage), taskName,
		)
		if p.WorkflowRunName != "" {
			for _, objectPrefix := range workflowTaskObjectPrefixes(p.TaskName) {
				fmt.Fprintf(&b, " or %s like /^%s-%s(-|$)/",
					eventField(evObjectName),
					escapeInsightsRegex(p.WorkflowRunName),
					escapeInsightsRegex(objectPrefix),
				)
			}
		}
		b.WriteString("\n")
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
