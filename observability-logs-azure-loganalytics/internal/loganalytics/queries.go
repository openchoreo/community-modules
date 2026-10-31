// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"fmt"
	"strings"
)

// BuildComponentLogsKQL renders a ComponentLogsParams as a KQL query string
// against ContainerLogV2. Only the namespace is required; the UID filters
// are added when non-empty. Time range is passed via the SDK's Timespan
// option, not the query body, so the query is portable between /query and
// /search if we ever need /search.
func BuildComponentLogsKQL(p ComponentLogsParams) string {
	var sb strings.Builder

	sb.WriteString(ContainerLogV2Table)
	sb.WriteString("\n| where ")
	sb.WriteString(podLabelEquals(LabelNamespace, p.Namespace))

	if scope := releaseLineScope(podLabel, p.ComponentUID, p.ReleaseLineUID, p.AllReleaseLines); scope != "" {
		sb.WriteString("\n| where ")
		sb.WriteString(scope)
	}
	if p.ProjectUID != "" {
		sb.WriteString("\n| where ")
		sb.WriteString(podLabelEquals(LabelProjectUID, p.ProjectUID))
	}
	if p.EnvironmentUID != "" {
		sb.WriteString("\n| where ")
		sb.WriteString(podLabelEquals(LabelEnvironmentUID, p.EnvironmentUID))
	}

	if len(p.LogLevels) > 0 {
		sb.WriteString("\n| where LogLevel in (")
		for i, lvl := range p.LogLevels {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(kqlString(lvl))
		}
		sb.WriteString(")")
	}

	if p.SearchPhrase != "" {
		sb.WriteString("\n| where tostring(LogMessage) contains ")
		sb.WriteString(kqlString(p.SearchPhrase))
	}

	sb.WriteString("\n| order by TimeGenerated ")
	sb.WriteString(string(sortOrderOrDefault(p.SortOrder)))

	if p.Limit > 0 {
		sb.WriteString(fmt.Sprintf("\n| take %d", p.Limit))
	}

	// Project the columns the handler will map onto ComponentLogEntry.
	// Pod labels live inside KubernetesMetadata.podLabels (a dynamic blob);
	// extract them as top-level columns for the handler. A release line's pods
	// carry the component UID under their own label.
	sb.WriteString(`
| extend _labels = parse_json(tostring(KubernetesMetadata.podLabels))
| project
    TimeGenerated,
    LogMessage = tostring(LogMessage),
    LogLevel,
    PodName,
    ContainerName,
    PodNamespace,
    ComponentUID        = coalesce(tostring(_labels["openchoreo.dev/component-uid"]), tostring(_labels["openchoreo.dev/release-line-component-uid"])),
    ProjectUID          = tostring(_labels["openchoreo.dev/project-uid"]),
    EnvironmentUID      = tostring(_labels["openchoreo.dev/environment-uid"]),
    OpenChoreoNamespace = tostring(_labels["openchoreo.dev/namespace"])`)

	return sb.String()
}

// BuildWorkflowLogsKQL renders a WorkflowLogsParams as a KQL query.
// Workflow pods land in workflows-<openchoreoNamespace> per Argo's convention.
func BuildWorkflowLogsKQL(p WorkflowLogsParams) string {
	var sb strings.Builder

	sb.WriteString(ContainerLogV2Table)
	sb.WriteString("\n| where PodNamespace == ")
	sb.WriteString(kqlString(WorkflowNamespacePrefix + p.Namespace))

	if p.WorkflowRunName != "" {
		sb.WriteString("\n| where PodName startswith ")
		sb.WriteString(kqlString(p.WorkflowRunName))
	}

	// Exclude Argo infra containers from workflow logs.
	sb.WriteString(`
| where ContainerName !in ("init", "wait")`)

	if len(p.LogLevels) > 0 {
		sb.WriteString("\n| where LogLevel in (")
		for i, lvl := range p.LogLevels {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(kqlString(lvl))
		}
		sb.WriteString(")")
	}

	if p.SearchPhrase != "" {
		sb.WriteString("\n| where tostring(LogMessage) contains ")
		sb.WriteString(kqlString(p.SearchPhrase))
	}

	sb.WriteString("\n| order by TimeGenerated ")
	sb.WriteString(string(sortOrderOrDefault(p.SortOrder)))

	if p.Limit > 0 {
		sb.WriteString(fmt.Sprintf("\n| take %d", p.Limit))
	}

	sb.WriteString(`
| project
    TimeGenerated,
    LogMessage = tostring(LogMessage)`)

	return sb.String()
}

// PingKQL is a near-zero-cost query used at boot to validate credentials
// and workspace reachability.
func PingKQL() string {
	return ContainerLogV2Table + " | take 1"
}

func sortOrderOrDefault(s SortOrder) SortOrder {
	if s == SortAsc {
		return SortAsc
	}
	return SortDesc
}

// podLabel renders the value of one pod label, empty when the pod lacks it.
// Pod labels live inside KubernetesMetadata.podLabels, a dynamic blob, so the
// key is an index expression rather than a column.
func podLabel(key string) string {
	return "tostring(parse_json(tostring(KubernetesMetadata.podLabels))[" + kqlString(key) + "])"
}

// podLabelEquals renders an equality predicate against one pod label.
func podLabelEquals(key, value string) string {
	return podLabel(key) + " == " + kqlString(value)
}

// releaseLineScope renders the predicate placing a record on a component's
// release lines, label rendering the value of one of the record's labels. The
// component UID alone keeps its main line's records, those with the UID and no
// release line label. A release line's pods carry the component UID under
// another label than the main line's, so allLines matches either label and
// covers every line. One release line matches on its UID alone. Empty without a
// component or a line.
func releaseLineScope(label func(string) string, componentUID, lineUID string, allLines bool) string {
	switch {
	case lineUID != "":
		return label(LabelReleaseLineUID) + " == " + kqlString(lineUID)
	case componentUID == "":
		return ""
	case allLines:
		return "(" + label(LabelComponentUID) + " == " + kqlString(componentUID) +
			" or " + label(LabelReleaseLineComponentUID) + " == " + kqlString(componentUID) + ")"
	}
	return label(LabelComponentUID) + " == " + kqlString(componentUID) +
		" and isempty(" + label(LabelReleaseLineUID) + ")"
}

func kqlString(s string) string {
	// Replace control characters that KQL would otherwise interpret.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
