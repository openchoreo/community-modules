// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// auditFilterColumn maps a listable filter onto the column holding its value.
// An unlistable filter returns "", which IsListableAuditFilter reports on.
func auditFilterColumn(f AuditFilter) string {
	switch f {
	case AuditFilterAction:
		return ColAction
	case AuditFilterActorEntitlements:
		return ColActorEntitlements
	case AuditFilterActorID:
		return ColActorID
	case AuditFilterActorIssuer:
		return ColActorIssuer
	case AuditFilterActorSessionID:
		return ColActorSessionID
	case AuditFilterActorType:
		return ColActorType
	case AuditFilterCategory:
		return ColCategory
	case AuditFilterOperationID:
		return ColOperationID
	case AuditFilterProducer:
		return ColProducer
	case AuditFilterResourceComponent:
		return ColResourceComponent
	case AuditFilterResourceEnvironment:
		return ColResourceEnvironment
	case AuditFilterResourceName:
		return ColResourceName
	case AuditFilterResourceNamespace:
		return ColResourceNamespace
	case AuditFilterResourceProject:
		return ColResourceProject
	case AuditFilterResourceResource:
		return ColResourceResource
	case AuditFilterResourceType:
		return ColResourceType
	case AuditFilterResult:
		return ColResult
	case AuditFilterSourceIP:
		return ColSourceIP
	case AuditFilterSurface:
		return ColSurface
	case AuditFilterUserAgent:
		return ColUserAgent
	default:
		return ""
	}
}

// BuildAuditLogsKQL renders an audit query as statements over one shared
// filtered set, answered in a single round trip:
//
//  1. The page, ordered and truncated.
//  2. Total, counted over the whole window rather than the page. The contract
//     requires an exact count: a caller reads total == len(records) as "window
//     fully read", so reporting the page size would make it stop early.
//  3. The timeline, when asked for.
//
// Base is materialised because three statements read it. A let binding is
// re-evaluated per reference, which would scan the window once per statement.
//
// The window is [StartTime, EndTime) and is written into the query: the SDK
// Timespan only bounds the scan, and its end inclusivity is not specified.
func BuildAuditLogsKQL(p AuditParams, table string) string {
	limit := effectiveAuditLimit(p.Limit)
	order := sortOrderOrDefault(p.SortOrder)

	// EventId only makes the order deterministic. It does not make paging
	// lossless: a caller resumes from the last record's time, so records sharing
	// that instant past the limit are missed either way.
	//
	// sort-then-take rather than top, which accepts only one sort expression.
	records := fmt.Sprintf("Base\n| sort by %s %s, %s %s\n| take %d\n| project\n    %s",
		ColTimeGenerated, order, ColEventID, order, limit,
		strings.Join(auditProjection(), ",\n    "))

	stmts := []string{
		records,
		fmt.Sprintf("Base\n| summarize %s = count()", ColTotal),
	}

	if p.IncludeTimeline {
		_, kql, _ := ResolveTimelineInterval(p.StartTime, p.EndTime, p.TimelineInterval)
		// bin_at, not bin: bin's origin is undocumented, and an origin other
		// than StartTime lands every count outside the buckets mapping walks.
		// Empty buckets are filled there too, KQL having no zero fill.
		stmts = append(stmts, fmt.Sprintf(
			"Base\n| summarize %s = count() by %s = bin_at(%s, %s, %s), %s",
			ColBucketTotal, ColBucketStart, ColTimeGenerated, kql,
			kqlDatetime(p.StartTime), ColResult))
	}

	// Separator, not a terminator: no trailing semicolon.
	return auditBase(p, table, "", auditBaseColumns()) + strings.Join(stmts, ";\n")
}

