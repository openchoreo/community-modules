// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// kqlDatetimeLayout is KQL's datetime at its full 100ns tick precision.
const kqlDatetimeLayout = "2006-01-02T15:04:05.0000000Z"

// BuildEventsKQL renders an events query as three statements over one shared
// filtered set, answered in a single round trip:
//
//  1. Boundary: the timestamp of the limit-th event in sort order.
//  2. The events up to and including Boundary. Every event before it is within
//     the first limit, so this is the page plus the rest of the group sharing
//     its final timestamp - the contract forbids splitting that group, since a
//     caller resumes from the last timestamp it was given.
//  3. Total: every event in the window, counted exactly. Log Analytics puts no
//     cap on counting, so there is no reason to stop short of the true figure;
//     it exceeds the page whenever the page is not the whole window.
//
// Sorting happens before truncation in both 1 and 2, so the events left out
// are always those furthest from the sort direction.
//
// The window is [StartTime, EndTime) and is written into the query: the SDK
// Timespan only bounds the scan, and its end inclusivity is not specified.
func BuildEventsKQL(p EventsParams, table, scopeName string) (string, error) {
	if p.Component != nil && p.Workflow != nil {
		return "", errors.New("loganalytics: events query has both a component and a workflow scope")
	}
	if p.Component == nil && p.Workflow == nil && len(p.Reasons) == 0 {
		// An unscoped sweep without reasons would read every namespace.
		return "", errors.New("loganalytics: unscoped events query requires reasons")
	}

	limit := p.Limit
	if limit < 1 {
		limit = DefaultEventsLimit
	}
	order := sortOrderOrDefault(p.SortOrder)
	boundaryAgg, boundaryCmp := "max", "<="
	if order == SortDesc {
		boundaryAgg, boundaryCmp = "min", ">="
	}

	var sb strings.Builder
	sb.WriteString(eventsBase(p, table, scopeName))

	fmt.Fprintf(&sb,
		"let Boundary = toscalar(Base | top %d by TimeGenerated %s | summarize %s(TimeGenerated));\n",
		limit, order, boundaryAgg)

	// _ItemId breaks ties so events sharing a timestamp come back in a stable
	// order. The receiver re-emits an event on every update under the same
	// event UID, so the UID would not.
	fmt.Fprintf(&sb, `Base
| where TimeGenerated %s Boundary
| order by TimeGenerated %s, _ItemId %s
| take %d
| project
    TimeGenerated,
    Message         = tostring(Body),
    EventType       = tostring(SeverityText),
    Reason          = %s,
    ObjectNamespace = %s,
    ObjectKind      = %s,
    ObjectName      = %s,
    NamespaceName   = %s,
    ComponentName   = %s,
    ComponentUID    = %s,
    ProjectName     = %s,
    ProjectUID      = %s,
    EnvironmentName = %s,
    EnvironmentUID  = %s;
`,
		boundaryCmp, order, order, limit+MaxEventBoundaryGroup,
		logAttr(attrEventReason),
		logAttr(attrObjectNamespace),
		resAttr(resObjectKind),
		resAttr(resObjectName),
		eventLabel(LabelNamespace),
		eventLabel(LabelComponentName),
		eventLabel(LabelComponentUID),
		eventLabel(LabelProjectName),
		eventLabel(LabelProjectUID),
		eventLabel(LabelEnvironmentName),
		eventLabel(LabelEnvironmentUID),
	)

	sb.WriteString("Base\n| summarize Total = count()")

	return sb.String(), nil
}

// eventsBase renders the `let Base = ...;` statement holding every filter.
// It projects nothing: `let` is not materialised, so anything computed here
// would be recomputed by each statement that reads Base.
func eventsBase(p EventsParams, table, scopeName string) string {
	var sb strings.Builder
	sb.WriteString("let Base = ")
	sb.WriteString(table)

	// Cheapest first: the time range prunes extents, ScopeName is a plain
	// column, and the dynamic-column lookups run on what is left.
	sb.WriteString("\n| where TimeGenerated >= ")
	sb.WriteString(kqlDatetime(p.StartTime))
	sb.WriteString(" and TimeGenerated < ")
	sb.WriteString(kqlDatetime(p.EndTime))
	sb.WriteString("\n| where ScopeName == ")
	sb.WriteString(kqlString(scopeName))

	switch {
	case p.Component != nil:
		s := p.Component
		sb.WriteString("\n| where ")
		sb.WriteString(dynEquals("ResourceAttributes", eventLabel(LabelNamespace), s.Namespace))
		for _, f := range []struct{ label, value string }{
			{LabelProjectUID, s.ProjectUID},
			{LabelComponentUID, s.ComponentUID},
			{LabelEnvironmentUID, s.EnvironmentUID},
		} {
			if f.value == "" {
				continue
			}
			sb.WriteString("\n| where ")
			sb.WriteString(dynEquals("ResourceAttributes", eventLabel(f.label), f.value))
		}

	case p.Workflow != nil:
		// Workflow pods run in workflows-<namespace> and their objects are named
		// after the run, with the task embedded - the workflow logs convention.
		// Event attributes carry no Argo node annotation, so the object name is
		// what identifies a task.
		s := p.Workflow
		sb.WriteString("\n| where ")
		sb.WriteString(dynEquals("Attributes", logAttr(attrObjectNamespace), WorkflowNamespacePrefix+s.Namespace))
		sb.WriteString("\n| where ")
		sb.WriteString(resAttr(resObjectName))
		sb.WriteString(" startswith_cs ")
		sb.WriteString(kqlString(s.WorkflowRunName))
		if s.TaskName != "" {
			sb.WriteString("\n| where ")
			sb.WriteString(resAttr(resObjectName))
			sb.WriteString(" contains_cs ")
			sb.WriteString(kqlString(s.TaskName))
		}
	}

	// Reasons narrow a scoped query too; on an unscoped sweep they are the
	// only filter besides the window, and nothing may widen or replace them.
	if len(p.Reasons) > 0 {
		sb.WriteString("\n| where ")
		sb.WriteString(dynIn("Attributes", logAttr(attrEventReason), p.Reasons))
	}

	sb.WriteString(";\n")
	return sb.String()
}

// kqlDatetime renders t as a KQL datetime literal. KQL keeps 100ns ticks, so a
// finer time is rounded up to the next tick: stored times sit on ticks, which
// makes `>= ceil(t)` and `< ceil(t)` exactly `>= t` and `< t`.
func kqlDatetime(t time.Time) string {
	t = t.UTC()
	if r := t.Nanosecond() % 100; r != 0 {
		t = t.Add(time.Duration(100 - r))
	}
	return "datetime(" + t.Format(kqlDatetimeLayout) + ")"
}

// EventsTableProbeKQL fails when the events table does not exist yet, and
// otherwise returns nothing.
func EventsTableProbeKQL(table string) string {
	return table + " | take 0"
}
