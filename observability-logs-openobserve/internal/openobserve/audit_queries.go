// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// AuditEntitlementsFilter matches values across every actor.entitlements claim column.
	AuditEntitlementsFilter = "actor.entitlements"

	auditEntitlementColumnPrefix = "actor_entitlements_"
	colEventID                   = "event_id"
	colResult                    = "result"

	// OpenObserve stores an array of objects as one JSON string column.
	colReturnedItems = "returned_items"

	maxTimelineBuckets = 500

	// Keeps count*week from overflowing a Duration.
	maxTimelineIntervalCount = 10000

	// One row per bucket and result value.
	maxTimelineRows = maxTimelineBuckets * 20
)

// auditFilterColumns maps contract filters to OpenObserve's flattened columns.
var auditFilterColumns = map[string]string{
	"actor.id":              "actor_id",
	"actor.type":            "actor_type",
	"actor.issuer":          "actor_issuer",
	"actor.session_id":      "actor_session_id",
	"resource.type":         "resource_type",
	"resource.namespace":    "resource_namespace",
	"resource.environment":  "resource_environment",
	"resource.project":      "resource_project",
	"resource.component":    "resource_component",
	"resource.release_line": "resource_release_line",
	"resource.resource":     "resource_resource",
	"resource.name":         "resource_name",
	"action":                "action",
	"category":              "category",
	"result":                colResult,
	"producer":              "producer",
	"surface":               "surface",
	"operation_id":          "operation_id",
	"source_ip":             "source_ip",
	"user_agent":            "user_agent",
}

// IsAuditFilter reports whether a filter's values can be listed.
func IsAuditFilter(filter string) bool {
	_, ok := auditFilterColumns[filter]
	return ok || filter == AuditEntitlementsFilter
}

// auditSQLString quotes a SQL string literal. Unlike escapeSQLString it does not double
// backslashes, which OpenObserve reads literally.
func auditSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// auditConditions builds the shared WHERE clauses. It reports false when a filter names a
// column the stream has never stored, which OpenObserve would reject and no record can match.
func auditConditions(params AuditLogsParams, columns map[string]bool) ([]string, bool) {
	var conditions []string

	for _, f := range []struct {
		column string
		values []string
	}{
		{"actor_id", params.ActorIDs},
		{"actor_type", params.ActorTypes},
		{"actor_issuer", params.ActorIssuers},
		{"actor_session_id", params.ActorSessionIDs},
		{"resource_type", params.ResourceTypes},
		{"resource_namespace", params.ResourceNamespaces},
		{"resource_environment", params.ResourceEnvironments},
		{"resource_project", params.ResourceProjects},
		{"resource_component", params.ResourceComponents},
		{"resource_release_line", params.ResourceReleaseLines},
		{"resource_resource", params.ResourceResources},
		{"resource_name", params.ResourceNames},
		{"action", params.Actions},
		{"category", params.Categories},
		{colResult, params.Results},
		{"producer", params.Producers},
		{"surface", params.Surfaces},
		{"operation_id", params.OperationIDs},
		{"request_id", params.RequestIDs},
		{colEventID, params.EventIDs},
		{"source_ip", params.SourceIPs},
		{"user_agent", params.UserAgents},
	} {
		if len(f.values) == 0 {
			continue
		}
		if !columns[f.column] {
			return nil, false
		}
		parts := make([]string, 0, len(f.values))
		for _, v := range f.values {
			parts = append(parts, f.column+" = "+auditSQLString(v))
		}
		conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
	}

	if len(params.ActorEntitlements) > 0 {
		entitlementColumns := auditEntitlementColumns(columns)
		if len(entitlementColumns) == 0 {
			return nil, false
		}
		var parts []string
		for _, column := range entitlementColumns {
			for _, v := range params.ActorEntitlements {
				parts = append(parts,
					"array_has(cast_to_arr("+quoteIdentifier(column)+"), "+auditSQLString(v)+")")
			}
		}
		conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
	}

	if len(params.ReturnedItemNames) > 0 {
		if !columns[colReturnedItems] {
			return nil, false
		}
		parts := make([]string, 0, len(params.ReturnedItemNames))
		for _, name := range params.ReturnedItemNames {
			parts = append(parts,
				"strpos("+colReturnedItems+", "+auditSQLString(returnedItemNameNeedle(name))+") > 0")
		}
		conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
	}

	if params.SearchPhrase != "" {
		if !columns[colLog] {
			return nil, false
		}
		// strpos rather than LIKE: OpenObserve rejects LIKE escape characters other than
		// backslash, so wildcards in the phrase could not be matched literally.
		conditions = append(conditions,
			"strpos("+colLog+", "+auditSQLString(params.SearchPhrase)+") > 0")
	}

	return conditions, true
}

