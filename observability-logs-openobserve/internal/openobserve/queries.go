// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// escapeSQLString escapes backslashes and single quotes in a value
// to prevent SQL injection when interpolating into single-quoted SQL strings.
func escapeSQLString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `''`)
	return value
}

// generateAlertConfig generates an OpenObserve alert configuration as JSON
func generateAlertConfig(params LogAlertParams, streamName string, logger *slog.Logger) ([]byte, error) {

	query := fmt.Sprintf(
		"SELECT count(*) as %s FROM \"%s\" WHERE str_match(log, '%s')",
		"match_count",
		streamName,
		escapeSQLString(params.SearchPattern),
	)

	alertConfig := map[string]interface{}{
		"name":        params.Name,
		"stream_name": streamName,
		"query":       query,
		"condition": map[string]interface{}{
			"column":   "match_count",
			"operator": ">",
			"value":    params.ThresholdValue,
		},
		"duration":     params.Duration,
		"frequency":    params.Frequency,
		"is_realtime":  "no",
		"destinations": []string{"openchoreo_alerts"},
		"alert_type":   "scheduled",
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(alertConfig, "", "    "); err == nil {
			fmt.Printf("Generated alert config for %s:\n", params.Name)
			fmt.Println(string(prettyJSON))
		}
	}

	return json.Marshal(alertConfig)
}

// generateComponentLogsQuery generates the OpenObserve query for application logs
func generateComponentLogsQuery(params ComponentLogsParams, stream string, logger *slog.Logger) ([]byte, error) {

	conditions := []string{
		"kubernetes_labels_openchoreo_dev_project_uid = '" + escapeSQLString(params.ProjectID) + "'",
		"kubernetes_labels_openchoreo_dev_environment_uid = '" + escapeSQLString(params.EnvironmentID) + "'",
	}

	// Add optional component IDs filter. i.e. If this is empty, it returns all components logs in the specified
	// project and environment
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
	sql := "SELECT * FROM " + stream + " WHERE " + strings.Join(conditions, " AND ")

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
