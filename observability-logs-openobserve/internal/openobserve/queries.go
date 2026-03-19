// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// quoteIdentifier wraps a SQL identifier (e.g. table/stream name) in double
// quotes and escapes any embedded double-quote characters to prevent SQL injection.
func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// escapeSQLString escapes backslashes and single quotes in a value
// to prevent SQL injection when interpolating into single-quoted SQL strings.
func escapeSQLString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `''`)
	return value
}

// mapOperator maps the API operator string to the OpenObserve SQL operator.
func mapOperator(op string) (string, error) {
	switch op {
	case "gt":
		return ">", nil
	case "gte":
		return ">=", nil
	case "lt":
		return "<", nil
	case "lte":
		return "<=", nil
	case "eq":
		return "=", nil
	case "neq":
		return "!=", nil
	default:
		return "", fmt.Errorf("unsupported operator %q: must be one of gt, gte, lt, lte, eq, neq", op)
	}
}

// ReverseMapOperator maps the OpenObserve SQL operator back to the API operator string.
func ReverseMapOperator(op string) string {
	switch op {
	case "=":
		return "eq"
	case ">":
		return "gt"
	case ">=":
		return "gte"
	case "<":
		return "lt"
	case "<=":
		return "lte"
	case "!=":
		return "neq"
	default:
		return op
	}
}

// strMatchPattern matches str_match(log, 'pattern') in SQL queries.
// The pattern allows embedded doubled single quotes (SQL escape: '' for ').
var strMatchPattern = regexp.MustCompile(`str_match\s*\(\s*log\s*,\s*'((?:[^']|'')*)'\s*\)`)

// ExtractSearchPattern extracts the search pattern from a str_match SQL expression.
// It unescapes SQL-escaped doubled single quotes ('') and doubled backslashes (\\)
// to reverse the escaping performed by escapeSQLString.
func ExtractSearchPattern(sql string) string {
	matches := strMatchPattern.FindStringSubmatch(sql)
	if len(matches) >= 2 {
		// Unescape SQL-escaped doubled quotes and backslashes.
		// Order matters: unescape quotes first, then backslashes, to properly
		// reverse the escaping done by escapeSQLString.
		pattern := matches[1]
		pattern = strings.ReplaceAll(pattern, "''", "'")
		pattern = strings.ReplaceAll(pattern, "\\\\", "\\")
		return pattern
	}
	return ""
}

// ToDurationString converts a period value and frequency type to a duration string.
func ToDurationString(value int, frequencyType string) string {
	switch frequencyType {
	case "minutes":
		return fmt.Sprintf("%dm", value)
	case "hours":
		return fmt.Sprintf("%dh", value)
	default:
		return fmt.Sprintf("%dm", value)
	}
}

// parseDurationMinutes parses a duration string like "5m" or "2h" and returns the value in minutes.
func parseDurationMinutes(duration string) (int, error) {
	if len(duration) < 2 {
		return 0, fmt.Errorf("invalid duration string: %q", duration)
	}
	unit := duration[len(duration)-1]
	valueStr := duration[:len(duration)-1]
	var value int
	if _, err := fmt.Sscanf(valueStr, "%d", &value); err != nil {
		return 0, fmt.Errorf("invalid duration value in %q: %w", duration, err)
	}
	switch unit {
	case 'm':
		return value, nil
	case 'h':
		return value * 60, nil
	default:
		return 0, fmt.Errorf("unsupported duration unit %q in %q", string(unit), duration)
	}
}

// generateAlertConfig generates an OpenObserve alert configuration as JSON
func generateAlertConfig(params LogAlertParams, streamName string, logger *slog.Logger) ([]byte, error) {
	query := fmt.Sprintf(
		"SELECT _timestamp FROM %s WHERE str_match(log, '%s') AND kubernetes_labels_openchoreo_dev_environment_uid = '%s' AND kubernetes_labels_openchoreo_dev_component_uid = '%s'",
		quoteIdentifier(streamName),
		escapeSQLString(params.SearchPattern),
		escapeSQLString(params.EnvironmentUID),
		escapeSQLString(params.ComponentUID),
	)

	sqlOperator, err := mapOperator(params.Operator)
	if err != nil {
		return nil, fmt.Errorf("invalid alert operator: %w", err)
	}

	alertName := ""
	if params.Name != nil {
		alertName = *params.Name
	}

	period, err := parseDurationMinutes(params.Window)
	if err != nil {
		return nil, fmt.Errorf("invalid alert window: %w", err)
	}

	frequency, err := parseDurationMinutes(params.Interval)
	if err != nil {
		return nil, fmt.Errorf("invalid alert interval: %w", err)
	}

	alertConfig := map[string]interface{}{
		"name":         alertName,
		"stream_name":  streamName,
		"stream_type":  "logs",
		"enabled":      *params.Enabled,
		"is_real_time": false,
		"query_condition": map[string]interface{}{
			"type":       "sql",
			"sql":        query,
			"conditions": nil,
		},
		"trigger_condition": map[string]interface{}{
			"period":    period,
			"frequency": frequency,
			"threshold": params.ThresholdValue,
			"operator":  sqlOperator,
			"silence":   0,
		},
		"destinations": []string{"openchoreo"},
		"context_attributes": map[string]interface{}{
			"namespace":      params.Namespace,
			"projectUid":     params.ProjectUID,
			"environmentUid": params.EnvironmentUID,
			"componentUid":   params.ComponentUID,
		},
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(alertConfig, "", "    "); err == nil {
			fmt.Printf("Generated alert config for %s:\n", alertName)
			fmt.Println(string(prettyJSON))
		}
	}

	return json.Marshal(alertConfig)
}