// returnedItemNameNeedle is an item's "name" pair as stored in returned_items. The
// closing quote keeps db-creds from matching db-creds-old, and JSON-escaping the value
// stops a quote in it from reaching into the next item.
func returnedItemNameNeedle(name string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(name)
	return `"name":` + strings.TrimSuffix(b.String(), "\n")
}

// auditEntitlementColumns returns the stored claim columns, sorted for stable SQL.
func auditEntitlementColumns(columns map[string]bool) []string {
	var result []string
	for column := range columns {
		if strings.HasPrefix(column, auditEntitlementColumnPrefix) {
			result = append(result, column)
		}
	}
	sort.Strings(result)
	return result
}

func auditQueryEnvelope(sql string, start, end time.Time, size int) map[string]interface{} {
	return map[string]interface{}{
		"query": map[string]interface{}{
			"sql":        sql,
			"start_time": start.UnixMicro(),
			"end_time":   end.UnixMicro(),
			"from":       0,
			"size":       size,
		},
	}
}

func generateAuditLogsQuery(
	params AuditLogsParams, stream string, conditions []string, logger *slog.Logger,
) ([]byte, error) {
	direction := "DESC"
	if strings.EqualFold(params.SortOrder, "asc") {
		direction = "ASC"
	}

	// event_id breaks ties so paging by the last record's time is stable.
	sql := "SELECT * FROM " + quoteIdentifier(stream) + whereClause(conditions) +
		" ORDER BY " + colTimestamp + " " + direction + ", " + colEventID + " " + direction

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}

	query := auditQueryEnvelope(sql, params.StartTime, params.EndTime, limit)
	logPlatformQuery(logger, "audit logs", query)
	return json.Marshal(query)
}

func generateAuditLogsCountQuery(
	params AuditLogsParams, stream string, conditions []string, logger *slog.Logger,
) ([]byte, error) {
	sql := "SELECT count(*) as total FROM " + quoteIdentifier(stream) + whereClause(conditions)

	query := auditQueryEnvelope(sql, params.StartTime, params.EndTime, 0)
	logPlatformQuery(logger, "audit logs count", query)
	return json.Marshal(query)
}

// generateAuditTimelineQuery numbers buckets from the window start rather than using
// histogram(), which aligns buckets to the epoch.
func generateAuditTimelineQuery(
	params AuditLogsParams, stream string, conditions []string, width time.Duration, logger *slog.Logger,
) ([]byte, error) {
	sql := "SELECT CAST((" + colTimestamp + " - " + strconv.FormatInt(params.StartTime.UnixMicro(), 10) +
		") / " + strconv.FormatInt(width.Microseconds(), 10) + " AS BIGINT) as bucket, " +
		colResult + " as result, count(*) as record_count FROM " + quoteIdentifier(stream) +
		whereClause(conditions) + " GROUP BY bucket, " + colResult

	query := auditQueryEnvelope(sql, params.StartTime, params.EndTime, maxTimelineRows)
	logPlatformQuery(logger, "audit timeline", query)
	return json.Marshal(query)
}

// auditValuesSource returns a subquery yielding the filter's values, or false when no
// stored column holds them. Entitlements union every claim column.
func auditValuesSource(
	stream, filter string, conditions []string, columns map[string]bool,
) (string, bool) {
	if filter == AuditEntitlementsFilter {
		entitlementColumns := auditEntitlementColumns(columns)
		if len(entitlementColumns) == 0 {
			return "", false
		}
		branches := make([]string, 0, len(entitlementColumns))
		for _, column := range entitlementColumns {
			branchConditions := append(append([]string{}, conditions...), quoteIdentifier(column)+" IS NOT NULL")
			branches = append(branches, "SELECT unnest(cast_to_arr("+quoteIdentifier(column)+")) as value, "+
				colEventID+" FROM "+quoteIdentifier(stream)+whereClause(branchConditions))
		}
		return "(" + strings.Join(branches, " UNION ALL ") + ")", true
	}

	column, ok := auditFilterColumns[filter]
	if !ok || !columns[column] {
		return "", false
	}
	return "(SELECT " + column + " as value, " + colEventID + " FROM " + quoteIdentifier(stream) + whereClause(conditions) + ")", true
}

// auditValuesConditions is shared by both values queries so the total describes the list.
func auditValuesConditions(valueSearch string) []string {
	conditions := []string{"value IS NOT NULL", "value != ''"}
	if valueSearch != "" {
		conditions = append(conditions,
			"strpos(lower(value), "+auditSQLString(strings.ToLower(valueSearch))+") > 0")
	}
	return conditions
}

