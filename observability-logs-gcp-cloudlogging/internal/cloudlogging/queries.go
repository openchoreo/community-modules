// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import (
	"fmt"
	"strings"
	"time"
)

// BuildComponentLogsFilter renders a ComponentLogsParams as a Cloud Logging
// filter string against k8s_container logs. Only the namespace is required;
// the UID filters are added when non-empty. Time bounds are included in the
// filter (Cloud Logging has no separate timespan parameter like Azure's
// Timespan option).
func BuildComponentLogsFilter(p ComponentLogsParams) string {
	// Scope by the OpenChoreo *logical* namespace (the openchoreo.dev/namespace
	// pod label), NOT the synthesized Kubernetes namespace. The Observer passes
	// the logical namespace (e.g. "default"); the workload actually runs in a
	// dp-<...> Kubernetes namespace, so filtering resource.labels.namespace_name
	// would never match. This mirrors the Azure and AWS adapters, which both
	// filter component-scope queries on the openchoreo.dev/namespace label.
	clauses := []string{
		fmt.Sprintf(`resource.type=%s`, quote(k8sContainerResource)),
		labelEquals(LabelNamespace, p.Namespace),
	}

	if p.ComponentUID != "" {
		clauses = append(clauses, labelEquals(LabelComponentUID, p.ComponentUID))
	}
	if p.ProjectUID != "" {
		clauses = append(clauses, labelEquals(LabelProjectUID, p.ProjectUID))
	}
	if p.EnvironmentUID != "" {
		clauses = append(clauses, labelEquals(LabelEnvironmentUID, p.EnvironmentUID))
	}

	clauses = append(clauses, timeRangeClauses(p.StartTime, p.EndTime)...)
	if c := severityClause(p.LogLevels); c != "" {
		clauses = append(clauses, c)
	}
	if c := searchPhraseClause(p.SearchPhrase); c != "" {
		clauses = append(clauses, c)
	}

	return strings.Join(clauses, "\n")
}

// BuildWorkflowLogsFilter renders a WorkflowLogsParams as a Cloud Logging
// filter. Workflow pods land in workflows-<openchoreoNamespace> per Argo's
// convention; Argo infra containers (init, wait) are excluded.
func BuildWorkflowLogsFilter(p WorkflowLogsParams) string {
	clauses := []string{
		fmt.Sprintf(`resource.type=%s`, quote(k8sContainerResource)),
		fmt.Sprintf(`resource.labels.namespace_name=%s`, quote(WorkflowNamespacePrefix+p.Namespace)),
	}

	if p.WorkflowRunName != "" {
		// pod_name starts with the workflow run name; Cloud Logging has no
		// startswith, so use the has/contains (:) operator scoped to pod_name.
		clauses = append(clauses, fmt.Sprintf(`resource.labels.pod_name:%s`, quote(p.WorkflowRunName)))
	}

	// Exclude Argo infra containers.
	clauses = append(clauses,
		fmt.Sprintf(`resource.labels.container_name!=%s`, quote("init")),
		fmt.Sprintf(`resource.labels.container_name!=%s`, quote("wait")),
	)

	clauses = append(clauses, timeRangeClauses(p.StartTime, p.EndTime)...)
	if c := severityClause(p.LogLevels); c != "" {
		clauses = append(clauses, c)
	}
	if c := searchPhraseClause(p.SearchPhrase); c != "" {
		clauses = append(clauses, c)
	}

	return strings.Join(clauses, "\n")
}

// labelEquals renders an equality clause against a GKE pod label, using the
// verified field path labels."k8s-pod/<rawKey>".
func labelEquals(rawKey, value string) string {
	return fmt.Sprintf(`labels.%s=%s`, quote(podLabelKey(rawKey)), quote(value))
}

// timeRangeClauses renders the timestamp bounds. Bounds are only emitted when
// non-zero so callers can omit either side.
func timeRangeClauses(start, end time.Time) []string {
	var out []string
	if !start.IsZero() {
		out = append(out, fmt.Sprintf(`timestamp>=%s`, quote(start.UTC().Format(time.RFC3339Nano))))
	}
	if !end.IsZero() {
		out = append(out, fmt.Sprintf(`timestamp<=%s`, quote(end.UTC().Format(time.RFC3339Nano))))
	}
	return out
}

// severityClause maps the OpenChoreo log levels onto a Cloud Logging severity
// filter. OpenChoreo uses WARN; Cloud Logging uses WARNING — the mapping in
// toGCPSeverity handles that. Multiple levels are OR-ed.
func severityClause(levels []string) string {
	if len(levels) == 0 {
		return ""
	}
	terms := make([]string, 0, len(levels))
	for _, l := range levels {
		gcp := toGCPSeverity(l)
		if gcp == "" {
			continue
		}
		terms = append(terms, fmt.Sprintf(`severity=%q`, gcp))
	}
	if len(terms) == 0 {
		return ""
	}
	if len(terms) == 1 {
		return terms[0]
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}

// searchPhraseClause renders a substring filter over the log payload. The :
// (has/contains) operator matches the phrase anywhere in the textPayload or
// jsonPayload.message field.
func searchPhraseClause(phrase string) string {
	if phrase == "" {
		return ""
	}
	q := quote(phrase)
	return fmt.Sprintf(`(textPayload:%s OR jsonPayload.message:%s)`, q, q)
}

// toGCPSeverity maps an OpenChoreo log level (DEBUG|INFO|WARN|ERROR) to the
// Cloud Logging LogSeverity enum name. Returns "" for unrecognized levels so
// the caller can drop them rather than build an invalid filter.
func toGCPSeverity(level string) string {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return "DEBUG"
	case "INFO":
		return "INFO"
	case "WARN", "WARNING":
		return "WARNING"
	case "ERROR":
		return "ERROR"
	default:
		return ""
	}
}

// quote wraps a value in double quotes for a Cloud Logging filter string,
// escaping backslashes and embedded double quotes. Newlines are flattened so
// a value can never break out of the quoted literal.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
