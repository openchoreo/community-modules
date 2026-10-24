// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/openobserve"
)

const testLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

// recordingServer is a mock OpenObserve that records request bodies and answers
// search, alert list, create and update calls.
func recordingServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_search"):
			_ = json.NewEncoder(w).Encode(openobserve.OpenObserveResponse{Took: 1})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/default/alerts":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"list": []map[string]string{{"alert_id": "alert-1", "name": "rule"}},
			})
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "alert-1"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(server.Close)
	return server, func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(bodies, "\n")
	}
}

func lineCases() map[string]*string {
	line := testLineUID
	return map[string]*string{"with a line": &line, "without a line": nil}
}

func wantLineHeader(t *testing.T, line *string, got, sent string) {
	t.Helper()
	want := ""
	if line != nil {
		want = "true"
		if !strings.Contains(sent, "release_line_uid = '"+testLineUID+"'") {
			t.Errorf("the release line did not reach the OpenObserve query: %s", sent)
		}
	} else if strings.Contains(sent, "release_line_uid") {
		t.Errorf("query without a release line filters on one: %s", sent)
	}
	if got != want {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, want)
	}
}

func TestQueryLogs_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases() {
		t.Run(name, func(t *testing.T) {
			server, sent := recordingServer(t)
			client := openobserve.NewClient(server.URL, "default", "default", "k8s_events", "admin", "pass", testLogger())
			handler := NewLogsHandler(client, nil, testLogger())

			scope := gen.LogsQueryRequest_SearchScope{}
			_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: ptr("comp-uid"), ReleaseLineUid: line,
			})
			resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &gen.LogsQueryRequest{
				StartTime:   time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
				EndTime:     time.Date(2025, 6, 15, 1, 0, 0, 0, time.UTC),
				SearchScope: scope,
			}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.QueryLogs200JSONResponse)
			if !is {
				t.Fatalf("expected 200 response, got %T", resp)
			}
			wantLineHeader(t, line, ok.Headers.OpenChoreoReleaseLineApplied, sent())
		})
	}
}

func TestQueryLogs_WorkflowScopeNoReleaseLineHeader(t *testing.T) {
	server, _ := recordingServer(t)
	client := openobserve.NewClient(server.URL, "default", "default", "k8s_events", "admin", "pass", testLogger())
	handler := NewLogsHandler(client, nil, testLogger())

	scope := gen.LogsQueryRequest_SearchScope{}
	_ = scope.FromWorkflowSearchScope(gen.WorkflowSearchScope{Namespace: "default", WorkflowRunName: ptr("run-1")})
	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &gen.LogsQueryRequest{
		StartTime:   time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2025, 6, 15, 1, 0, 0, 0, time.UTC),
		SearchScope: scope,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.QueryLogs200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty", got)
	}
}

func TestQueryEvents_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases() {
		t.Run(name, func(t *testing.T) {
			server, sent := recordingServer(t)
			client := openobserve.NewClient(server.URL, "default", "default", "k8s_events", "admin", "pass", testLogger())
			handler := NewLogsHandler(client, nil, testLogger())

			scope := gen.EventsQueryRequest_SearchScope{}
			_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: ptr("comp-uid"), ReleaseLineUid: line,
			})
			resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &gen.EventsQueryRequest{
				StartTime:   time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
				EndTime:     time.Date(2025, 6, 15, 1, 0, 0, 0, time.UTC),
				SearchScope: &scope,
			}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.QueryEvents200JSONResponse)
			if !is {
				t.Fatalf("expected 200 response, got %T", resp)
			}
			wantLineHeader(t, line, ok.Headers.OpenChoreoReleaseLineApplied, sent())
		})
	}
}

func alertRuleRequest(line *string) *gen.AlertRuleRequest {
	req := &gen.AlertRuleRequest{}
	req.Metadata.Name = "rule"
	req.Metadata.Namespace = "ns-1"
	if line != nil {
		uid, _ := parseUUID(*line)
		req.Metadata.ReleaseLineUid = &uid
	}
	req.Source.Query = "error"
	req.Condition.Enabled = true
	req.Condition.Operator = gen.AlertRuleRequestConditionOperator("gt")
	req.Condition.Threshold = 1
	req.Condition.Window = "5m"
	req.Condition.Interval = "1m"
	return req
}

func TestCreateAlertRule_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases() {
		t.Run(name, func(t *testing.T) {
			server, sent := recordingServer(t)
			client := openobserve.NewClient(server.URL, "default", "default", "k8s_events", "admin", "pass", testLogger())
			handler := NewLogsHandler(client, nil, testLogger())

			resp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: alertRuleRequest(line)})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.CreateAlertRule201JSONResponse)
			if !is {
				t.Fatalf("expected 201 response, got %T", resp)
			}
			wantLineHeader(t, line, ok.Headers.OpenChoreoReleaseLineApplied, sent())
		})
	}
}

func TestUpdateAlertRule_ReleaseLineHeader(t *testing.T) {
	for name, line := range lineCases() {
		t.Run(name, func(t *testing.T) {
			server, sent := recordingServer(t)
			client := openobserve.NewClient(server.URL, "default", "default", "k8s_events", "admin", "pass", testLogger())
			handler := NewLogsHandler(client, nil, testLogger())

			resp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
				RuleName: "rule", Body: alertRuleRequest(line),
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ok, is := resp.(gen.UpdateAlertRule200JSONResponse)
			if !is {
				t.Fatalf("expected 200 response, got %T", resp)
			}
			wantLineHeader(t, line, ok.Headers.OpenChoreoReleaseLineApplied, sent())
		})
	}
}