// auditProjection is the record page's projection list.
//
// A slice rather than a format string, because a misaligned placeholder in a
// list this long yields valid KQL that reads the wrong column into a field.
//
// The dynamic columns go through tostring() and are decoded in Go: the SDK
// hands them back as untyped any whose shape varies by row.
func auditProjection() []string {
	cols := []string{
		ColEventTime, ColSchemaVersion, ColEventID, ColAction, ColCategory,
		ColResult, ColRequestID, ColSourceIP, ColUserAgent, ColProducer,
		ColSurface, ColOperationID, ColActorType, ColActorID, ColActorIssuer,
		ColActorSessionID, ColHTTPMethod, ColHTTPPath, ColResourceType,
		ColResourceNamespace, ColResourceEnvironment, ColResourceProject,
		ColResourceComponent, ColResourceResource, ColResourceUID, ColResourceName,
		ColCollectorNamespace, ColCollectorPod, ColCollectorContainer,
	}
	return append(cols,
		ColActorEntitlementsJSON+" = tostring("+ColActorEntitlements+")",
		ColMetadataJSON+" = tostring("+ColMetadata+")",
		ColResourceMetadataJSON+" = tostring("+ColResourceMetadata+")",
	)
}

// auditProjectionColumns reduces auditProjection's `alias = expr` forms to the
// alias, which is what mapping reads the table by.
func auditProjectionColumns() []string {
	proj := auditProjection()
	out := make([]string, len(proj))
	for i, p := range proj {
		if name, _, ok := strings.Cut(p, " = "); ok {
			out[i] = name
			continue
		}
		out[i] = p
	}
	return out
}

// BuildAuditFilterValuesKQL lists the distinct values one filter takes under a
// query, with a count each, plus how many distinct values match.
//
// The filter's own selections are cleared: one that counted its own selection
// would offer only what is already picked.
func BuildAuditFilterValuesKQL(p AuditFilterValuesParams, table string) string {
	col := auditFilterColumn(p.Filter)

	var sb strings.Builder
	// Only the counted column is cached; nothing else is read back.
	sb.WriteString(auditBase(p.Query, table, p.Filter, []string{col}))

	// No filter value would select an empty string, so absent fields are not
	// offered.
	values := "Base\n| extend _value = " + col + "\n| where isnotempty(_value)"

	if p.Filter == AuditFilterActorEntitlements {
		// Expanded, unlike the record filter: every value is offered on its own
		// rather than one entry per claim.
		values = "Base\n| where isnotnull(" + ColActorEntitlements + ")" +
			"\n| mv-expand _entKey = bag_keys(" + ColActorEntitlements + ") to typeof(string)" +
			"\n| mv-expand _value = " + ColActorEntitlements + "[_entKey] to typeof(string)" +
			"\n| where isnotempty(_value)"
	}

	if p.ValueSearch != "" {
		// A quoted literal, never a pattern the caller can inject.
		values += "\n| where _value contains " + kqlString(p.ValueSearch)
	}

	// Clamped again here because the builder is exported, and take 0 would read
	// as "this filter has no values".
	maxValues := p.MaxValues
	if maxValues < 1 {
		maxValues = DefaultAuditMaxValues
	}
	if maxValues > MaxAuditMaxValues {
		maxValues = MaxAuditMaxValues
	}

	sb.WriteString(values)
	fmt.Fprintf(&sb, `
| summarize _count = count() by _value
| sort by _count desc, _value asc
| take %d
| project Value = _value, Count = _count;
`, maxValues)

	sb.WriteString(values)
	sb.WriteString("\n| summarize TotalValues = dcount(_value)")

	return sb.String()
}

