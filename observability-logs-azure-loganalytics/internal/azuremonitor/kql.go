// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"fmt"
	"strings"
)

// Label keys mirror the constants in internal/loganalytics/labels.go to keep
// the alert query consistent with the log query path. Duplicated here to keep
// the azuremonitor package self-contained.
const (
	labelNamespace      = "openchoreo.dev/namespace"
	labelComponentUID   = "openchoreo.dev/component-uid"
	labelProjectUID     = "openchoreo.dev/project-uid"
	labelEnvironmentUID = "openchoreo.dev/environment-uid"
)

// BuildAlertKQL constructs a Log Analytics query for an alert rule. If the
// input already starts with "ContainerLogV2" it is treated as a literal KQL
// statement and returned unchanged. Otherwise it is treated as a search
// phrase and wrapped in a count-by-scope KQL that mirrors the log query
// path: filter by openchoreo.dev/* pod labels, scope by namespace + UIDs,
// contains the phrase, project a single integer count column for Azure to
// compare against the threshold.
func BuildAlertKQL(in RuleInput) string {
	if isKQLStatement(in.Query) {
		return in.Query
	}

	var sb strings.Builder
	sb.WriteString("ContainerLogV2")

	if ns := strings.TrimSpace(in.Namespace); ns != "" {
		// The alert rule CR lives in the synthesized DP namespace
		// (dp-<openchoreo-ns>-<project>-<env>-<hash>), which is also the
		// PodNamespace where the workload pods run. Filter on PodNamespace
		// (a top-level column) — faster than parsing KubernetesMetadata
		// and exact-matches the k8s namespace the pod is actually in.
		// The pod label openchoreo.dev/namespace carries the OpenChoreo
		// logical namespace (e.g. "default") which doesn't match the CR's
		// namespace and can't be used here.
		sb.WriteString("\n| where PodNamespace == ")
		sb.WriteString(kqlString(ns))
	}
	if uid := normaliseUID(in.ComponentUID); uid != "" {
		sb.WriteString("\n| where tostring(parse_json(tostring(KubernetesMetadata.podLabels))[")
		sb.WriteString(kqlString(labelComponentUID))
		sb.WriteString("]) == ")
		sb.WriteString(kqlString(uid))
	}
	if uid := normaliseUID(in.ProjectUID); uid != "" {
		sb.WriteString("\n| where tostring(parse_json(tostring(KubernetesMetadata.podLabels))[")
		sb.WriteString(kqlString(labelProjectUID))
		sb.WriteString("]) == ")
		sb.WriteString(kqlString(uid))
	}
	if uid := normaliseUID(in.EnvironmentUID); uid != "" {
		sb.WriteString("\n| where tostring(parse_json(tostring(KubernetesMetadata.podLabels))[")
		sb.WriteString(kqlString(labelEnvironmentUID))
		sb.WriteString("]) == ")
		sb.WriteString(kqlString(uid))
	}

	if phrase := strings.TrimSpace(in.Query); phrase != "" {
		sb.WriteString("\n| where tostring(LogMessage) contains ")
		sb.WriteString(kqlString(phrase))
	}

	return sb.String()
}

// isKQLStatement returns true if the input looks like a hand-written KQL
// query rather than a search phrase. Heuristic: starts with a known table
// name token. This lets advanced users supply full KQL while the common
// path (search phrase) gets wrapped.
func isKQLStatement(q string) bool {
	trimmed := strings.TrimSpace(q)
	for _, prefix := range []string{
		"ContainerLogV2",
		"ContainerLog",
		"AzureDiagnostics",
		"KubeEvents",
		"InsightsMetrics",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// kqlString quotes a Go string as a KQL string literal. KQL uses double
// quotes; embedded double quotes and backslashes are escaped.
func kqlString(s string) string {
	out := strings.ReplaceAll(s, `\`, `\\`)
	out = strings.ReplaceAll(out, `"`, `\"`)
	return fmt.Sprintf(`"%s"`, out)
}

// normaliseUID strips zero-UUIDs that the OpenAPI client sends when a field
// is unset. uuid.UUID's zero value renders as 00000000-0000-0000-0000-000000000000.
func normaliseUID(u string) string {
	if u == "" || u == "00000000-0000-0000-0000-000000000000" {
		return ""
	}
	return u
}