// generateWorkflowLogsQuery generates the OpenObserve query for workflow logs
func generateWorkflowLogsQuery(params WorkflowLogsParams, stream string, logger *slog.Logger) ([]byte, error) {
	var conditions []string

	// Add namespace filter
	if params.Namespace != "" {
		conditions = append(conditions, "kubernetes_namespace_name = 'workflows-"+escapeSQLString(params.Namespace)+"'")
	}

	// Add workflow run name filter
	if params.WorkflowRunName != "" {
		conditions = append(conditions, "kubernetes_labels_workflows_argoproj_io_workflow = '"+escapeSQLString(params.WorkflowRunName)+"'")
	}

	// Add search phrase filter
	if params.SearchPhrase != "" {
		conditions = append(conditions, "log LIKE '%"+escapeSQLString(params.SearchPhrase)+"%'")
	}

	// Add log levels filter
	if len(params.LogLevels) > 0 {
		levelConditions := make([]string, len(params.LogLevels))
		for i, level := range params.LogLevels {
			levelConditions[i] = "logLevel = '" + escapeSQLString(level) + "'"
		}
		conditions = append(conditions, "("+strings.Join(levelConditions, " OR ")+")")
	}

	// Build SQL
	sql := "SELECT * FROM " + quoteIdentifier(stream)
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Add sort order
	if params.SortOrder == "ASC" || params.SortOrder == "asc" {
		sql += " ORDER BY _timestamp ASC"
	} else {
		sql += " ORDER BY _timestamp DESC"
	}

	// Set default limit if not specified
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"sql":        sql,
			"start_time": params.StartTime.UnixMicro(),
			"end_time":   params.EndTime.UnixMicro(),
			"from":       0,
			"size":       limit,
		},
		"timeout": 0,
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(query, "", "    "); err == nil {
			fmt.Printf("Generated query to fetch %s workflow logs:\n", stream)
			fmt.Println(string(prettyJSON))
		}
	}

	return json.Marshal(query)
}

// generateComponentLogsQuery generates the OpenObserve query for application logs
func generateComponentLogsQuery(params ComponentLogsParams, stream string, logger *slog.Logger) ([]byte, error) {
	if params.Namespace == "" {
		return nil, fmt.Errorf("namespace is required for component log queries")
	}

	var conditions []string

	// Add namespace filter
	conditions = append(conditions, "kubernetes_labels_openchoreo_dev_namespace = '"+escapeSQLString(params.Namespace)+"'")

	// Add project filter
	if params.ProjectID != "" {
		conditions = append(conditions, "kubernetes_labels_openchoreo_dev_project_uid = '"+escapeSQLString(params.ProjectID)+"'")
	}

	// Add environment filter
	if params.EnvironmentID != "" {
		conditions = append(conditions, "kubernetes_labels_openchoreo_dev_environment_uid = '"+escapeSQLString(params.EnvironmentID)+"'")
	}

	// Add optional component IDs filter
	if len(params.ComponentIDs) > 0 {
		componentConditions := make([]string, len(params.ComponentIDs))
		for i, id := range params.ComponentIDs {
			componentConditions[i] = "kubernetes_labels_openchoreo_dev_component_uid = '" + escapeSQLString(id) + "'"
		}
		conditions = append(conditions, "("+strings.Join(componentConditions, " OR ")+")")
	}

	// Add search phrase filter
	if params.SearchPhrase != "" {
		conditions = append(conditions, "log LIKE '%"+escapeSQLString(params.SearchPhrase)+"%'")
	}

	// Add log levels filter
	if len(params.LogLevels) > 0 {
		levelConditions := make([]string, len(params.LogLevels))
		for i, level := range params.LogLevels {
			levelConditions[i] = "logLevel = '" + escapeSQLString(level) + "'"
		}
		conditions = append(conditions, "("+strings.Join(levelConditions, " OR ")+")")
	}

	// Build SQL
	sql := "SELECT * FROM " + quoteIdentifier(stream)
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Add sort order (whitelist to prevent injection since this is not inside quotes)
	if params.SortOrder == "ASC" || params.SortOrder == "asc" {
		sql += " ORDER BY _timestamp ASC"
	} else {
		sql += " ORDER BY _timestamp DESC"
	}

	// Set default limit if not specified
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"sql":        sql,
			"start_time": params.StartTime.UnixMicro(),
			"end_time":   params.EndTime.UnixMicro(),
			"from":       0,
			"size":       limit,
		},
		"timeout": 0,
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(query, "", "    "); err == nil {
			fmt.Printf("Generated query to fetch %s application logs:\n", stream)
			fmt.Println(string(prettyJSON))
		}
	}

	return json.Marshal(query)
}
