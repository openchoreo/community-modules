// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxTieRecords bounds the follow-up read that completes a page ending mid-timestamp. A
// Kubernetes event timestamp has second granularity, so this many events in one second
// from one scope is far beyond anything a cluster produces.
const maxTieRecords = 10000

// EventsQueryParams holds the parameters of an events query. At most one of Component and
// Workflow is set; neither being set is an unscoped sweep, which must carry Reasons.
type EventsQueryParams struct {
	Component *ComponentEventsScope
	Workflow  *WorkflowEventsScope
	Reasons   []string
	StartTime time.Time
	EndTime   time.Time
	Limit     int
	SortOrder string
}

// ComponentEventsScope scopes events to an OpenChoreo namespace and, optionally, a
// project, component and environment within it.
type ComponentEventsScope struct {
	Namespace     string
	ProjectID     string
	ComponentID   string
	EnvironmentID string
}

// WorkflowEventsScope scopes events to a workflow run and, optionally, one of its tasks.
type WorkflowEventsScope struct {
	Namespace       string
	WorkflowRunName string
	TaskName        string
}

// EventEntry is one parsed Kubernetes event.
type EventEntry struct {
	Timestamp       time.Time
	Message         string
	Type            string
	Reason          string
	ObjectKind      string
	ObjectName      string
	ObjectNamespace string
	ComponentName   string
	ComponentID     string
	ProjectName     string
	ProjectID       string
	EnvironmentName string
	EnvironmentID   string
	NamespaceName   string
}

// EventsResult is a page of events plus the number of events matching the query.
type EventsResult struct {
	Events     []EventEntry
	TotalCount int
	Took       int
}

func eventsConditions(p EventsQueryParams) (string, error) {
	// Only records the k8s_events receiver produced carry an event reason, which is what
	// keeps container logs out of an events query that has no other scope.
	conditions := []string{"isNotNull(" + field(evReason) + ")"}

	switch {
	case p.Component != nil:
		s := p.Component
		if s.Namespace == "" {
			return "", fmt.Errorf("namespace is required for component event queries")
		}
		conditions = append(conditions, eq(evNamespaceName, s.Namespace))
		if s.ProjectID != "" {
			conditions = append(conditions, eq(evProjectID, s.ProjectID))
		}
		if s.ComponentID != "" {
			conditions = append(conditions, eq(evComponentID, s.ComponentID))
		}
		if s.EnvironmentID != "" {
			conditions = append(conditions, eq(evEnvironmentID, s.EnvironmentID))
		}
	case p.Workflow != nil:
		s := p.Workflow
		if s.Namespace == "" || s.WorkflowRunName == "" {
			return "", fmt.Errorf("namespace and workflow run name are required for workflow event queries")
		}
		conditions = append(conditions,
			"startsWith("+field(evObjectName)+", "+str(s.WorkflowRunName)+")",
			eq(evObjectNamespace, workflowNamespacePrefix+s.Namespace),
		)
		if s.TaskName != "" {
			conditions = append(conditions, containsText(evObjectName, s.TaskName, true))
		}
	default:
		// An unscoped sweep is only ever narrowed by reasons. Refusing one without them
		// here, as well as in the handler, means no code path can turn a missing scope
		// into "every namespace".
		if len(p.Reasons) == 0 {
			return "", fmt.Errorf("an unscoped events query requires reasons")
		}
	}

	conditions = append(conditions, anyOf(evReason, p.Reasons))
	return and(conditions...), nil
}

var eventsFields = []string{
	fTimestamp, fContent, fLogLevel, evSeverity, evReason, evObjectKind, evObjectName, evObjectNamespace,
	evComponentName, evComponentID, evProjectName, evProjectID,
	evEnvironmentName, evEnvironmentID, evNamespaceName,
}

// GenerateEventsQueries returns the page query and the count query for events.
func GenerateEventsQueries(p EventsQueryParams) (records, count string, err error) {
	conditions, err := eventsConditions(p)
	if err != nil {
		return "", "", err
	}
	return buildRecordsQuery("", conditions, p.SortOrder, clampLimit(p.Limit), eventsFields),
		buildCountQuery("", conditions), nil
}

