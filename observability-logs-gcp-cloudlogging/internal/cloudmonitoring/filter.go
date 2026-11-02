// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"fmt"
	"strings"
)

const (
	// Raw OpenChoreo pod-label keys stamped by the controllers.
	labelNamespace      = "openchoreo.dev/namespace"
	labelComponentUID   = "openchoreo.dev/component-uid"
	labelProjectUID     = "openchoreo.dev/project-uid"
	labelEnvironmentUID = "openchoreo.dev/environment-uid"
	labelReleaseLineUID = "openchoreo.dev/release-line-uid"

	// podLabelPrefix is how GKE surfaces pod labels on a LogEntry.
	podLabelPrefix = "k8s-pod/"

	k8sContainerResource = "k8s_container"
)

// BuildAlertFilter renders the log-based-metric filter for an alert rule. A
// raw Cloud Logging filter (see isRawFilter) is used verbatim; otherwise the
// filter is composed from the rule's scope plus a free-text phrase.
func BuildAlertFilter(in RuleInput) string {
	if isRawFilter(in.Query) {
		return in.Query
	}

	clauses := []string{fmt.Sprintf(`resource.type=%s`, quote(k8sContainerResource))}

	if ns := strings.TrimSpace(in.Namespace); ns != "" {
		clauses = append(clauses, labelEquals(labelNamespace, ns))
	}
	clauses = append(clauses, releaseLineScopeClauses(normalizeUID(in.ComponentUID), normalizeUID(in.ReleaseLineUID))...)
	if uid := normalizeUID(in.ProjectUID); uid != "" {
		clauses = append(clauses, labelEquals(labelProjectUID, uid))
	}
	if uid := normalizeUID(in.EnvironmentUID); uid != "" {
		clauses = append(clauses, labelEquals(labelEnvironmentUID, uid))
	}
	if phrase := strings.TrimSpace(in.Query); phrase != "" {
		clauses = append(clauses, fmt.Sprintf(`(textPayload:%s OR jsonPayload.message:%s)`, quote(phrase), quote(phrase)))
	}

	return strings.Join(clauses, "\n")
}

// ReleaseLineApplied reports whether BuildAlertFilter scopes the rule to its
// component's main line or to its release line. A raw filter is used
// verbatim, so it is never scoped.
func ReleaseLineApplied(in RuleInput) bool {
	if isRawFilter(in.Query) {
		return false
	}
	return normalizeUID(in.ReleaseLineUID) != "" || normalizeUID(in.ComponentUID) != ""
}

// UnscopableLineRule reports whether a release line rule's query is a raw
// Cloud Logging filter, which BuildAlertFilter uses verbatim and so can't
// scope to the line.
func UnscopableLineRule(in RuleInput) bool {
	return normalizeUID(in.ReleaseLineUID) != "" && isRawFilter(in.Query)
}

// releaseLineScopeClauses places a rule on one of a component's lines: a
// release line matches on its UID alone, since its pods don't carry the
// component UID, and the component UID alone keeps its main line's entries,
// those without a release line label. Returns nil without a component or a
// line.
func releaseLineScopeClauses(componentUID, lineUID string) []string {
	switch {
	case lineUID != "":
		return []string{labelEquals(labelReleaseLineUID, lineUID)}
	case componentUID == "":
		return nil
	}
	return []string{labelEquals(labelComponentUID, componentUID), labelAbsent(labelReleaseLineUID)}
}

func isRawFilter(q string) bool {
	t := strings.TrimSpace(q)
	// Nested fields are always followed by a subfield, e.g. resource.type=...,
	// labels."k8s-pod/...", jsonPayload.message:...
	for _, prefix := range []string{"resource.", "labels.", "jsonPayload.", "protoPayload.", "httpRequest."} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	// Scalar fields must be followed by a comparison/restriction operator to
	// count as a filter (not just be a word that happens to start the phrase).
	for _, field := range []string{"severity", "logName", "textPayload", "timestamp", "trace", "spanId", "insertId"} {
		if rest, ok := strings.CutPrefix(t, field); ok && startsWithFilterOperator(rest) {
			return true
		}
	}
	return false
}

func startsWithFilterOperator(s string) bool {
	s = strings.TrimLeft(s, " ")
	for _, op := range []string{"=", "!=", "<", ">", ":"} {
		if strings.HasPrefix(s, op) {
			return true
		}
	}
	return false
}

func labelEquals(rawKey, value string) string {
	return fmt.Sprintf(`labels.%s=%s`, quote(podLabelKey(rawKey)), quote(value))
}

// labelAbsent matches entries without the pod label: NOT on a missing field is
// true, and :* tests for presence.
func labelAbsent(rawKey string) string {
	return fmt.Sprintf(`NOT labels.%s:*`, quote(podLabelKey(rawKey)))
}

func podLabelKey(rawKey string) string {
	return podLabelPrefix + strings.ReplaceAll(rawKey, ".", "_")
}

func normalizeUID(u string) string {
	if u == "" || u == "00000000-0000-0000-0000-000000000000" {
		return ""
	}
	return u
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
