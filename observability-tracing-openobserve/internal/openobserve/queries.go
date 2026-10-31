// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
)

// MaxQueryLimit is the upper bound for query result sizes to prevent
// excessively large responses from OpenObserve.
const MaxQueryLimit = 1000

// validateSQLIdentifier checks that the identifier contains only alphanumeric
// characters, underscores, hyphens, or dots. It returns an error if the
// identifier is empty or contains disallowed characters.
var validIdentifierRe = regexp.MustCompile(`^[a-zA-Z0-9_.\-]+$`)

func validateSQLIdentifier(identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("SQL identifier must not be empty")
	}
	if !validIdentifierRe.MatchString(identifier) {
		return "", fmt.Errorf("SQL identifier %q contains invalid characters", identifier)
	}
	return identifier, nil
}

// escapeSQLString escapes backslashes and single quotes in a value
// to prevent SQL injection when interpolating into single-quoted SQL strings.
func escapeSQLString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `'`, `''`)
	return value
}

// Resource attribute columns that place a span on a component's release lines. The collector copies
// the pod labels onto spans and OpenObserve flattens them under the service_ prefix.
const (
	componentUIDColumn   = "service_openchoreo_dev_component_uid"
	releaseLineUIDColumn = "service_openchoreo_dev_release_line_uid"
	// releaseLineComponentUIDColumn is the component UID on a release line's spans, which don't carry componentUIDColumn.
	releaseLineComponentUIDColumn = "service_openchoreo_dev_release_line_component_uid"
)

// lineColumns reports which release line attribute columns the traces stream has stored.
// OpenObserve rejects a query naming a column the stream lacks.
type lineColumns struct {
	line, lineComponent bool
}

// releaseLineConditions matches a component's spans by release line. The component UID alone keeps
// its main line's spans, those with the component UID and no release line UID. A release line's
// pods carry the component UID under another attribute than the main line's, so AllReleaseLines
// matches either and covers every line. One release line matches on its UID alone. A column the
// stream has not stored is left out of the main and every line match, since no span carries that
// attribute. Returns nil without a component or a line.
func releaseLineConditions(scope Scope) []string {
	if scope.ReleaseLineID != "" {
		return []string{releaseLineUIDColumn + " = '" + escapeSQLString(scope.ReleaseLineID) + "'"}
	}
	if scope.ComponentID == "" {
		return nil
	}

	match := componentUIDColumn + " = '" + escapeSQLString(scope.ComponentID) + "'"
	if scope.AllReleaseLines {
		if scope.stored.lineComponent {
			match = "(" + match + " OR " + releaseLineComponentUIDColumn + " = '" + escapeSQLString(scope.ComponentID) + "')"
		}
		return []string{match}
	}
	if scope.stored.line {
		return []string{match, releaseLineUIDColumn + " IS NULL"}
	}
	return []string{match}
}

// spanConditions builds the SQL WHERE conditions for the spans of one trace. Spans of other
// components are kept, and the component's own spans are narrowed to the requested line: its main
// line, one release line, or every line.
func spanConditions(params TracesQueryParams) []string {
	conditions := []string{"trace_id = '" + escapeSQLString(params.TraceID) + "'"}
	scope := params.Scope
	component := escapeSQLString(scope.ComponentID)
	switch {
	case scope.ReleaseLineID != "" && scope.ComponentID == "":
		conditions = append(conditions, releaseLineUIDColumn+" = '"+escapeSQLString(scope.ReleaseLineID)+"'")
	case scope.ReleaseLineID != "":
		otherComponent := "(" + componentUIDColumn + " IS NULL OR " + componentUIDColumn + " != '" + component + "')"
		if scope.stored.lineComponent {
			otherComponent += " AND (" + releaseLineComponentUIDColumn + " IS NULL OR " + releaseLineComponentUIDColumn + " != '" + component + "')"
		}
		conditions = append(conditions, "("+releaseLineUIDColumn+" = '"+escapeSQLString(scope.ReleaseLineID)+"' OR ("+otherComponent+"))")
	case scope.ComponentID != "" && !scope.AllReleaseLines && scope.stored.lineComponent:
		conditions = append(conditions, "("+releaseLineComponentUIDColumn+" IS NULL OR "+releaseLineComponentUIDColumn+" != '"+component+"')")
	}
	return conditions
}