// auditBase renders the shared `let Base = materialize(...);`. Sharing it is
// what guarantees the values a filter offers match returned records.
//
// skip names a filter whose own selections are dropped; "" keeps them all.
//
// keep is projected inside materialize, which caches whole rows against a
// per-node budget shared with concurrent queries. RawRecord is the widest
// column and no statement reads it back.
func auditBase(p AuditParams, table string, skip AuditFilter, keep []string) string {
	var sb strings.Builder
	sb.WriteString("let Base = materialize(\n")
	sb.WriteString(table)

	// Cheapest first: the time range prunes extents before anything else runs.
	sb.WriteString("\n| where ")
	sb.WriteString(ColTimeGenerated)
	sb.WriteString(" >= ")
	sb.WriteString(kqlDatetime(p.StartTime))
	sb.WriteString(" and ")
	sb.WriteString(ColTimeGenerated)
	sb.WriteString(" < ")
	sb.WriteString(kqlDatetime(p.EndTime))

	// Dropped here, not at mapping time, so unparseable rows stay out of Total
	// and the timeline as well as the page.
	for _, col := range auditIdentityColumns {
		sb.WriteString("\n| where isnotempty(")
		sb.WriteString(col)
		sb.WriteString(")")
	}
	sb.WriteString("\n| where isnotnull(")
	sb.WriteString(ColEventTime)
	sb.WriteString(")")

	// An absent or empty multi-value filter is not a filter. Getting this wrong
	// turns an unfiltered audit query into one matching nothing.
	for _, f := range []struct {
		skip   AuditFilter
		column string
		values []string
	}{
		{"", ColEventID, p.EventIDs},
		{"", ColRequestID, p.RequestIDs},
		{AuditFilterAction, ColAction, p.Actions},
		{AuditFilterCategory, ColCategory, p.Categories},
		{AuditFilterResult, ColResult, p.Results},
		{AuditFilterSourceIP, ColSourceIP, p.SourceIPs},
		{AuditFilterUserAgent, ColUserAgent, p.UserAgents},
		{AuditFilterProducer, ColProducer, p.Producers},
		{AuditFilterSurface, ColSurface, p.Surfaces},
		{AuditFilterOperationID, ColOperationID, p.OperationIDs},
		{AuditFilterActorType, ColActorType, p.ActorTypes},
		{AuditFilterActorID, ColActorID, p.ActorIDs},
		{AuditFilterActorIssuer, ColActorIssuer, p.ActorIssuers},
		{AuditFilterActorSessionID, ColActorSessionID, p.ActorSessionIDs},
		{AuditFilterResourceType, ColResourceType, p.ResourceTypes},
		{AuditFilterResourceNamespace, ColResourceNamespace, p.ResourceNamespaces},
		{AuditFilterResourceEnvironment, ColResourceEnvironment, p.ResourceEnvironments},
		{AuditFilterResourceProject, ColResourceProject, p.ResourceProjects},
		{AuditFilterResourceComponent, ColResourceComponent, p.ResourceComponents},
		{AuditFilterResourceResource, ColResourceResource, p.ResourceResources},
		{AuditFilterResourceName, ColResourceName, p.ResourceNames},
	} {
		// event_id and request_id carry the zero AuditFilter, having no listable
		// filter, so they must never be cleared by it.
		if f.skip != "" && f.skip == skip {
			continue
		}
		writeInFilter(&sb, f.column, f.values)
	}

	if skip != AuditFilterActorEntitlements {
		writeEntitlementsFilter(&sb, p.ActorEntitlements)
	}

	if p.SearchPhrase != "" {
		// The raw line, so field names and punctuation match too.
		sb.WriteString("\n| where ")
		sb.WriteString(ColRawRecord)
		sb.WriteString(" contains ")
		sb.WriteString(kqlString(p.SearchPhrase))
	}

	if len(keep) > 0 {
		sb.WriteString("\n| project ")
		sb.WriteString(strings.Join(keep, ", "))
	}

	sb.WriteString("\n);\n")
	return sb.String()
}

// auditBaseColumns is what the page, Total and the timeline read out of Base.
func auditBaseColumns() []string {
	cols := []string{ColTimeGenerated}
	cols = append(cols, auditRecordSourceColumns()...)
	return cols
}

// auditRecordSourceColumns is the table columns auditProjection reads, as
// opposed to the output names it produces.
func auditRecordSourceColumns() []string {
	return []string{
		ColEventTime, ColSchemaVersion, ColEventID, ColAction, ColCategory,
		ColResult, ColRequestID, ColSourceIP, ColUserAgent, ColProducer,
		ColSurface, ColOperationID, ColActorType, ColActorID, ColActorIssuer,
		ColActorSessionID, ColHTTPMethod, ColHTTPPath, ColResourceType,
		ColResourceNamespace, ColResourceEnvironment, ColResourceProject,
		ColResourceComponent, ColResourceResource, ColResourceUID, ColResourceName,
		ColCollectorNamespace, ColCollectorPod, ColCollectorContainer,
		ColActorEntitlements, ColMetadata, ColResourceMetadata,
	}
}

