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

const (
	testLineUID      = "93f65294-926f-475b-98f1-dc4f152f38da"
	testComponentUID = "d2108e2c-f912-4cab-a0b4-71afde25c5ac"
)

// releaseLineColumns are the stream columns once a release line's pods have logged.
var releaseLineColumns = []string{
	"kubernetes_labels_openchoreo_dev_component_uid",
	"kubernetes_labels_openchoreo_dev_release_line_uid",
	"kubernetes_labels_openchoreo_dev_release_line_component_uid",
	"k8s_object_label_openchoreo_dev_component_uid",
	"k8s_object_label_openchoreo_dev_release_line_uid",
	"k8s_object_label_openchoreo_dev_release_line_component_uid",
}

// lineServer fakes OpenObserve's stream schema, search and alert endpoints. It records the
// request bodies it receives and serves back the last alert written.
func lineServer(t *testing.T) (*LogsHandler, func() string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	var alert []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/schema"):
			fields := make([]map[string]string, 0, len(releaseLineColumns))
			for _, c := range releaseLineColumns {
				fields = append(fields, map[string]string{"name": c, "type": "Utf8"})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"schema": fields})
		case strings.HasSuffix(r.URL.Path, "/_search"):
			_ = json.NewEncoder(w).Encode(openobserve.OpenObserveResponse{Hits: []map[string]interface{}{}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/default/alerts":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"list": []map[string]string{{"alert_id": "alert-1", "name": "rule"}},
			})
		case r.Method == http.MethodGet:
			_, _ = w.Write(alert)
		case r.Method == http.MethodPost:
			alert = b
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "alert-1"})
		default:
			alert = b
		}
	}))
	t.Cleanup(server.Close)
	client := openobserve.NewClient(server.URL, "default", "default", "k8s_events", "admin", "pass", testLogger())
	return NewLogsHandler(client, nil, testLogger()), func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(bodies, "\n")
	}
}

var lineScopes = map[string]struct {
	line     *string
	allLines *bool
	want     string
}{
	"one line":   {line: ptr(testLineUID), want: "release_line_uid = '" + testLineUID + "'"},
	"every line": {allLines: ptr(true), want: "release_line_component_uid = '" + testComponentUID + "'"},
	"main line":  {want: "release_line_uid IS NULL"},
}

func TestQueryLogs_ReleaseLineHeader(t *testing.T) {
	for name, tt := range lineScopes {
		t.Run(name, func(t *testing.T) {
			handler, sent := lineServer(t)
			scope := gen.LogsQueryRequest_SearchScope{}
			_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: ptr(testComponentUID), ReleaseLineUid: tt.line, AllReleaseLines: tt.allLines,
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
			if !strings.Contains(sent(), "kubernetes_labels_openchoreo_dev_"+tt.want) {
				t.Errorf("the line scope did not reach the OpenObserve query, want %s in %s", tt.want, sent())
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", got)
			}
		})
	}
}

func TestQueryLogs_WorkflowScopeNoReleaseLineHeader(t *testing.T) {
	handler, _ := lineServer(t)
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
	for name, tt := range lineScopes {
		t.Run(name, func(t *testing.T) {
			handler, sent := lineServer(t)
			scope := gen.EventsQueryRequest_SearchScope{}
			_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: ptr(testComponentUID), ReleaseLineUid: tt.line, AllReleaseLines: tt.allLines,
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
			if !strings.Contains(sent(), "k8s_object_label_openchoreo_dev_"+tt.want) {
				t.Errorf("the line scope did not reach the OpenObserve query, want %s in %s", tt.want, sent())
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", got)
			}
		})
	}
}

func alertRuleRequest(line *string) *gen.AlertRuleRequest {
	req := &gen.AlertRuleRequest{}
	req.Metadata.Name = "rule"
	req.Metadata.Namespace = "ns-1"
	req.Metadata.ComponentUid, _ = parseUUID(testComponentUID)
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

// A rule covers its release line, or its component's main line, and reads back naming its component.
func TestAlertRules_ReleaseLine(t *testing.T) {
	for name, tt := range map[string]struct {
		line    *string
		want    string
		notWant string
	}{
		"release line": {line: ptr(testLineUID), want: "release_line_uid = '" + testLineUID + "'", notWant: "component_uid"},
		"main line":    {want: "component_uid = '" + testComponentUID + "' AND kubernetes_labels_openchoreo_dev_release_line_uid IS NULL"},
	} {
		t.Run(name, func(t *testing.T) {
			handler, sent := lineServer(t)
			created, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: alertRuleRequest(tt.line)})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			createResp, ok := created.(gen.CreateAlertRule201JSONResponse)
			if !ok {
				t.Fatalf("expected 201 response, got %T", created)
			}
			updated, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
				RuleName: "rule", Body: alertRuleRequest(tt.line),
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			updateResp, ok := updated.(gen.UpdateAlertRule200JSONResponse)
			if !ok {
				t.Fatalf("expected 200 response, got %T", updated)
			}
			for kind, got := range map[string]string{
				"create": createResp.Headers.OpenChoreoReleaseLineApplied,
				"update": updateResp.Headers.OpenChoreoReleaseLineApplied,
			} {
				if got != "true" {
					t.Errorf("%s OpenChoreo-Release-Line-Applied = %q, want true", kind, got)
				}
			}

			var rules int
			for _, body := range strings.Split(sent(), "\n") {
				var rule struct {
					QueryCondition *struct {
						SQL string `json:"sql"`
					} `json:"query_condition"`
				}
				if json.Unmarshal([]byte(body), &rule) != nil || rule.QueryCondition == nil {
					continue
				}
				rules++
				if sql := rule.QueryCondition.SQL; !strings.Contains(sql, tt.want) || (tt.notWant != "" && strings.Contains(sql, tt.notWant)) {
					t.Errorf("rule SQL = %s, want %s", sql, tt.want)
				}
			}
			if rules != 2 {
				t.Errorf("expected the created and the updated rule, got %d", rules)
			}

			got, err := handler.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "rule"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			getResp, ok := got.(gen.GetAlertRule200JSONResponse)
			if !ok {
				t.Fatalf("expected 200 response, got %T", got)
			}
			if getResp.Metadata == nil || getResp.Metadata.ComponentUid == nil || getResp.Metadata.ComponentUid.String() != testComponentUID {
				t.Errorf("component UID = %v, want %s", getResp.Metadata, testComponentUID)
			}
			if getResp.Source == nil || getResp.Source.Query == nil || *getResp.Source.Query != "error" {
				t.Errorf("query = %v, want error", getResp.Source)
			}
		})
	}
}