func generateAuditFilterValuesQuery(
	params AuditLogsParams, source, valueSearch string, maxValues int, logger *slog.Logger,
) ([]byte, error) {
	// count(distinct event_id) rather than count(*): the entitlements source unions one
	// row per claim column per array entry, so a record carrying a value more than once
	// must still count once. Ordered by alias: this OpenObserve ignores ORDER BY count(*)
	// over a subquery.
	sql := "SELECT value, count(distinct " + colEventID + ") as record_count FROM " + source +
		whereClause(auditValuesConditions(valueSearch)) +
		" GROUP BY value ORDER BY record_count DESC, value ASC LIMIT " + strconv.Itoa(maxValues)

	query := auditQueryEnvelope(sql, params.StartTime, params.EndTime, maxValues)
	logPlatformQuery(logger, "audit filter values", query)
	return json.Marshal(query)
}

func generateAuditFilterValuesTotalQuery(
	params AuditLogsParams, source, valueSearch string, logger *slog.Logger,
) ([]byte, error) {
	sql := "SELECT count(distinct value) as total FROM " + source +
		whereClause(auditValuesConditions(valueSearch))

	query := auditQueryEnvelope(sql, params.StartTime, params.EndTime, 0)
	logPlatformQuery(logger, "audit filter values total", query)
	return json.Marshal(query)
}

func parseAuditFilterValues(resp *OpenObserveResponse) []AuditFilterValue {
	values := make([]AuditFilterValue, 0, len(resp.Hits))
	for _, hit := range resp.Hits {
		value, ok := hit["value"].(string)
		if !ok || value == "" {
			continue
		}
		count := int64(0)
		if c, ok := hit["record_count"].(float64); ok {
			count = int64(c)
		}
		values = append(values, AuditFilterValue{Value: value, Count: count})
	}
	return values
}

// buildAuditTimeline zero-fills the buckets OpenObserve returns no rows for.
func buildAuditTimeline(interval string, start, end time.Time, rows []map[string]interface{}) (*AuditTimeline, error) {
	width, err := parseTimelineInterval(interval)
	if err != nil {
		return nil, err
	}
	window := end.Sub(start)
	if width <= 0 || window <= 0 {
		return nil, fmt.Errorf("invalid timeline window")
	}

	count := int((window + width - 1) / width)
	buckets := make([]AuditTimelineBucket, count)
	for i := range buckets {
		buckets[i] = AuditTimelineBucket{
			StartTime: start.Add(time.Duration(i) * width),
			Counts:    map[string]int64{},
		}
	}

	for _, row := range rows {
		index, ok := row["bucket"].(float64)
		if !ok || index != math.Trunc(index) || index < 0 || int(index) >= count {
			continue
		}
		n, _ := row["record_count"].(float64)
		b := &buckets[int(index)]
		b.Total += int64(n)
		if result, ok := row["result"].(string); ok && result != "" {
			b.Counts[result] += int64(n)
		}
	}

	return &AuditTimeline{Interval: interval, Buckets: buckets}, nil
}

// ResolveTimelineInterval picks the bucket width, coarsening rather than rejecting one
// that exceeds the bucket limit.
func ResolveTimelineInterval(requested string, start, end time.Time) (string, error) {
	window := end.Sub(start)
	if window <= 0 {
		return "", fmt.Errorf("end time must be after start time")
	}

	width, err := parseTimelineInterval(requested)
	if err != nil {
		return "", err
	}
	if width <= 0 {
		width = (window / 60).Truncate(time.Minute)
		if width < time.Minute {
			width = time.Minute
		}
	}

	for (window+width-1)/width > maxTimelineBuckets {
		next := coarsenInterval(width)
		if next <= width {
			width += 7 * 24 * time.Hour
			continue
		}
		width = next
	}

	return formatTimelineInterval(width), nil
}

// parseTimelineInterval reads <count><unit> notation; empty means the adapter chooses.
func parseTimelineInterval(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid timeline interval: %s", s)
	}

	count, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || count <= 0 || count > maxTimelineIntervalCount {
		return 0, fmt.Errorf("invalid timeline interval: %s", s)
	}

	var unit time.Duration
	switch s[len(s)-1] {
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid timeline interval unit: %s", s)
	}

	return time.Duration(count) * unit, nil
}

func coarsenInterval(d time.Duration) time.Duration {
	switch {
	case d < time.Hour:
		return time.Hour
	case d < 24*time.Hour:
		return 24 * time.Hour
	case d < 7*24*time.Hour:
		return 7 * 24 * time.Hour
	}
	return d
}

func formatTimelineInterval(d time.Duration) string {
	switch {
	case d%(7*24*time.Hour) == 0:
		return fmt.Sprintf("%dw", d/(7*24*time.Hour))
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	default:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
}
