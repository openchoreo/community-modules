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

	// podLabelPrefix is how GKE surfaces pod labels on a LogEntry.
	podLabelPrefix = "k8s-pod/"

	k8sContainerResource = "k8s_container"
)

// SanitizePodLabelDots mirrors cloudlogging.SanitizePodLabelDots: the modern
// GKE managed logging agent replaces dots in pod-label keys with underscores
// when surfacing them under k8s-pod/<key>. Defaults true; set from config at
// startup so both packages agree.
var SanitizePodLabelDots = true

// BuildAlertFilter renders the log-based-metric filter for an alert rule.
// If the caller supplied a raw Cloud Logging filter (Query starts with a
// resource./labels./severity token), it is used verbatim; otherwise the
// filter is composed from the rule's scope plus a free-text phrase, matching
// the shape the log-query path produces.
func BuildAlertFilter(in RuleInput) string {
	if isRawFilter(in.Query) {
		return in.Query
	}

	clauses := []string{fmt.Sprintf(`resource.type=%s`, quote(k8sContainerResource))}

	if ns := strings.TrimSpace(in.Namespace); ns != "" {
		clauses = append(clauses, labelEquals(labelNamespace, ns))
	}
	if uid := normalizeUID(in.ComponentUID); uid != "" {
		clauses = append(clauses, labelEquals(labelComponentUID, uid))
	}
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

// isRawFilter reports whether the query is already a Cloud Logging filter
// rather than a free-text search phrase.
func isRawFilter(q string) bool {
	t := strings.TrimSpace(q)
	for _, prefix := range []string{"resource.", "labels.", "severity", "logName", "jsonPayload.", "textPayload", "protoPayload."} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

// labelEquals renders an equality clause against a GKE pod label, applying the
// dots-to-underscores sanitization when enabled.
func labelEquals(rawKey, value string) string {
	return fmt.Sprintf(`labels.%s=%s`, quote(podLabelKey(rawKey)), quote(value))
}

func podLabelKey(rawKey string) string {
	if SanitizePodLabelDots {
		rawKey = strings.ReplaceAll(rawKey, ".", "_")
	}
	return podLabelPrefix + rawKey
}

// normalizeUID strips the zero UUID the OpenAPI client sends for unset fields.
func normalizeUID(u string) string {
	if u == "" || u == "00000000-0000-0000-0000-000000000000" {
		return ""
	}
	return u
}

// quote wraps a value in double quotes for a Cloud Logging filter, escaping
// backslashes and embedded quotes and flattening newlines.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
