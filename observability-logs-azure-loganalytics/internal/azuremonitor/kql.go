// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"fmt"
	"strings"
)

const (
	labelNamespace      = "openchoreo.dev/namespace"
	labelComponentUID   = "openchoreo.dev/component-uid"
	labelProjectUID     = "openchoreo.dev/project-uid"
	labelEnvironmentUID = "openchoreo.dev/environment-uid"
	labelReleaseLineUID = "openchoreo.dev/release-line-uid"
)

func BuildAlertKQL(in RuleInput) string {
	if isKQLStatement(in.Query) {
		return in.Query
	}

	var sb strings.Builder
	sb.WriteString("ContainerLogV2")

	if ns := strings.TrimSpace(in.Namespace); ns != "" {
		sb.WriteString("\n| where PodNamespace == ")
		sb.WriteString(kqlString(ns))
	}
	if scope := releaseLineScope(normaliseUID(in.ComponentUID), normaliseUID(in.ReleaseLineUID)); scope != "" {
		sb.WriteString("\n| where ")
		sb.WriteString(scope)
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

// ReleaseLineScoped reports whether BuildAlertKQL scopes the rule by release
// line, to its line or else to its component's main line. A full KQL statement
// is sent as is, so it is not.
func ReleaseLineScoped(in RuleInput) bool {
	if isKQLStatement(in.Query) {
		return false
	}
	return normaliseUID(in.ReleaseLineUID) != "" || normaliseUID(in.ComponentUID) != ""
}

// UnscopableLineRule reports whether a release line rule's query is a full
// KQL statement, which BuildAlertKQL sends as is and so can't scope to the
// line.
func UnscopableLineRule(in RuleInput) bool {
	return normaliseUID(in.ReleaseLineUID) != "" && isKQLStatement(in.Query)
}

// releaseLineScope renders the predicate scoping a rule by release line. One
// line matches on its UID alone, and the component UID alone keeps its main
// line, the pods with the UID and no release line label. Empty without either.
func releaseLineScope(componentUID, lineUID string) string {
	switch {
	case lineUID != "":
		return podLabel(labelReleaseLineUID) + " == " + kqlString(lineUID)
	case componentUID != "":
		return podLabel(labelComponentUID) + " == " + kqlString(componentUID) +
			" and isempty(" + podLabel(labelReleaseLineUID) + ")"
	}
	return ""
}

// podLabel renders the value of one pod label, empty when the pod lacks it.
func podLabel(key string) string {
	return "tostring(parse_json(tostring(KubernetesMetadata.podLabels))[" + kqlString(key) + "])"
}

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
