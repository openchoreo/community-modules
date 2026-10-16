// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"fmt"
	"strings"
)

// ClusterInstanceExpr derives the cluster a record came from.
// ContainerLogV2 carries _ResourceId, the AKS cluster's ARM resource ID,
// written by Azure itself. The cluster's resource name is its last path segment.
const ClusterInstanceExpr = `tolower(extract(@"/([^/]+)$", 1, tostring(_ResourceId)))`

// MaxPlatformTotal bounds the total count. The contract caps total at 1000,
// so counting past 1001 would buy nothing and scan the whole window.
const MaxPlatformTotal = 1000

// Bounds on how many distinct filter values one request may ask for, from the
// adapter contract's PlatformLogFilterValuesRequest.maxValues.
const (
	DefaultMaxFilterValues = 100
	MaxMaxFilterValues     = 1000
)

// filterColumnExpr maps a listable filter onto the expression holding its value.
func filterColumnExpr(f PlatformLogFilter) string {
	switch f {
	case FilterClusterInstance:
		// platformBase always extends this column, so read it rather than
		// recomputing the expression and giving the two a way to drift.
		return "ClusterInstance"
	case FilterNamespace:
		return "PodNamespace"
	case FilterPodName:
		return "PodName"
	case FilterContainerName:
		return "ContainerName"
	default:
		return ""
	}
}

// BuildPlatformLogsKQL renders a platform-logs query as two tabular
// statements over one shared filtered set: the page of records, and the total
// matching count. Both read the same `Base`, so the count always describes the
// rows beside it, and one request stays one round trip - Log Analytics allows
// only five concurrent Analytics queries per identity, so a second call would
// halve throughput.
//
// Time range is passed via the SDK's Timespan option, not the query body, so
// the query stays portable between /query and /search.
func BuildPlatformLogsKQL(p PlatformLogsParams) string {
	var sb strings.Builder

	sb.WriteString(platformBase(p, ""))

	sb.WriteString("Base\n| order by TimeGenerated ")
	sb.WriteString(string(sortOrderOrDefault(p.SortOrder)))
	if p.Limit > 0 {
		fmt.Fprintf(&sb, "\n| take %d", p.Limit)
	}
	// Level is projected either way; platformBase only computes it when it also
	// has to filter on it, so compute it here over the page when it did not.
	if len(p.LogLevels) == 0 {
		sb.WriteString("\n")
		sb.WriteString(levelExpr())
	}
	sb.WriteString(`
| extend _imgName = tostring(KubernetesMetadata.image),
         _imgRepo = tostring(KubernetesMetadata.imageRepo),
         _imgTag  = tostring(KubernetesMetadata.imageTag)
| extend ContainerImage = strcat(
    iff(isempty(_imgRepo) or _imgName startswith strcat(_imgRepo, "/"),
        "", strcat(_imgRepo, "/")),
    _imgName,
    iff(isempty(_imgTag) or _imgName contains ":",
        "", strcat(":", _imgTag)))
| project
    TimeGenerated,
    LogMessage = tostring(LogMessage),
    Level,
    ClusterInstance,
    PodNamespace,
    PodName,
    ContainerName,
    NodeName = Computer,
    ContainerImage,
    Labels = tostring(KubernetesMetadata.podLabels);
`)

	// take before summarize bounds the scan: the contract caps total at 1000,
	// so one past the cap is all that distinguishes "1000" from "more".
	fmt.Fprintf(&sb, "Base\n| take %d\n| summarize Total = count()", MaxPlatformTotal+1)

	return sb.String()
}

