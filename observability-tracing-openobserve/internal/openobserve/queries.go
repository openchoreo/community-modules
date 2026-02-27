// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
)

// escapeSQLString escapes backslashes and single quotes in a value
// to prevent SQL injection when interpolating into single-quoted SQL strings.
func escapeSQLString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `''`)
	return value
}

// generateTracesListQuery generates the OpenObserve query to list individual spans
// so that traces can be grouped in Go code to identify root spans.
func generateTracesListQuery(params TracesQueryParams, stream string, logger *slog.Logger) ([]byte, error) {
	sql := fmt.Sprintf(
		"SELECT trace_id, span_id, operation_name, span_kind, "+
			"start_time, end_time, reference_parent_span_id "+
			"FROM %s",
		stream,
	)

	conditions := buildFilterConditions(params)
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}

	sql += " ORDER BY start_time"

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"sql":        sql,
			"start_time": params.StartTime.UnixMicro(),
			"end_time":   params.EndTime.UnixMicro(),
			"from":       0,
			"size":       -1,
		},
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(query, "", "    "); err == nil {
			fmt.Printf("Generated query to list traces:\n")
			fmt.Println(string(prettyJSON))
		}
	}

	return json.Marshal(query)
}

// generateSpansListQuery generates the OpenObserve query to list spans for a given trace.
func generateSpansListQuery(params TracesQueryParams, stream string, logger *slog.Logger) ([]byte, error) {
	conditions := []string{
		"trace_id = '" + escapeSQLString(params.TraceID) + "'",
	}

	sql := fmt.Sprintf(
		"SELECT span_id, operation_name, span_kind, start_time, end_time, "+
			"end_time - start_time as duration, reference_parent_span_id "+
			"FROM %s WHERE %s",
		stream, strings.Join(conditions, " AND "),
	)

	// Add sort order
	if params.Sort == "asc" || params.Sort == "ASC" {
		sql += " ORDER BY start_time ASC"
	} else {
		sql += " ORDER BY start_time DESC"
	}

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
			fmt.Printf("Generated query to list spans for trace %s:\n", params.TraceID)
			fmt.Println(string(prettyJSON))
		}
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(query, "", "    "); err == nil {
			fmt.Printf("Generated query to fetch %s application logs:\n", stream)
			fmt.Println(string(prettyJSON))
		}
	}

	return json.Marshal(query)
}

// generateSpanDetailQuery generates the OpenObserve query to fetch a single span by traceId and spanId.
func generateSpanDetailQuery(params TracesQueryParams, stream string, logger *slog.Logger) ([]byte, error) {
	conditions := []string{
		"trace_id = '" + escapeSQLString(params.TraceID) + "'",
		"span_id = '" + escapeSQLString(params.SpanID) + "'",
	}

	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE %s",
		stream, strings.Join(conditions, " AND "),
	)

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"sql":        sql,
			"start_time": 1,
			"end_time":   int64(math.MaxInt64 / 2),
			"from":       0,
			"size":       1,
		},
		"timeout": 0,
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(query, "", "    "); err == nil {
			fmt.Printf("Generated query to fetch span detail (trace=%s, span=%s):\n", params.TraceID, params.SpanID)
			fmt.Println(string(prettyJSON))
		}
	}

	return json.Marshal(query)
}

// buildFilterConditions builds SQL WHERE conditions from the scope filter parameters.
func buildFilterConditions(params TracesQueryParams) []string {
	var conditions []string

	if params.Scope.Namespace != "" {
		conditions = append(conditions, "service_openchoreo_dev_namespace = '"+escapeSQLString(params.Scope.Namespace)+"'")
	}
	if params.Scope.ProjectID != "" {
		conditions = append(conditions, "service_openchoreo_dev_project_uid = '"+escapeSQLString(params.Scope.ProjectID)+"'")
	}
	if params.Scope.EnvironmentID != "" {
		conditions = append(conditions, "service_openchoreo_dev_environment_uid = '"+escapeSQLString(params.Scope.EnvironmentID)+"'")
	}
	if params.Scope.ComponentID != "" {
		conditions = append(conditions, "service_openchoreo_dev_component_uid = '"+escapeSQLString(params.Scope.ComponentID)+"'")
	}

	return conditions
}
