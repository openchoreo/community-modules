// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
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
	if !hasTermFilter(conds, EvComponentID, "comp-uid") {
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
	if !ok || len(sort) != 1 {
		t.Fatalf("unexpected sort: %v", query["sort"])
	}
	ts, ok := sort[0][EvTimestamp].(map[string]interface{})
	if !ok || ts["order"] != "desc" {
		t.Errorf("expected default sort order desc, got %v", sort[0])
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
