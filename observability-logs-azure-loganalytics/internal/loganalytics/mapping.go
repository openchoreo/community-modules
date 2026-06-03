// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

func mapComponentRows(resp azlogs.QueryWorkspaceResponse) ([]ComponentLogEntry, error) {
	if len(resp.Tables) == 0 {
		return nil, nil
	}
	t := resp.Tables[0]
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, err
	}

	out := make([]ComponentLogEntry, 0, len(t.Rows))
	for _, row := range t.Rows {
		msg := rowString(row, idx, "LogMessage")
		out = append(out, ComponentLogEntry{
			Timestamp:           rowTime(row, idx, "TimeGenerated"),
			LogMessage:          msg,
			LogLevel:            resolveLogLevel(rowString(row, idx, "LogLevel"), msg),
			PodName:             rowString(row, idx, "PodName"),
			ContainerName:       rowString(row, idx, "ContainerName"),
			PodNamespace:        rowString(row, idx, "PodNamespace"),
			ComponentUID:        rowString(row, idx, "ComponentUID"),
			ProjectUID:          rowString(row, idx, "ProjectUID"),
			EnvironmentUID:      rowString(row, idx, "EnvironmentUID"),
			OpenChoreoNamespace: rowString(row, idx, "OpenChoreoNamespace"),
		})
	}
	return out, nil
}

func mapWorkflowRows(resp azlogs.QueryWorkspaceResponse) ([]WorkflowLogEntry, error) {
	if len(resp.Tables) == 0 {
		return nil, nil
	}
	t := resp.Tables[0]
	idx, err := buildColumnIndex(t.Columns)
	if err != nil {
		return nil, err
	}

	out := make([]WorkflowLogEntry, 0, len(t.Rows))
	for _, row := range t.Rows {
		out = append(out, WorkflowLogEntry{
			Timestamp:  rowTime(row, idx, "TimeGenerated"),
			LogMessage: rowString(row, idx, "LogMessage"),
		})
	}
	return out, nil
}

func buildColumnIndex(cols []azlogs.Column) (map[string]int, error) {
	if len(cols) == 0 {
		return nil, errors.New("loganalytics: response had no columns")
	}
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		if c.Name == nil {
			return nil, fmt.Errorf("loganalytics: column %d has no name", i)
		}
		idx[*c.Name] = i
	}
	return idx, nil
}

// rowString returns the string value at the named column, or "" if the
// row is too short or the cell is nil/non-string.
func rowString(row azlogs.Row, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i >= len(row) {
		return ""
	}
	switch v := row[i].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// resolveLogLevel mirrors the sibling adapters' two-step approach:
//  1. If the message is structured JSON, check common level field names.
//  2. Scan the message text for level keywords.
//  3. Default to INFO.
//
// AMA sets LogLevel="error" for all stderr output regardless of actual
// severity, so we intentionally ignore the AMA-provided level and derive
// it from the message content instead.
func resolveLogLevel(_, msg string) string {
	// Step 1: structured JSON envelope (e.g. {"level":"info","msg":"..."})
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

	// Step 2: keyword scan of the message text — matches sibling adapter behaviour.
	return extractLogLevel(msg)
}

// extractLogLevel scans the log message text for a level keyword and returns
// a normalised level string. Defaults to INFO when no keyword is found.
// Matches the logic used by the OpenSearch and AWS CloudWatch adapters.
func extractLogLevel(msg string) string {
	upper := strings.ToUpper(msg)
	for _, level := range []string{"ERROR", "FATAL", "SEVERE", "WARN", "WARNING", "INFO", "DEBUG"} {
		if strings.Contains(upper, level) {
			if level == "WARNING" {
				return "WARN"
			}
			return level
		}
	}
	return "INFO"
}

// normalizeLevel uppercases the level and maps known aliases.
func normalizeLevel(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "WARNING":
		return "WARN"
	case "INFORMATION", "INFORMATIONAL":
		return "INFO"
	case "CRITICAL", "FATAL", "SEVERE":
		return "ERROR"
	case "TRACE":
		return "DEBUG"
	default:
		return strings.ToUpper(strings.TrimSpace(s))
	}
}

// rowTime parses an ISO-8601 timestamp cell. azlogs returns datetime
// values as RFC3339Nano strings. Returns the zero time on any failure.
func rowTime(row azlogs.Row, idx map[string]int, name string) time.Time {
	s := rowString(row, idx, name)
	if s == "" {
		return time.Time{}
	}
	// Try the common formats; azlogs uses RFC3339Nano.
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
