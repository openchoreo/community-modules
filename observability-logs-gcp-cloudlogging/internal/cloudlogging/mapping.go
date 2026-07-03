// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import (
	"encoding/json"
	"fmt"
	"strings"

	"cloud.google.com/go/logging"
)

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

func mapWorkflowEntry(e *logging.Entry) WorkflowLogEntry {
	return WorkflowLogEntry{
		Timestamp:  e.Timestamp.UTC(),
		LogMessage: payloadString(e.Payload),
	}
}

func resourceLabels(e *logging.Entry) map[string]string {
	if e.Resource == nil {
		return map[string]string{}
	}
	return e.Resource.Labels
}

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
