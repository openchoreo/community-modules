// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var (
	evStart = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	evEnd   = time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
)

func sqlOf(t *testing.T, raw []byte) (string, map[string]interface{}) {
	t.Helper()
	var query map[string]interface{}
	if err := json.Unmarshal(raw, &query); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	q, ok := query["query"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing query object: %s", raw)
	}
	sql, _ := q["sql"].(string)
	return sql, q
}

func TestGenerateComponentEventsQuery(t *testing.T) {
	t.Run("basic query scopes by namespace label and defaults", func(t *testing.T) {
		params := EventsQueryParams{Namespace: "default", StartTime: evStart, EndTime: evEnd}
		raw, err := generateComponentEventsQuery(params, "k8s_events", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, q := sqlOf(t, raw)

		if !strings.Contains(sql, `FROM "k8s_events"`) {
			t.Errorf("expected events stream in FROM: %s", sql)
		}
		if !strings.Contains(sql, `k8s_object_label_openchoreo_dev_namespace = 'default'`) {
			t.Errorf("expected namespace label filter: %s", sql)
		}
		if !strings.Contains(sql, "ORDER BY _timestamp DESC") {
			t.Errorf("expected default DESC order: %s", sql)
		}
		if q["size"].(float64) != 100 {
			t.Errorf("expected default limit 100, got %v", q["size"])
		}
	})

	t.Run("filters by project, component, environment and respects limit/sort", func(t *testing.T) {
		params := EventsQueryParams{
			Namespace:     "default",
			ProjectID:     "proj-1",
			ComponentID:   "comp-1",
			EnvironmentID: "env-1",
			Limit:         25,
			SortOrder:     "asc",
			StartTime:     evStart,
			EndTime:       evEnd,
		}
		raw, err := generateComponentEventsQuery(params, "k8s_events", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, q := sqlOf(t, raw)

		for _, want := range []string{
			"k8s_object_label_openchoreo_dev_project_uid = 'proj-1'",
			"k8s_object_label_openchoreo_dev_component_uid = 'comp-1'",
			"k8s_object_label_openchoreo_dev_environment_uid = 'env-1'",
			"ORDER BY _timestamp ASC",
		} {
			if !strings.Contains(sql, want) {
				t.Errorf("expected %q in SQL: %s", want, sql)
			}
		}
		if q["size"].(float64) != 25 {
			t.Errorf("expected limit 25, got %v", q["size"])
		}
	})

	t.Run("missing namespace returns error", func(t *testing.T) {
		_, err := generateComponentEventsQuery(EventsQueryParams{StartTime: evStart, EndTime: evEnd}, "k8s_events", testLogger())
		if err == nil {
			t.Fatal("expected error for missing namespace")
		}
	})

	t.Run("escapes single quotes", func(t *testing.T) {
		params := EventsQueryParams{Namespace: "a'b", StartTime: evStart, EndTime: evEnd}
		raw, err := generateComponentEventsQuery(params, "k8s_events", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := sqlOf(t, raw)
		if !strings.Contains(sql, "'a''b'") {
			t.Errorf("expected escaped quote in SQL: %s", sql)
		}
	})
}

func TestGenerateWorkflowEventsQuery(t *testing.T) {
	t.Run("matches involved object name prefix and workflows namespace", func(t *testing.T) {
		params := WorkflowEventsQueryParams{
			Namespace:       "default",
			WorkflowRunName: "build-123",
			StartTime:       evStart,
			EndTime:         evEnd,
		}
		raw, err := generateWorkflowEventsQuery(params, "k8s_events", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := sqlOf(t, raw)

		if !strings.Contains(sql, "k8s_object_name LIKE 'build-123%'") {
			t.Errorf("expected object-name prefix match: %s", sql)
		}
		if !strings.Contains(sql, "k8s_namespace_name = 'workflows-default'") {
			t.Errorf("expected workflows namespace filter: %s", sql)
		}
	})

	t.Run("narrows by task name", func(t *testing.T) {
		params := WorkflowEventsQueryParams{
			Namespace:       "default",
			WorkflowRunName: "build-123",
			TaskName:        "compile",
			StartTime:       evStart,
			EndTime:         evEnd,
		}
		raw, err := generateWorkflowEventsQuery(params, "k8s_events", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := sqlOf(t, raw)
		if !strings.Contains(sql, "k8s_object_name LIKE '%compile%'") {
			t.Errorf("expected task-name match: %s", sql)
		}
	})

	t.Run("missing required fields returns error", func(t *testing.T) {
		cases := []WorkflowEventsQueryParams{
			{WorkflowRunName: "x", StartTime: evStart, EndTime: evEnd},
			{Namespace: "default", StartTime: evStart, EndTime: evEnd},
		}
		for _, params := range cases {
			if _, err := generateWorkflowEventsQuery(params, "k8s_events", testLogger()); err == nil {
				t.Errorf("expected error for params %+v", params)
			}
		}
	})
}

func TestGenerateEventsCountQueries(t *testing.T) {
	compRaw, err := generateComponentEventsCountQuery(EventsQueryParams{Namespace: "default", StartTime: evStart, EndTime: evEnd}, "k8s_events", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, q := sqlOf(t, compRaw)
	if !strings.Contains(sql, "SELECT count(*) as total") {
		t.Errorf("expected count select: %s", sql)
	}
	if q["size"].(float64) != 0 {
		t.Errorf("expected size 0 for count query, got %v", q["size"])
	}

	wfRaw, err := generateWorkflowEventsCountQuery(WorkflowEventsQueryParams{Namespace: "default", WorkflowRunName: "build-123", StartTime: evStart, EndTime: evEnd}, "k8s_events", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, _ = sqlOf(t, wfRaw)
	if !strings.Contains(sql, "SELECT count(*) as total") {
		t.Errorf("expected count select: %s", sql)
	}
}

func TestParseEventEntry(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	source := map[string]interface{}{
		"body":               "Job completed",
		"severity":           "Normal",
		"k8s_event_reason":   "Completed",
		"k8s_object_kind":    "Job",
		"k8s_object_name":    "build-123-abc",
		"k8s_namespace_name": "dp-default-development",
		"k8s_object_label_openchoreo_dev_component":       "github-issue-reporter",
		"k8s_object_label_openchoreo_dev_component_uid":   "9f88452c-0f3f-4cc9-bd77-dd6158fd23b9",
		"k8s_object_label_openchoreo_dev_project":         "default",
		"k8s_object_label_openchoreo_dev_project_uid":     "1e3c0e8d-05ac-4587-b0e2-90dbb80dded5",
		"k8s_object_label_openchoreo_dev_environment":     "development",
		"k8s_object_label_openchoreo_dev_environment_uid": "8430d38a-01fa-43a6-ab87-109710b5f1ae",
		"k8s_object_label_openchoreo_dev_namespace":       "default",
	}

	entry := parseEventEntry(ts.UnixMicro(), source)

	if !entry.Timestamp.Equal(ts) {
		t.Errorf("timestamp = %v, want %v", entry.Timestamp, ts)
	}
	checks := map[string]string{
		"Message":         entry.Message,
		"Type":            entry.Type,
		"Reason":          entry.Reason,
		"ObjectKind":      entry.ObjectKind,
		"ObjectName":      entry.ObjectName,
		"ObjectNamespace": entry.ObjectNamespace,
		"ComponentName":   entry.ComponentName,
		"ComponentID":     entry.ComponentID,
		"ProjectName":     entry.ProjectName,
		"EnvironmentName": entry.EnvironmentName,
		"NamespaceName":   entry.NamespaceName,
	}
	wants := map[string]string{
		"Message":         "Job completed",
		"Type":            "Normal",
		"Reason":          "Completed",
		"ObjectKind":      "Job",
		"ObjectName":      "build-123-abc",
		"ObjectNamespace": "dp-default-development",
		"ComponentName":   "github-issue-reporter",
		"ComponentID":     "9f88452c-0f3f-4cc9-bd77-dd6158fd23b9",
		"ProjectName":     "default",
		"EnvironmentName": "development",
		"NamespaceName":   "default",
	}
	for field, got := range checks {
		if got != wants[field] {
			t.Errorf("%s = %q, want %q", field, got, wants[field])
		}
	}
}

func TestParseEventEntry_MissingFields(t *testing.T) {
	// A non-string or absent field must not panic and must yield empty values.
	entry := parseEventEntry(0, map[string]interface{}{"severity": 123, "body": "only body"})
	if entry.Message != "only body" {
		t.Errorf("Message = %q, want %q", entry.Message, "only body")
	}
	if entry.Type != "" {
		t.Errorf("Type = %q, want empty for non-string", entry.Type)
	}
}

// eventsMockServer returns a test server that answers the search query with the
// given hits and any count query with the given total.
func eventsMockServer(t *testing.T, hits []map[string]interface{}, total int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if isCountQuery(r) {
			_ = json.NewEncoder(w).Encode(OpenObserveResponse{
				Took: 1,
				Hits: []map[string]interface{}{{"total": float64(total)}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(OpenObserveResponse{Took: 7, Total: len(hits), Hits: hits})
	}))
}

func TestGetComponentEvents(t *testing.T) {
	hits := []map[string]interface{}{
		{
			"_timestamp":       float64(evStart.UnixMicro()),
			"body":             "Job completed",
			"severity":         "Normal",
			"k8s_event_reason": "Completed",
			"k8s_object_kind":  "Job",
		},
	}
	server := eventsMockServer(t, hits, 42)
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetComponentEvents(context.Background(), EventsQueryParams{
		Namespace: "default",
		StartTime: evStart,
		EndTime:   evEnd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
	if result.Events[0].Message != "Job completed" || result.Events[0].Type != "Normal" {
		t.Errorf("unexpected parsed event: %+v", result.Events[0])
	}
	if result.TotalCount != 42 {
		t.Errorf("expected total 42 from count query, got %d", result.TotalCount)
	}
	if result.Took != 7 {
		t.Errorf("expected took 7, got %d", result.Took)
	}
}

func TestGetWorkflowEvents(t *testing.T) {
	hits := []map[string]interface{}{
		{
			"_timestamp":      float64(evStart.UnixMicro()),
			"body":            "Created pod",
			"severity":        "Normal",
			"k8s_object_name": "build-123-abc",
		},
	}
	server := eventsMockServer(t, hits, 3)
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetWorkflowEvents(context.Background(), WorkflowEventsQueryParams{
		Namespace:       "default",
		WorkflowRunName: "build-123",
		StartTime:       evStart,
		EndTime:         evEnd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Events) != 1 || result.TotalCount != 3 {
		t.Errorf("unexpected result: events=%d total=%d", len(result.Events), result.TotalCount)
	}
}

func TestGetComponentEvents_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	if _, err := client.GetComponentEvents(context.Background(), EventsQueryParams{
		Namespace: "default",
		StartTime: evStart,
		EndTime:   evEnd,
	}); err == nil {
		t.Fatal("expected error on server failure")
	}
}
