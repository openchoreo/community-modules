// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import (
	"encoding/json"
	"fmt"
	"strings"

	"cloud.google.com/go/logging"
)

// mapComponentEntry projects a Cloud Logging entry onto a ComponentLogEntry.
// Pod labels live on entry.Labels under the k8s-pod/ prefix; resource labels
// (namespace_name, pod_name, container_name) live on entry.Resource.Labels.
func mapComponentEntry(e *logging.Entry) ComponentLogEntry {
	msg := payloadString(e.Payload)
	res := resourceLabels(e)
	return ComponentLogEntry{
		Timestamp:           e.Timestamp.UTC(),
		LogMessage:          msg,
		LogLevel:            resolveLogLevel(e.Severity, msg),
		PodName:             res["pod_name"],
		ContainerName:       res["container_name"],
		PodNamespace:        res["namespace_name"],
		ComponentUID:        e.Labels[podLabelKey(LabelComponentUID)],
		ProjectUID:          e.Labels[podLabelKey(LabelProjectUID)],
		EnvironmentUID:      e.Labels[podLabelKey(LabelEnvironmentUID)],
		OpenChoreoNamespace: e.Labels[podLabelKey(LabelNamespace)],
	}
}

// mapWorkflowEntry projects a Cloud Logging entry onto a WorkflowLogEntry.
func mapWorkflowEntry(e *logging.Entry) WorkflowLogEntry {
	return WorkflowLogEntry{
		Timestamp:  e.Timestamp.UTC(),
		LogMessage: payloadString(e.Payload),
	}
}

// resourceLabels returns the monitored-resource labels map, or an empty map
// when the entry carries no resource.
func resourceLabels(e *logging.Entry) map[string]string {
	if e.Resource == nil {
		return map[string]string{}
	}
	return e.Resource.Labels
}

// payloadString renders an entry payload as a string. Cloud Logging payloads
// are one of: a plain string (textPayload), a structured map (jsonPayload),
// or a proto. Structured payloads are returned as compact JSON so the full
// record is preserved for the caller.
func payloadString(payload interface{}) string {
	switch v := payload.(type) {
	case nil:
		return ""
	case string:
		return v
	case map[string]interface{}:
		// Prefer a conventional message field when present.
		for _, key := range []string{"message", "msg", "log"} {
			if raw, ok := v[key]; ok {
				if s, ok := raw.(string); ok && s != "" {
					return s
				}
			}
		}
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	}
}

// resolveLogLevel derives an OpenChoreo level (DEBUG|INFO|WARN|ERROR) for an
// entry, mirroring the sibling adapters' approach:
//  1. Use the entry's structured Cloud Logging severity when it carries one.
//  2. Otherwise, if the message is a JSON envelope, check common level fields.
//  3. Otherwise, scan the message text for a level keyword.
//  4. Default to INFO.
func resolveLogLevel(sev logging.Severity, msg string) string {
	if l := fromGCPSeverity(sev); l != "" {
		return l
	}

	// Structured JSON envelope (e.g. {"level":"info","msg":"..."}).
	if len(msg) > 0 && msg[0] == '{' {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(msg), &envelope); err == nil {
			for _, key := range []string{"level", "logLevel", "severity", "severityText", "severity_text"} {
				if raw, ok := envelope[key]; ok {
					var s string
					if err := json.Unmarshal(raw, &s); err == nil && s != "" {
						return normalizeLevel(s)
					}
				}
			}
		}
	}

	return extractLogLevel(msg)
}

// fromGCPSeverity maps a Cloud Logging severity onto the OpenChoreo level set.
// GCP's NOTICE collapses to INFO; CRITICAL/ALERT/EMERGENCY collapse to ERROR
// (they are all >= ERROR). Returns "" for the default/unset severity so the
// caller falls back to message inspection.
func fromGCPSeverity(sev logging.Severity) string {
	switch sev {
	case logging.Debug:
		return "DEBUG"
	case logging.Info, logging.Notice:
		return "INFO"
	case logging.Warning:
		return "WARN"
	case logging.Error, logging.Critical, logging.Alert, logging.Emergency:
		return "ERROR"
	default: // logging.Default (unset)
		return ""
	}
}

// extractLogLevel scans the log message text for a level keyword and returns a
// normalized level string. Defaults to INFO when no keyword is found.
func extractLogLevel(msg string) string {
	upper := strings.ToUpper(msg)
	for _, level := range []string{"ERROR", "FATAL", "SEVERE", "WARN", "WARNING", "INFO", "DEBUG"} {
		if strings.Contains(upper, level) {
			if level == "WARNING" {
				return "WARN"
			}
			if level == "FATAL" || level == "SEVERE" {
				return "ERROR"
			}
			return level
		}
	}
	return "INFO"
}

// normalizeLevel uppercases the level and maps known aliases onto the
// OpenChoreo level set.
func normalizeLevel(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "WARNING":
		return "WARN"
	case "INFORMATION", "INFORMATIONAL", "NOTICE":
		return "INFO"
	case "CRITICAL", "FATAL", "SEVERE", "ALERT", "EMERGENCY":
		return "ERROR"
	case "TRACE":
		return "DEBUG"
	default:
		return strings.ToUpper(strings.TrimSpace(s))
	}
}