// generateTracesListQuery generates the OpenObserve query to list individual spans
// so that traces can be grouped in Go code to identify root spans.
func generateTracesListQuery(params TracesQueryParams, stream string, logger *slog.Logger) ([]byte, error) {
	safeStream, err := validateSQLIdentifier(stream)
	if err != nil {
		return nil, fmt.Errorf("invalid stream identifier: %w", err)
	}

	sql := fmt.Sprintf(
		"SELECT trace_id, span_id, operation_name, span_kind, "+
			"start_time, end_time, reference_parent_span_id, span_status "+
			"FROM %s",
		safeStream,
	)

	conditions := buildFilterConditions(params)
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Add sort order
	if params.SortOrder == "asc" || params.SortOrder == "ASC" {
		sql += " ORDER BY start_time ASC"
	} else {
		sql += " ORDER BY start_time DESC"
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"sql":        sql,
			"start_time": params.StartTime.UnixMicro(),
			"end_time":   params.EndTime.UnixMicro(),
			"from":       0,
			"size":       limit,
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
	conditions := spanConditions(params)

	safeStream, err := validateSQLIdentifier(stream)
	if err != nil {
		return nil, fmt.Errorf("invalid stream identifier: %w", err)
	}

	sql := fmt.Sprintf(
		"SELECT span_id, operation_name, span_kind, start_time, end_time, "+
			"end_time - start_time as duration, reference_parent_span_id, span_status, status_message "+
			"FROM %s WHERE %s",
		safeStream, strings.Join(conditions, " AND "),
	)

	// Add sort order
	if params.SortOrder == "asc" || params.SortOrder == "ASC" {
		sql += " ORDER BY start_time ASC"
	} else {
		sql += " ORDER BY start_time DESC"
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > MaxQueryLimit {
		limit = MaxQueryLimit
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

	return json.Marshal(query)
}

// generateSpanDetailQuery generates the OpenObserve query to fetch a single span by traceId and spanId.
func generateSpanDetailQuery(params TracesQueryParams, stream string, logger *slog.Logger) ([]byte, error) {
	conditions := []string{
		"trace_id = '" + escapeSQLString(params.TraceID) + "'",
		"span_id = '" + escapeSQLString(params.SpanID) + "'",
	}

	safeStream, err := validateSQLIdentifier(stream)
	if err != nil {
		return nil, fmt.Errorf("invalid stream identifier: %w", err)
	}

	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE %s",
		safeStream, strings.Join(conditions, " AND "),
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

// generateTracesCountQuery generates a count query to get the true total number of matching traces.
func generateTracesCountQuery(params TracesQueryParams, stream string, logger *slog.Logger) ([]byte, error) {
	safeStream, err := validateSQLIdentifier(stream)
	if err != nil {
		return nil, fmt.Errorf("invalid stream identifier: %w", err)
	}

	sql := fmt.Sprintf(
		"SELECT count(distinct trace_id) as total FROM %s",
		safeStream,
	)

	conditions := buildFilterConditions(params)
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"sql":        sql,
			"start_time": params.StartTime.UnixMicro(),
			"end_time":   params.EndTime.UnixMicro(),
			"from":       0,
			"size":       0,
		},
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(query, "", "    "); err == nil {
			fmt.Printf("Generated count query for traces:\n")
			fmt.Println(string(prettyJSON))
		}
	}

	return json.Marshal(query)
}

// generateSpansCountQuery generates a count query to get the true total number of matching spans for a trace.
func generateSpansCountQuery(params TracesQueryParams, stream string, logger *slog.Logger) ([]byte, error) {
	safeStream, err := validateSQLIdentifier(stream)
	if err != nil {
		return nil, fmt.Errorf("invalid stream identifier: %w", err)
	}

	conditions := spanConditions(params)

	sql := fmt.Sprintf(
		"SELECT count(*) as total FROM %s WHERE %s",
		safeStream, strings.Join(conditions, " AND "),
	)

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"sql":        sql,
			"start_time": params.StartTime.UnixMicro(),
			"end_time":   params.EndTime.UnixMicro(),
			"from":       0,
			"size":       0,
		},
	}

	if logger.Enabled(nil, slog.LevelDebug) {
		if prettyJSON, err := json.MarshalIndent(query, "", "    "); err == nil {
			fmt.Printf("Generated count query for spans (trace=%s):\n", params.TraceID)
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
	conditions = append(conditions, releaseLineConditions(params.Scope)...)

	return conditions
}
