// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
	osearch "github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

// eventsSearchServer returns a test server that responds with a single enriched event hit.
func eventsSearchServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"took":      3,
			"timed_out": false,
			"hits": map[string]interface{}{
				"total": map[string]interface{}{"value": 1, "relation": "eq"},
				"hits": []map[string]interface{}{
					{
						"_id":    "ev-1",
						"_score": 1.0,
						"_source": map[string]interface{}{
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
								"k8s.object.label.openchoreo.dev/component":     "github-issue-reporter",
								"k8s.object.label.openchoreo.dev/component-uid": "a022c8af-78c8-4fa2-a9fd-51eb8579ecb2",
								"k8s.object.label.openchoreo.dev/namespace":     "default",
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestQueryEvents_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, nil, testLogger())
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryEvents400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryEvents_ComponentScope_EmptyNamespace(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, nil, testLogger())

	searchScope := gen.EventsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{Namespace: ""})

	body := gen.EventsQueryRequest{
		StartTime:   time.Now(),
		EndTime:     time.Now(),
		SearchScope: searchScope,
	}
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryEvents400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryEvents_ComponentScope_Success(t *testing.T) {
	server := eventsSearchServer(t)
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	eqb := osearch.NewQueryBuilder("k8s-events-")
	handler := NewLogsHandler(osClient, nil, eqb, nil, testLogger())

	componentUID := "a022c8af-78c8-4fa2-a9fd-51eb8579ecb2"
	searchScope := gen.EventsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{
		Namespace:    "default",
		ComponentUid: &componentUID,
	})

	body := gen.EventsQueryRequest{
		StartTime:   time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		SearchScope: searchScope,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	queryResp, ok := resp.(gen.QueryEvents200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if queryResp.Total == nil || *queryResp.Total != 1 {
		t.Fatalf("expected total=1, got %v", queryResp.Total)
	}
	if queryResp.Events == nil || len(*queryResp.Events) != 1 {
		t.Fatalf("expected 1 event, got %v", queryResp.Events)
	}
	ev := (*queryResp.Events)[0]
	if ev.Reason == nil || *ev.Reason != "Completed" {
		t.Errorf("expected reason 'Completed', got %v", ev.Reason)
	}
	if ev.Message == nil || *ev.Message != "Job completed" {
		t.Errorf("expected message 'Job completed', got %v", ev.Message)
	}
	if ev.Type == nil || *ev.Type != "Normal" {
		t.Errorf("expected type 'Normal', got %v", ev.Type)
	}
	if ev.Metadata == nil || ev.Metadata.ObjectKind == nil || *ev.Metadata.ObjectKind != "Job" {
		t.Errorf("expected objectKind 'Job', got %v", ev.Metadata)
	}
	if ev.Metadata.ComponentName == nil || *ev.Metadata.ComponentName != "github-issue-reporter" {
		t.Errorf("expected componentName, got %v", ev.Metadata.ComponentName)
	}
	if ev.Metadata.ComponentUid == nil || ev.Metadata.ComponentUid.String() != componentUID {
		t.Errorf("expected componentUid %s, got %v", componentUID, ev.Metadata.ComponentUid)
	}
}

func TestQueryEvents_WorkflowScope_Success(t *testing.T) {
	server := eventsSearchServer(t)
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	eqb := osearch.NewQueryBuilder("k8s-events-")
	handler := NewLogsHandler(osClient, nil, eqb, nil, testLogger())

	workflowRunName := "build-run-123"
	searchScope := gen.EventsQueryRequest_SearchScope{}
	_ = searchScope.FromWorkflowSearchScope(gen.WorkflowSearchScope{
		Namespace:       "default",
		WorkflowRunName: &workflowRunName,
	})

	body := gen.EventsQueryRequest{
		StartTime:   time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		SearchScope: searchScope,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryEvents200JSONResponse); !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
}

func TestQueryEvents_SearchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"search failed"}`)
	}))
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	eqb := osearch.NewQueryBuilder("k8s-events-")
	handler := NewLogsHandler(osClient, nil, eqb, nil, testLogger())

	searchScope := gen.EventsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{Namespace: "default"})

	body := gen.EventsQueryRequest{
		StartTime:   time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		SearchScope: searchScope,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryEvents500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}