// writeEntitlementsFilter matches a record carrying any of the given values
// under any entitlement claim.
//
// The subquery ends in summarize so it returns one row per record. Expanding
// in place would repeat a record once per claim and inflate Total.
//
// set_has_element matches a whole element, so "admin" cannot match a record
// entitled only to "admin-readonly".
func writeEntitlementsFilter(sb *strings.Builder, values []string) {
	if len(values) == 0 {
		return
	}
	// bag_keys returns null, not an empty array, for a null bag.
	sb.WriteString("\n| where isnotnull(" + ColActorEntitlements + ")")

	sb.WriteString("\n| mv-apply _entKey = bag_keys(" + ColActorEntitlements +
		") to typeof(string) on (\n    summarize _entMatch = max(iff(")
	for i, v := range values {
		if i > 0 {
			sb.WriteString(" or ")
		}
		sb.WriteString("set_has_element(" + ColActorEntitlements + "[_entKey], ")
		sb.WriteString(kqlString(v))
		sb.WriteString(")")
	}
	sb.WriteString(", 1, 0))\n)")
	sb.WriteString("\n| where _entMatch == 1")
}

// timelineUnits is the coarsening ladder, finest first.
var timelineUnits = []struct {
	suffix string
	d      time.Duration
}{
	{"m", time.Minute},
	{"h", time.Hour},
	{"d", 24 * time.Hour},
	{"w", 7 * 24 * time.Hour},
}

// ResolveTimelineInterval returns the bucket width as the contract's
// "<count><unit>", the KQL timespan literal, and a duration, coarsening the
// requested width until the window fits maxTimelineBuckets.
//
// Coarsening rather than refusing means a caller always gets a drawable
// timeline, and the response reports the width used.
func ResolveTimelineInterval(start, end time.Time, requested string) (string, string, time.Duration) {
	d := parseTimelineInterval(requested)
	window := end.Sub(start)
	if window <= 0 {
		return formatTimelineInterval(d), kqlTimespan(d), d
	}
	if d <= 0 {
		d = time.Hour
	}

	// Up the ladder first, then whole weeks: "3w" reads better than 21d.
	for buckets(window, d) > maxTimelineBuckets {
		next := time.Duration(0)
		for _, u := range timelineUnits {
			if u.d > d {
				next = u.d
				break
			}
		}
		if next == 0 {
			d += 7 * 24 * time.Hour
			continue
		}
		d = next
	}

	return formatTimelineInterval(d), kqlTimespan(d), d
}

// buckets rounds the partial final bucket up; it holds real records.
func buckets(window, d time.Duration) int64 {
	if d <= 0 {
		return maxTimelineBuckets + 1
	}
	return int64((window + d - 1) / d)
}

// parseTimelineInterval reads "<count><unit>", unit m|h|d|w. Unparseable falls
// back to an hour rather than failing the query; the width is a hint.
func parseTimelineInterval(s string) time.Duration {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return time.Hour
	}
	for _, u := range timelineUnits {
		if !strings.HasSuffix(s, u.suffix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(s, u.suffix))
		if err != nil || n < 1 {
			return time.Hour
		}
		return time.Duration(n) * u.d
	}
	return time.Hour
}

// formatTimelineInterval renders d in the contract's "<count><unit>" form,
// using the coarsest unit that divides it exactly.
func formatTimelineInterval(d time.Duration) string {
	if d <= 0 {
		return "1h"
	}
	for i := len(timelineUnits) - 1; i >= 0; i-- {
		u := timelineUnits[i]
		if d%u.d == 0 {
			return strconv.FormatInt(int64(d/u.d), 10) + u.suffix
		}
	}
	return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
}

// kqlTimespan renders d as a KQL timespan literal. KQL has no week unit, so a
// week is written as 7d.
func kqlTimespan(d time.Duration) string {
	if d <= 0 {
		return "1h"
	}
	switch {
	case d%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	case d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	default:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	}
}

// AuditTableProbeKQL fails when the audit table does not exist yet, and
// otherwise returns nothing.
func AuditTableProbeKQL(table string) string {
	return table + " | take 0"
}
