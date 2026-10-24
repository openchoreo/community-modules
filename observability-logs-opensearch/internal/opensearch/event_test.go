// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"strings"
	"testing"
	"time"
)

func mustConditions(t *testing.T, query map[string]interface{}) []interface{} {
	t.Helper()
	q, ok := query["query"].(map[string]interface{})
	if !ok {
		t.Fatalf("query missing 'query' object: %v", query)
	}
	boolQ, ok := q["bool"].(map[string]interface{})
	if !ok {
		t.Fatalf("query missing 'bool' object: %v", q)
	}
	must, ok := boolQ["must"].([]map[string]interface{})
	if !ok {
		t.Fatalf("bool missing 'must' array: %v", boolQ)
	}
	out := make([]interface{}, len(must))
	for i, m := range must {
		out[i] = m
	}
	return out
}

// hasTermFilter reports whether the must conditions contain a term filter on field=value.
func hasTermFilter(conds []interface{}, field, value string) bool {
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		term, ok := m["term"].(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := term[field]; ok && v == value {
			return true
		}
	}
	return false
}

// hasTermsFilter reports whether the must conditions contain a terms filter on
// field matching exactly the given values (order-independent).
func hasTermsFilter(conds []interface{}, field string, values ...string) bool {
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		terms, ok := m["terms"].(map[string]interface{})
		if !ok {
			continue
		}
		got, ok := terms[field].([]string)
		if !ok || len(got) != len(values) {
			continue
		}
		// Counted, not a set: with a set, got ["A","A"] satisfies want ["A","B"]
		// because both values are present and the lengths agree.
		want := map[string]int{}
		for _, v := range values {
			want[v]++
		}
		matched := true
		for _, v := range got {
			if want[v] == 0 {
				matched = false
				break
			}
			want[v]--
		}
		if matched {
			return true
		}
	}
	return false
}

func hasWildcardFilter(conds []interface{}, field, value string) bool {
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		w, ok := m["wildcard"].(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := w[field]; ok && v == value {
			return true
		}
	}
	return false
}

func TestBuildComponentEventsQuery(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildComponentEventsQuery(EventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
		ProjectID:     "proj-uid",
		ComponentID:   "comp-uid",
		EnvironmentID: "env-uid",
		Limit:         50,
		SortOrder:     "asc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := query["size"]; got != 50 {
		t.Errorf("expected size=50, got %v", got)
	}

	conds := mustConditions(t, query)
	if !hasTermFilter(conds, EvNamespaceName, "default") {
		t.Errorf("missing namespace term on %s", EvNamespaceName)
	}
	if !hasTermFilter(conds, EvProjectID, "proj-uid") {
		t.Errorf("missing project term on %s", EvProjectID)
	}
	if !strings.Contains(queryJSON(t, query), `{"term":{"`+EvComponentID+`":"comp-uid"}}`) {
		t.Errorf("missing component term on %s", EvComponentID)
	}
	if !hasTermFilter(conds, EvEnvironmentID, "env-uid") {
		t.Errorf("missing environment term on %s", EvEnvironmentID)
	}

	// Ensure field paths use dotted (not underscore) keys.
	if EvComponentID != "resource.k8s.object.label.openchoreo.dev/component-uid" {
		t.Errorf("unexpected component-uid field path: %s", EvComponentID)
	}
}