// generateTieQuery reads every event at exactly one timestamp.
func generateTieQuery(conditions string, ts time.Time) string {
	return fetchLogs().
		filter(conditions).
		filter(field(fTimestamp) + " == toTimestamp(" + str(ts.UTC().Format(time.RFC3339Nano)) + ")").
		then("limit " + strconv.Itoa(maxTieRecords)).
		then("fields " + fieldList(eventsFields)).
		String()
}

// GetEvents returns a page of events and the number of events matching the query.
//
// The page honours the contract's resumption rules: total is exact, sorting happens before
// truncation, and a page that would end part-way through the events sharing one timestamp
// is extended to cover all of them, so a caller resuming from the last timestamp neither
// skips events nor stalls on a timestamp holding more than a page of them.
func (c *Client) GetEvents(ctx context.Context, p EventsQueryParams) (*EventsResult, error) {
	conditions, err := eventsConditions(p)
	if err != nil {
		return nil, err
	}
	limit := clampLimit(p.Limit)
	recordsQuery := buildRecordsQuery("", conditions, p.SortOrder, limit, eventsFields)
	countQuery := buildCountQuery("", conditions)

	page, total, err := c.runPageAndCount(ctx, recordsQuery, countQuery, p.StartTime, p.EndTime, limit)
	if err != nil {
		return nil, err
	}

	records := page.Records
	if len(records) == limit && total > int64(limit) {
		last := timeField(records[len(records)-1], fTimestamp)
		if !last.IsZero() {
			ties, err := c.Query(ctx, QueryRequest{
				Query: generateTieQuery(conditions, last), Start: p.StartTime, End: p.EndTime,
				MaxRecords: maxTieRecords,
			})
			if err != nil {
				return nil, fmt.Errorf("tie extension query failed: %w", err)
			}
			records = extendTies(records, last, ties.Records)
		}
	}

	events := make([]EventEntry, 0, len(records))
	for _, r := range records {
		events = append(events, parseEvent(r))
	}
	return &EventsResult{Events: events, TotalCount: int(total), Took: int(page.Took.Milliseconds())}, nil
}

// extendTies replaces the page's trailing run of records at the last timestamp with the
// complete set read for that timestamp, so none of them is split across pages.
func extendTies(page []map[string]any, last time.Time, ties []map[string]any) []map[string]any {
	cut := len(page)
	for cut > 0 && timeField(page[cut-1], fTimestamp).Equal(last) {
		cut--
	}
	if len(ties) < len(page)-cut {
		// The follow-up read saw fewer records than the page itself holds at that
		// timestamp, so it cannot be the complete set; keep the page as it was.
		return page
	}
	out := make([]map[string]any, 0, cut+len(ties))
	out = append(out, page[:cut]...)
	return append(out, ties...)
}

// eventType recovers the Kubernetes event type from the log record. The receiver carries
// Normal/Warning as the OTLP severity text; depending on ingest, Grail may hold it verbatim
// or normalised to a log level, so both spellings are mapped back. Grail normalises
// Normal to NOTICE.
func eventType(r map[string]any) string {
	for _, key := range []string{fLogLevel, evSeverity} {
		switch strings.ToUpper(stringField(r, key)) {
		case "NORMAL", "INFO", "NOTICE":
			return "Normal"
		case "WARNING", "WARN":
			return "Warning"
		}
	}
	return ""
}

func parseEvent(r map[string]any) EventEntry {
	return EventEntry{
		Timestamp:       timeField(r, fTimestamp),
		Message:         stringField(r, fContent),
		Type:            eventType(r),
		Reason:          stringField(r, evReason),
		ObjectKind:      stringField(r, evObjectKind),
		ObjectName:      stringField(r, evObjectName),
		ObjectNamespace: stringField(r, evObjectNamespace),
		ComponentName:   stringField(r, evComponentName),
		ComponentID:     stringField(r, evComponentID),
		ProjectName:     stringField(r, evProjectName),
		ProjectID:       stringField(r, evProjectID),
		EnvironmentName: stringField(r, evEnvironmentName),
		EnvironmentID:   stringField(r, evEnvironmentID),
		NamespaceName:   stringField(r, evNamespaceName),
	}
}