// BuildPlatformLogFilterValuesKQL renders the distinct values one filter takes
// under a query, with a count for each, plus how many distinct values match.
//
// The filter's own selections are cleared from the conditions: a filter that
// counted its own selection would offer only what is already picked.
func BuildPlatformLogFilterValuesKQL(p PlatformLogFilterValuesParams) string {
	col := filterColumnExpr(p.Filter)
	var sb strings.Builder

	sb.WriteString(platformBase(p.Query, p.Filter))

	// Records where the field is absent are not represented: no filter value
	// would select an empty string.
	values := "Base\n| extend _value = " + col + "\n| where isnotempty(_value)"
	if p.ValueSearch != "" {
		// `contains` is a case-insensitive literal substring match. The needle
		// is a quoted literal, never a pattern the caller can inject.
		values += "\n| where _value contains " + kqlString(p.ValueSearch)
	}

	// The handler clamps this, but the builder is exported: take 0 returns
	// nothing, which reads as "this filter has no values" rather than as a bug.
	maxValues := p.MaxValues
	if maxValues < 1 {
		maxValues = DefaultMaxFilterValues
	}
	if maxValues > MaxMaxFilterValues {
		maxValues = MaxMaxFilterValues
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

// platformBase renders the `let Base = ...;` statement holding every filter,
// shared by the record, count and filter-value queries. Sharing it is what
// guarantees the values offered by a filter actually match returned records.
//
// skip names a filter whose own selections are dropped; "" keeps them all.
func platformBase(p PlatformLogsParams, skip PlatformLogFilter) string {
	var sb strings.Builder
	sb.WriteString("let Base = ")
	sb.WriteString(ContainerLogV2Table)

	// An absent or empty multi-value filter is not a filter. Getting this
	// wrong turns an unfiltered platform query into one matching nothing.
	if skip != FilterNamespace {
		writeInFilter(&sb, "PodNamespace", p.Namespaces)
	}
	if skip != FilterPodName {
		writeInFilter(&sb, "PodName", p.PodNames)
	}
	if skip != FilterContainerName {
		writeInFilter(&sb, "ContainerName", p.ContainerNames)
	}

	// Keys are sorted so the same filters always render the same query.
	for _, k := range sortedKeys(p.Labels) {
		sb.WriteString("\n| where ")
		sb.WriteString(podLabelEquals(k, p.Labels[k]))
	}

	if p.SearchPhrase != "" {
		sb.WriteString("\n| where tostring(LogMessage) contains ")
		sb.WriteString(kqlString(p.SearchPhrase))
	}

	sb.WriteString("\n| extend ClusterInstance = ")
	sb.WriteString(ClusterInstanceExpr)

	if skip != FilterClusterInstance && len(p.ClusterInstances) > 0 {
		// in~ because the derived name is lower-cased while a caller may send
		// the cluster's name as Azure displays it.
		sb.WriteString("\n| where ClusterInstance in~ (")
		writeKQLList(&sb, p.ClusterInstances)
		sb.WriteString(")")
	}

	// The level ladder is seven `contains` scans plus five type checks, and it
	// is only needed here when a level filter has to run before the take. When
	// no level was requested, BuildPlatformLogsKQL appends it after the take so
	// it evaluates over a page rather than the whole window, and a filter-values
	// query skips it entirely.
	if len(p.LogLevels) > 0 {
		sb.WriteString("\n")
		sb.WriteString(levelExpr())
		sb.WriteString("\n| where Level in (")
		writeKQLList(&sb, p.LogLevels)
		sb.WriteString(")")
	}

	sb.WriteString(";\n")
	return sb.String()
}

func writeInFilter(sb *strings.Builder, column string, values []string) {
	if len(values) == 0 {
		return
	}
	sb.WriteString("\n| where ")
	sb.WriteString(column)
	sb.WriteString(" in (")
	writeKQLList(sb, values)
	sb.WriteString(")")
}

func writeKQLList(sb *strings.Builder, values []string) {
	for i, v := range values {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(kqlString(v))
	}
}

// levelExpr renders the log level
func levelExpr() string {
	var sb strings.Builder
	sb.WriteString("| extend _msg = tostring(LogMessage)\n")

	// Step 1: a structured message's own level field. gettype guards the
	// string check so a numeric `level` falls through to the next key, as
	// resolveLogLevel's json.Unmarshal into a string does.
	//
	// The lookup goes through parse_json(_msg) rather than LogMessage directly.
	// LogMessage is dynamic and AMA may store a structured message either as a
	// parsed object or as a string holding JSON text; parse_json is a no-op on
	// the first and parses the second, which is exactly what resolveLogLevel's
	// json.Unmarshal over tostring(LogMessage) does.
	sb.WriteString("| extend _envelope = parse_json(_msg)\n")
	sb.WriteString("| extend _lvlRaw = case(")
	for _, key := range levelEnvelopeKeys {
		ref := "_envelope." + key
		fmt.Fprintf(&sb,
			`gettype(%s) == "string" and isnotempty(tostring(%s)), tostring(%s), `,
			ref, ref, ref)
	}
	sb.WriteString(`"")`)
	sb.WriteString("\n| extend _lvlUp = toupper(trim(@\"\\s+\", _lvlRaw))\n")

	// Step 2: fall back to a keyword scan of the message text, first match
	// wins - "INFO: retrying after ERROR" is an ERROR.
	sb.WriteString(`| extend Level = case(_lvlUp != "", `)
	sb.WriteString(levelAliasExpr())
	sb.WriteString(", ")
	for _, kw := range levelKeywords {
		fmt.Fprintf(&sb, "_msg contains %s, %s, ",
			kqlString(kw.Keyword), kqlString(kw.Level))
	}
	sb.WriteString(kqlString(defaultLogLevel))
	sb.WriteString(")")

	return sb.String()
}

// levelAliasExpr normalises an envelope-supplied level, mirroring normalizeLevel.
func levelAliasExpr() string {
	var sb strings.Builder
	sb.WriteString("case(")
	for _, a := range levelAliases {
		if len(a.From) == 1 {
			sb.WriteString("_lvlUp == " + kqlString(a.From[0]))
		} else {
			sb.WriteString("_lvlUp in (")
			writeKQLList(&sb, a.From)
			sb.WriteString(")")
		}
		sb.WriteString(", " + kqlString(a.To) + ", ")
	}
	sb.WriteString("_lvlUp)")
	return sb.String()
}