func TestBuildComponentEventsQuery_Defaults(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildComponentEventsQuery(EventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := query["size"]; got != 100 {
		t.Errorf("expected default size=100, got %v", got)
	}
	sort, ok := query["sort"].([]map[string]interface{})
	if !ok || len(sort) != 2 {
		t.Fatalf("unexpected sort: %v", query["sort"])
	}
	ts, ok := sort[0][EvTimestamp].(map[string]interface{})
	if !ok || ts["order"] != "desc" {
		t.Errorf("expected default sort order desc, got %v", sort[0])
	}
	tiebreaker, ok := sort[1]["_seq_no"].(map[string]interface{})
	if !ok || tiebreaker["order"] != "desc" {
		t.Errorf("expected _seq_no tiebreaker desc, got %v", sort[1])
	}
	if _, present := query["search_after"]; present {
		t.Errorf("events queries must not carry search_after; resumption is by timestamp")
	}
}

func TestBuildComponentEventsQuery_StartBoundIsInclusive(t *testing.T) {
	// A caller reading a window in parts resumes AT the last timestamp it was
	// handed. An exclusive start drops every event bearing that timestamp,
	// including any the previous read did not return -- silently, because the
	// sweep still advances.
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildComponentEventsQuery(EventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conds := mustConditions(t, query)
	var bounds map[string]interface{}
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if rng, ok := m["range"].(map[string]interface{}); ok {
			if b, ok := rng[EvTimestamp].(map[string]interface{}); ok {
				bounds = b
			}
		}
	}
	if bounds == nil {
		t.Fatalf("no %s range filter found in %v", EvTimestamp, conds)
	}
	if _, exclusive := bounds["gt"]; exclusive {
		t.Errorf("events start bound must be inclusive (gte), got gt: %v", bounds)
	}
	if bounds["gte"] != "2026-06-05T00:00:00Z" {
		t.Errorf("expected gte start bound, got %v", bounds)
	}
	// The end stays exclusive so consecutive windows do not overlap.
	if bounds["lt"] != "2026-06-06T00:00:00Z" {
		t.Errorf("expected lt end bound, got %v", bounds)
	}
}

func TestBuildComponentEventsQuery_CountsPastTheLimit(t *testing.T) {
	// hits.total saturates at 10000 by default and becomes a floor, not a count.
	// A caller derives completeness from total against the events returned, so the
	// count has to reach limit+1 -- enough to tell "exactly a full page" from
	// "more than a page", and no more.
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildComponentEventsQuery(EventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
		Limit:         50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := query["track_total_hits"]; got != 51 {
		t.Errorf("expected track_total_hits=51 for limit=50, got %v", got)
	}
	if got := query["size"]; got != 50 {
		t.Errorf("expected size=50, got %v", got)
	}
}

func TestBuildComponentEventsQuery_Reasons(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildComponentEventsQuery(EventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
		Reasons:       []string{"DeploymentSucceeded", "DeploymentFailed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	conds := mustConditions(t, query)
	if !hasTermsFilter(conds, EvReason, "DeploymentSucceeded", "DeploymentFailed") {
		t.Errorf("missing reasons terms filter on %s: %v", EvReason, conds)
	}
}

func TestBuildComponentEventsQuery_MissingRequired(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	if _, err := qb.BuildComponentEventsQuery(EventsQueryParams{
		StartTime: "2026-06-05T00:00:00Z",
		EndTime:   "2026-06-06T00:00:00Z",
	}); err == nil {
		t.Errorf("expected error when namespace is missing")
	}
}

func TestBuildWorkflowEventsQuery(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildWorkflowEventsQuery(WorkflowEventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
		WorkflowRunID: "build-run-123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	conds := mustConditions(t, query)
	if !hasWildcardFilter(conds, EvObjectName, "build-run-123*") {
		t.Errorf("missing object name wildcard on %s", EvObjectName)
	}
	if !hasTermFilter(conds, EvObjectNamespace, "workflows-default") {
		t.Errorf("missing workflows-<ns> term on %s", EvObjectNamespace)
	}
}

func TestBuildWorkflowEventsQuery_WithTaskName(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildWorkflowEventsQuery(WorkflowEventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
		WorkflowRunID: "build-run-123",
		TaskName:      "clone-step",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	conds := mustConditions(t, query)
	if !hasWildcardFilter(conds, EvObjectName, "build-run-123*") {
		t.Errorf("missing object name prefix wildcard on %s", EvObjectName)
	}
	if !hasWildcardFilter(conds, EvObjectName, "*clone-step*") {
		t.Errorf("missing task name wildcard on %s", EvObjectName)
	}
}

func TestBuildWorkflowEventsQuery_MissingRequired(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	if _, err := qb.BuildWorkflowEventsQuery(WorkflowEventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
	}); err == nil {
		t.Errorf("expected error when workflow run ID is missing")
	}
}

func TestBuildWorkflowEventsQuery_Reasons(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildWorkflowEventsQuery(WorkflowEventsQueryParams{
		StartTime:     "2026-06-05T00:00:00Z",
		EndTime:       "2026-06-06T00:00:00Z",
		NamespaceName: "default",
		WorkflowRunID: "build-run-123",
		Reasons:       []string{"Completed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conds := mustConditions(t, query)
	if !hasTermsFilter(conds, EvReason, "Completed") {
		t.Errorf("missing reasons terms filter on %s: %v", EvReason, conds)
	}
}

func TestBuildReasonFilteredEventsQuery(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildReasonFilteredEventsQuery(ReasonFilteredEventsQueryParams{
		StartTime: "2026-06-05T00:00:00Z",
		EndTime:   "2026-06-06T00:00:00Z",
		Reasons:   []string{"DeploymentStarted", "DeploymentSucceeded"},
		Limit:     500,
		SortOrder: "asc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := query["size"]; got != 500 {
		t.Errorf("expected size=500, got %v", got)
	}

	conds := mustConditions(t, query)
	if !hasTermsFilter(conds, EvReason, "DeploymentStarted", "DeploymentSucceeded") {
		t.Errorf("missing reasons terms filter on %s: %v", EvReason, conds)
	}
	// No namespace/component/environment scoping: an unscoped sweep must not
	// restrict to any single namespace.
	if hasTermFilter(conds, EvNamespaceName, "default") {
		t.Errorf("unscoped query should not filter by namespace: %v", conds)
	}
}

func TestBuildReasonFilteredEventsQuery_MissingReasons(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	if _, err := qb.BuildReasonFilteredEventsQuery(ReasonFilteredEventsQueryParams{
		StartTime: "2026-06-05T00:00:00Z",
		EndTime:   "2026-06-06T00:00:00Z",
	}); err == nil {
		t.Errorf("expected error when reasons is empty")
	}
}

func TestBuildReasonFilteredEventsQuery_MissingTimeRange(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	if _, err := qb.BuildReasonFilteredEventsQuery(ReasonFilteredEventsQueryParams{
		Reasons: []string{"DeploymentStarted"},
	}); err == nil {
		t.Errorf("expected error when time range is missing")
	}
}

func TestParseEventHit(t *testing.T) {
	hit := Hit{
		Source: map[string]interface{}{
			"@timestamp": "2026-06-05T12:24:06Z",
			"body":       "Job completed",
			"severity": map[string]interface{}{
				"text":   "Normal",
				"number": 9,
			},
			"attributes": map[string]interface{}{
				"k8s.event.reason":   "Completed",
				"k8s.namespace.name": "dp-default-default-development-f8e58905",
			},
			"resource": map[string]interface{}{
				"k8s.object.kind": "Job",
				"k8s.object.name": "github-issue-reporter-development-5e31cab9-29677704",
				"k8s.object.label.openchoreo.dev/component":       "github-issue-reporter",
				"k8s.object.label.openchoreo.dev/component-uid":   "a022c8af-78c8-4fa2-a9fd-51eb8579ecb2",
				"k8s.object.label.openchoreo.dev/project":         "default",
				"k8s.object.label.openchoreo.dev/project-uid":     "fc480b7a-d4bb-4638-b39d-66b317f24fe7",
				"k8s.object.label.openchoreo.dev/environment":     "development",
				"k8s.object.label.openchoreo.dev/environment-uid": "cb6b3d47-f636-4e2d-aaa3-1b2b70283401",
				"k8s.object.label.openchoreo.dev/namespace":       "default",
			},
		},
	}

	entry := ParseEventHit(hit)

	if !entry.Timestamp.Equal(time.Date(2026, 6, 5, 12, 24, 6, 0, time.UTC)) {
		t.Errorf("unexpected timestamp: %v", entry.Timestamp)
	}
	checks := map[string]struct{ got, want string }{
		"message":         {entry.Message, "Job completed"},
		"type":            {entry.Type, "Normal"},
		"reason":          {entry.Reason, "Completed"},
		"objectKind":      {entry.ObjectKind, "Job"},
		"objectName":      {entry.ObjectName, "github-issue-reporter-development-5e31cab9-29677704"},
		"objectNamespace": {entry.ObjectNamespace, "dp-default-default-development-f8e58905"},
		"componentName":   {entry.ComponentName, "github-issue-reporter"},
		"componentID":     {entry.ComponentID, "a022c8af-78c8-4fa2-a9fd-51eb8579ecb2"},
		"projectName":     {entry.ProjectName, "default"},
		"projectID":       {entry.ProjectID, "fc480b7a-d4bb-4638-b39d-66b317f24fe7"},
		"environmentName": {entry.EnvironmentName, "development"},
		"environmentID":   {entry.EnvironmentID, "cb6b3d47-f636-4e2d-aaa3-1b2b70283401"},
		"namespaceName":   {entry.NamespaceName, "default"},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", name, c.got, c.want)
		}
	}
}

func TestParseEventHit_Empty(t *testing.T) {
	entry := ParseEventHit(Hit{Source: map[string]interface{}{}})
	if entry.Message != "" || entry.Type != "" || entry.Reason != "" || entry.ObjectKind != "" {
		t.Errorf("expected empty entry, got %+v", entry)
	}
}
