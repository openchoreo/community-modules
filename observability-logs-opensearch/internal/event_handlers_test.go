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
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
}

func TestQueryEvents_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, nil, nil, testLogger())
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryEvents400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryEvents_ComponentScope_EmptyNamespace(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, nil, nil, testLogger())

	searchScope := gen.EventsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{Namespace: ""})

	body := gen.EventsQueryRequest{
		StartTime:   time.Now(),
		EndTime:     time.Now(),
		SearchScope: &searchScope,
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
	handler := NewLogsHandler(osClient, nil, eqb, nil, nil, testLogger())

	componentUID := "a022c8af-78c8-4fa2-a9fd-51eb8579ecb2"
	searchScope := gen.EventsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{
		Namespace:    "default",
		ComponentUid: &componentUID,
	})

	body := gen.EventsQueryRequest{
		StartTime:   time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		SearchScope: &searchScope,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	queryResp, ok := resp.(gen.QueryEvents200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if queryResp.Body.Total != 1 {
		t.Fatalf("expected total=1, got %d", queryResp.Body.Total)
	}
	if queryResp.Body.Events == nil || len(*queryResp.Body.Events) != 1 {
		t.Fatalf("expected 1 event, got %v", queryResp.Body.Events)
	}
	ev := (*queryResp.Body.Events)[0]
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
	handler := NewLogsHandler(osClient, nil, eqb, nil, nil, testLogger())

	workflowRunName := "build-run-123"
	searchScope := gen.EventsQueryRequest_SearchScope{}
	_ = searchScope.FromWorkflowSearchScope(gen.WorkflowSearchScope{
		Namespace:       "default",
		WorkflowRunName: &workflowRunName,
	})

	body := gen.EventsQueryRequest{
		StartTime:   time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		SearchScope: &searchScope,
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
	handler := NewLogsHandler(osClient, nil, eqb, nil, nil, testLogger())

	searchScope := gen.EventsQueryRequest_SearchScope{}
	_ = searchScope.FromComponentSearchScope(gen.ComponentSearchScope{Namespace: "default"})

	body := gen.EventsQueryRequest{
		StartTime:   time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		SearchScope: &searchScope,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryEvents500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestQueryEvents_NoScope_NoReasons(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, nil, nil, testLogger())

	body := gen.EventsQueryRequest{
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryEvents400JSONResponse); !ok {
		t.Fatalf("expected 400 response when neither searchScope nor reasons is set, got %T", resp)
	}
}

func TestQueryEvents_UnscopedReasonSweep_Success(t *testing.T) {
	server := eventsSearchServer(t)
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	eqb := osearch.NewQueryBuilder("k8s-events-")
	handler := NewLogsHandler(osClient, nil, eqb, nil, nil, testLogger())

	reasons := []string{"DeploymentSucceeded", "DeploymentFailed"}
	body := gen.EventsQueryRequest{
		StartTime: time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		Reasons:   &reasons,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	queryResp, ok := resp.(gen.QueryEvents200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if queryResp.Body.Events == nil || len(*queryResp.Body.Events) != 1 {
		t.Fatalf("expected 1 event from the unscoped sweep, got %v", queryResp.Body.Events)
	}
}

// eventsServerWithHits stands up a fake OpenSearch returning n delivery event hits.
func eventsServerWithHits(t *testing.T, n, total int) *httptest.Server {
	t.Helper()
	hits := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		hits = append(hits, map[string]interface{}{
			"_id":    fmt.Sprintf("ev-%d", i),
			"_score": 1.0,
			"_source": map[string]interface{}{
				"@timestamp": "2026-06-05T12:24:06Z",
				"body":       "Job completed",
				"attributes": map[string]interface{}{
					"k8s.event.reason": "DeploymentSucceeded",
				},
			},
		})
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]interface{}{
			"took":      1,
			"timed_out": false,
			"hits": map[string]interface{}{
				"total": map[string]interface{}{"value": total, "relation": "eq"},
				"hits":  hits,
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
}

// queryEventsAtLimit runs a reason-filtered sweep against server at the given limit.
func queryEventsAtLimit(t *testing.T, serverURL string, limit int) gen.QueryEvents200JSONResponse {
	t.Helper()
	osClient := newTestOSClient(t, serverURL)
	eqb := osearch.NewQueryBuilder("k8s-events-")
	handler := NewLogsHandler(osClient, nil, eqb, nil, nil, testLogger())

	reasons := []string{"DeploymentSucceeded"}
	body := gen.EventsQueryRequest{
		StartTime: time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		Reasons:   &reasons,
		Limit:     &limit,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	queryResp, ok := resp.(gen.QueryEvents200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	return queryResp
}

func TestQueryEvents_TotalExceedsPageWhenTruncated(t *testing.T) {
	// The caller reads completeness as total against the events returned, so a
	// window holding more than a page must report the larger figure rather than
	// the page size -- otherwise a truncated read looks fully drained.
	server := eventsServerWithHits(t, 1, 5)
	defer server.Close()

	queryResp := queryEventsAtLimit(t, server.URL, 1)
	if queryResp.Body.Events == nil || len(*queryResp.Body.Events) != 1 {
		t.Fatalf("expected 1 event, got %v", queryResp.Body.Events)
	}
	if queryResp.Body.Total != 5 {
		t.Errorf("expected total=5 for a truncated read, got %d", queryResp.Body.Total)
	}
}

func TestQueryEvents_TotalMatchesPageWhenWindowRead(t *testing.T) {
	// Equal is what tells the caller it may advance past the window.
	server := eventsServerWithHits(t, 1, 1)
	defer server.Close()

	queryResp := queryEventsAtLimit(t, server.URL, 10)
	if queryResp.Body.Events == nil || len(*queryResp.Body.Events) != 1 {
		t.Fatalf("expected 1 event, got %v", queryResp.Body.Events)
	}
	if queryResp.Body.Total != 1 {
		t.Errorf("expected total=1 for a fully read window, got %d", queryResp.Body.Total)
	}
}

// TestQueryEvents_DoesNotSplitATimestampGroup is the regression test for silent
// loss on resume. A caller resumes from the last timestamp it was given, so if a
// full page ends part-way through a group of events sharing that timestamp, the
// unreturned members of that group are never read by anyone: the next query
// starts at their timestamp and the caller has already moved past it.
//
// The adapter therefore has to finish the group before returning, even where
// that takes the page past limit.
func TestQueryEvents_DoesNotSplitATimestampGroup(t *testing.T) {
	const (
		earlier  = "2026-06-05T12:24:05Z"
		boundary = "2026-06-05T12:24:06Z"
	)
	// A group of three at the boundary, against limit=2: bigger than both the page
	// and the follow-up page, so the follow-up has to be paged to finish it.
	all := map[string]string{
		"ev-1": earlier,
		"ev-2": boundary,
		"ev-3": boundary,
		"ev-4": boundary,
	}
	order := []string{"ev-1", "ev-2", "ev-3", "ev-4"}

	server := eventsBodyAwareServer(t, all, order)
	defer server.Close()

	osClient := newTestOSClient(t, server.URL)
	eqb := osearch.NewQueryBuilder("k8s-events-")
	handler := NewLogsHandler(osClient, nil, eqb, nil, nil, testLogger())

	reasons := []string{"DeploymentSucceeded"}
	limit := 2
	body := gen.EventsQueryRequest{
		StartTime: time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		Reasons:   &reasons,
		Limit:     &limit,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	queryResp, ok := resp.(gen.QueryEvents200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if queryResp.Body.Events == nil {
		t.Fatal("expected events")
	}
	got := *queryResp.Body.Events
	// The whole group comes back, not the two that fit the page.
	if len(got) != 4 {
		t.Fatalf("expected the boundary group finished (4 events), got %d", len(got))
	}
	// And total describes the page that was actually returned, so the caller reads
	// the window as fully read rather than re-sweeping it forever.
	if queryResp.Body.Total != len(got) {
		t.Fatalf("expected total=%d to match the events returned, got %d", len(got), queryResp.Body.Total)
	}
}

// eventsBodyAwareServer stands up a fake OpenSearch that honours the parts of the
// request body this adapter relies on: the @timestamp range, size, from and
// track_total_hits. The earlier mock ignored the body and so returned whole
// groups however small a page was asked for, which hid a follow-up query that
// could itself be truncated.
func eventsBodyAwareServer(t *testing.T, at map[string]string, order []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode search body: %v", err)
		}

		gte, lte := eventsRangeBounds(req)
		matched := make([]string, 0, len(order))
		for _, id := range order {
			ts := at[id]
			if gte != "" && ts < gte {
				continue
			}
			if lte != "" && ts > lte {
				continue
			}
			matched = append(matched, id)
		}

		size := intField(req, "size", 10)
		from := intField(req, "from", 0)
		page := matched
		if from < len(page) {
			page = page[from:]
		} else {
			page = nil
		}
		if size >= 0 && len(page) > size {
			page = page[:size]
		}

		// track_total_hits caps the count, as OpenSearch does.
		total := len(matched)
		if cap, ok := req["track_total_hits"].(float64); ok && total > int(cap) {
			total = int(cap)
		}

		hits := make([]map[string]interface{}, 0, len(page))
		for _, id := range page {
			hits = append(hits, map[string]interface{}{
				"_id":    id,
				"_score": 1.0,
				"_source": map[string]interface{}{
					"@timestamp": at[id],
					"body":       "Job completed",
					"attributes": map[string]interface{}{"k8s.event.reason": "DeploymentSucceeded"},
				},
			})
		}

		w.Header().Set("Content-Type", "application/json")
		out := map[string]interface{}{
			"took":      1,
			"timed_out": false,
			"hits": map[string]interface{}{
				"total": map[string]interface{}{"value": total, "relation": "eq"},
				"hits":  hits,
			},
		}
		if err := json.NewEncoder(w).Encode(out); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}))
}

// eventsRangeBounds digs the @timestamp bounds out of a query body. The adapter
// writes gte/lt for a window and gte/lte for a single timestamp group.
func eventsRangeBounds(req map[string]interface{}) (string, string) {
	query, _ := req["query"].(map[string]interface{})
	boolQ, _ := query["bool"].(map[string]interface{})
	must, _ := boolQ["must"].([]interface{})
	for _, cond := range must {
		m, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}
		rng, ok := m["range"].(map[string]interface{})
		if !ok {
			continue
		}
		bounds, ok := rng["@timestamp"].(map[string]interface{})
		if !ok {
			continue
		}
		gte, _ := bounds["gte"].(string)
		if lte, ok := bounds["lte"].(string); ok {
			return gte, lte
		}
		// A window uses an exclusive upper bound; trim it so string comparison
		// keeps it exclusive.
		if lt, ok := bounds["lt"].(string); ok {
			return gte, lt[:len(lt)-1] + string(rune(lt[len(lt)-1]-1))
		}
		return gte, ""
	}
	return "", ""
}

func intField(req map[string]interface{}, key string, def int) int {
	if v, ok := req[key].(float64); ok {
		return int(v)
	}
	return def
}
