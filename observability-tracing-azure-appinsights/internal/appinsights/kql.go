// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

import (
	"fmt"
	"regexp"
	"strings"
)

// Distributed-trace spans live in AppRequests (SERVER spans) and
// AppDependencies (CLIENT/PRODUCER/INTERNAL spans).
//
// All builders use `union withsource=SourceTable` so span kind can be
// derived from the table of origin, and rely on the verified exporter
// behavior that root spans have ParentId == OperationId (not empty).
// Time ranges are passed through the azlogs Timespan option, not the query
// body.

// unionHead is the shared prologue: both span tables plus the per-row
// derived columns every query needs.
const unionHead = `union withsource=SourceTable AppRequests, AppDependencies
| extend SpanKind = iff(SourceTable == "AppRequests", "SERVER", "CLIENT")
| extend IsRoot = (ParentId == OperationId or isempty(ParentId))
| extend SpanEnd = TimeGenerated + (todouble(DurationMs) * 1ms)`

// idPattern matches W3C trace/span IDs as hex strings. Inputs that fail this
// never reach a query.
var idPattern = regexp.MustCompile(`^[a-fA-F0-9]{1,64}$`)

func ValidID(s string) bool {
	return idPattern.MatchString(s)
}

// BuildTracesListKQL renders the traces list query: group spans by
// OperationId and compute per-trace summaries. Root-span fields fall back to
// the earliest span when the root is outside the window or sampled away.
func BuildTracesListKQL(p TracesParams) string {
	var sb strings.Builder
	sb.WriteString(unionHead)
	writeScopeFilters(&sb, p)
	sb.WriteString(`
| summarize
    SpanCount = count(),
    ErrorCount = countif(Success == false),
    RootSpanId = take_anyif(Id, IsRoot),
    RootSpanName = take_anyif(Name, IsRoot),
    RootSpanKind = take_anyif(SpanKind, IsRoot),
    (StartTime, EarliestSpanId, EarliestSpanName, EarliestSpanKind) = arg_min(TimeGenerated, Id, Name, SpanKind),
    EndTime = max(SpanEnd)
  by TraceId = OperationId
| extend
    RootSpanId = iff(isempty(RootSpanId), EarliestSpanId, RootSpanId),
    RootSpanName = iff(isempty(RootSpanName), EarliestSpanName, RootSpanName),
    RootSpanKind = iff(isempty(RootSpanKind), EarliestSpanKind, RootSpanKind)
| project TraceId, SpanCount, ErrorCount, RootSpanId, RootSpanName, RootSpanKind, StartTime, EndTime`)
	// `top N by` is the deterministic top-N-in-order operator. A separate
	// `order by | take N` would rely on the planner fusing the two steps;
	// `top` states the intent directly.
	fmt.Fprintf(&sb, "\n| top %d by StartTime %s", p.Limit, sortOrderOrDefault(p.SortOrder))
	return sb.String()
}

// BuildSpansKQL renders the spans-of-one-trace query.
func BuildSpansKQL(p TracesParams) string {
	var sb strings.Builder
	sb.WriteString(unionHead)
	sb.WriteString("\n| where OperationId == ")
	sb.WriteString(kqlString(p.TraceID))
	writeScopeFilters(&sb, p)
	sb.WriteString(spanProjection(p.IncludeAttributes))
	fmt.Fprintf(&sb, "\n| top %d by TimeGenerated asc", p.Limit)
	return sb.String()
}

// BuildSpanDetailsKQL renders the single-span lookup.
func BuildSpanDetailsKQL(traceID, spanID string) string {
	var sb strings.Builder
	sb.WriteString(unionHead)
	sb.WriteString("\n| where OperationId == ")
	sb.WriteString(kqlString(traceID))
	sb.WriteString("\n| where Id == ")
	sb.WriteString(kqlString(spanID))
	sb.WriteString(spanProjection(true))
	sb.WriteString("\n| take 1")
	return sb.String()
}

func PingKQL() string {
	return "union AppRequests, AppDependencies | take 1"
}

// writeScopeFilters appends the tenancy filters. Namespace is always
// emitted; the handler guarantees it is non-empty.
func writeScopeFilters(sb *strings.Builder, p TracesParams) {
	writePropertyFilter(sb, LabelNamespace, p.Namespace)
	if scope := releaseLineScope(p.ComponentUID, p.ReleaseLineUID, p.AllReleaseLines); scope != "" {
		sb.WriteString("\n| where ")
		sb.WriteString(scope)
	}
	if p.ProjectUID != "" {
		writePropertyFilter(sb, LabelProjectUID, p.ProjectUID)
	}
	if p.EnvironmentUID != "" {
		writePropertyFilter(sb, LabelEnvironmentUID, p.EnvironmentUID)
	}
}

func writePropertyFilter(sb *strings.Builder, label, value string) {
	sb.WriteString("\n| where ")
	sb.WriteString(propertyEquals(label, value))
}

// property renders the value of one span property, empty when the span lacks it.
func property(label string) string {
	return "tostring(Properties[" + kqlString(label) + "])"
}

func propertyEquals(label, value string) string {
	return property(label) + " == " + kqlString(value)
}

// releaseLineScope renders the predicate placing a span on a component's
// release lines. The component UID alone keeps its main line's spans, those
// with the UID and no release line label. A release line's pods carry the
// component UID under another label than the main line's, so allLines matches
// either label and covers every line. One release line matches on its UID
// alone. Empty without a component or a line.
func releaseLineScope(componentUID, lineUID string, allLines bool) string {
	switch {
	case lineUID != "":
		return propertyEquals(LabelReleaseLineUID, lineUID)
	case componentUID == "":
		return ""
	case allLines:
		return "(" + propertyEquals(LabelComponentUID, componentUID) +
			" or " + propertyEquals(LabelReleaseLineComponentUID, componentUID) + ")"
	}
	return propertyEquals(LabelComponentUID, componentUID) + " and isempty(" + property(LabelReleaseLineUID) + ")"
}

// spanProjection projects the columns the mapping layer reads. ParentSpanId
// is normalized to "" for root spans here so the convention matches the
// sibling adapters.
func spanProjection(includeAttributes bool) string {
	cols := `
| project
    TimeGenerated,
    SpanId = Id,
    ParentSpanId = iff(IsRoot, "", ParentId),
    Name,
    SpanKind,
    DurationMs = todouble(DurationMs),
    Success,
    StatusMessage = tostring(Properties["otel.status_description"]),
    SpanEnd`
	if includeAttributes {
		cols += `,
    Properties,
    Measurements`
	}
	return cols
}

func sortOrderOrDefault(s string) string {
	if strings.EqualFold(s, "asc") {
		return "asc"
	}
	return "desc"
}

// kqlString quotes s as a KQL string literal, escaping characters that
// would otherwise terminate or alter the literal.
func kqlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return `"` + s + `"`
}
