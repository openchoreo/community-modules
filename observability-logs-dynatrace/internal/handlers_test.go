// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/dynatrace"
)

// fakeBackend records the parameters each call received and answers with canned results.
type fakeBackend struct {
	err error

	componentParams dynatrace.ComponentLogsParams
	workflowParams  dynatrace.WorkflowLogsParams
	eventsParams    dynatrace.EventsQueryParams
	platformParams  dynatrace.PlatformLogsParams
	auditParams     dynatrace.AuditLogsParams
	valueField      string
	auditFilter     string
	maxValues       int
	timeline        string
}

func (f *fakeBackend) GetComponentLogs(_ context.Context, p dynatrace.ComponentLogsParams) (*dynatrace.ComponentLogsResult, error) {
	f.componentParams = p
	if f.err != nil {
		return nil, f.err
	}
	return &dynatrace.ComponentLogsResult{
		Logs: []dynatrace.ComponentLogEntry{{
			Timestamp: time.Unix(1, 0).UTC(), Log: "ERROR boom", LogLevel: "ERROR",
			ComponentUID: "8d7b5a4e-3f33-4c5e-9d1a-2b3c4d5e6f70", ComponentName: "api", ProjectUID: "not-a-uuid",
		}},
		TotalCount: 1, Took: 3,
	}, nil
}

func (f *fakeBackend) GetWorkflowLogs(_ context.Context, p dynatrace.WorkflowLogsParams) (*dynatrace.WorkflowLogsResult, error) {
	f.workflowParams = p
	if f.err != nil {
		return nil, f.err
	}
	return &dynatrace.WorkflowLogsResult{Logs: []dynatrace.WorkflowLogEntry{{Timestamp: time.Unix(1, 0).UTC(), Log: "step"}}, TotalCount: 1}, nil
}

func (f *fakeBackend) GetEvents(_ context.Context, p dynatrace.EventsQueryParams) (*dynatrace.EventsResult, error) {
	f.eventsParams = p
	if f.err != nil {
		return nil, f.err
	}
	return &dynatrace.EventsResult{Events: []dynatrace.EventEntry{{Timestamp: time.Unix(1, 0).UTC(), Reason: "Started", Type: "Normal"}}, TotalCount: 1}, nil
}

func (f *fakeBackend) GetPlatformLogs(_ context.Context, p dynatrace.PlatformLogsParams) (*dynatrace.PlatformLogsResult, error) {
	f.platformParams = p
	if f.err != nil {
		return nil, f.err
	}
	return &dynatrace.PlatformLogsResult{Logs: []dynatrace.PlatformLogEntry{{
		Timestamp: time.Unix(1, 0).UTC(), Log: "hi", LogLevel: "INFO", Labels: map[string]string{"app": "x"},
	}}, TotalCount: 1}, nil
}

func (f *fakeBackend) GetPlatformLogFilterValues(_ context.Context, p dynatrace.PlatformLogsParams, valueField, _ string, maxValues int) (*dynatrace.FilterValues, error) {
	f.platformParams, f.valueField, f.maxValues = p, valueField, maxValues
	if f.err != nil {
		return nil, f.err
	}
	return &dynatrace.FilterValues{Values: []dynatrace.FilterValue{{Value: "ns", Count: 2}}, TotalValues: 1}, nil
}

func (f *fakeBackend) GetAuditLogs(_ context.Context, p dynatrace.AuditLogsParams, timeline string) (*dynatrace.AuditLogsResult, error) {
	f.auditParams, f.timeline = p, timeline
	if f.err != nil {
		return nil, f.err
	}
	return &dynatrace.AuditLogsResult{
		Records: []dynatrace.AuditRecord{{
			SchemaVersion: "1.0", EventID: "e1", EventTime: time.Unix(1, 0).UTC(),
			Actor:     dynatrace.AuditActor{Type: "user", ID: "alice", Entitlements: map[string][]string{"groups": {"a"}}},
			Result:    "success",
			HTTP:      &dynatrace.AuditHTTPInfo{Method: "GET"},
			Resource:  &dynatrace.AuditResource{Name: "shop", Metadata: map[string]any{"k": "v"}},
			Metadata:  map[string]any{"x": 1},
			Collector: dynatrace.AuditCollectorInfo{PodName: "api-0"},
		}},
		Total: 1,
		Timeline: &dynatrace.AuditTimeline{Interval: "1h", Buckets: []dynatrace.AuditTimelineBucket{
			{StartTime: time.Unix(0, 0).UTC(), Total: 1, Counts: map[string]int64{"success": 1}},
		}},
	}, nil
}

func (f *fakeBackend) GetAuditLogFilterValues(_ context.Context, p dynatrace.AuditLogsParams, filter, _ string, maxValues int) (*dynatrace.FilterValues, error) {
	f.auditParams, f.auditFilter, f.maxValues = p, filter, maxValues
	if f.err != nil {
		return nil, f.err
	}
	return &dynatrace.FilterValues{Values: []dynatrace.FilterValue{{Value: "alice", Count: 1}}, TotalValues: 1}, nil
}

func newTestServer(t *testing.T, backend *fakeBackend) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer("0", 30*time.Second, NewLogsHandler(backend, logger), logger).Handler()
}

func TestWriteTimeoutCoversSequentialQueries(t *testing.T) {
	for _, q := range []time.Duration{30 * time.Second, 2 * time.Minute} {
		if got := NewServer("0", q, nil, nil).httpServer.WriteTimeout; got <= 2*q {
			t.Errorf("query timeout %s: write timeout %s does not cover two queries", q, got)
		}
	}
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

const window = `"startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T01:00:00Z"`

func TestHealth(t *testing.T) {
	code, body := do(t, newTestServer(t, &fakeBackend{}), http.MethodGet, "/health", "")
	if code != http.StatusOK || body["status"] != "healthy" {
		t.Errorf("health: %d %v", code, body)
	}
}

func TestQueryComponentLogs(t *testing.T) {
	b := &fakeBackend{}
	code, body := do(t, newTestServer(t, b), http.MethodPost, "/api/v1/logs/query",
		`{`+window+`,"limit":20,"sortOrder":"asc","logLevels":["ERROR"],"searchPhrase":"boom",`+
			`"searchScope":{"namespace":"default","projectUid":"p","environmentUid":"e","componentUid":"c"}}`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, body)
	}
	p := b.componentParams
	if p.Namespace != "default" || p.ProjectID != "p" || p.EnvironmentID != "e" || p.ComponentID != "c" ||
		p.Limit != 20 || p.SortOrder != "asc" || p.SearchPhrase != "boom" || len(p.LogLevels) != 1 {
		t.Errorf("unexpected params %+v", p)
	}
	logs := body["logs"].([]any)
	entry := logs[0].(map[string]any)
	meta := entry["metadata"].(map[string]any)
	if entry["level"] != "ERROR" || meta["componentUid"] == nil || meta["projectUid"] != nil {
		t.Errorf("unexpected entry %v", entry)
	}
}

func TestQueryWorkflowLogs(t *testing.T) {
	b := &fakeBackend{}
	code, body := do(t, newTestServer(t, b), http.MethodPost, "/api/v1/logs/query",
		`{`+window+`,"searchScope":{"namespace":"default","workflowRunName":"run-1"}}`)
	if code != http.StatusOK || b.workflowParams.WorkflowRunName != "run-1" {
		t.Fatalf("status %d: %v %+v", code, body, b.workflowParams)
	}
	if len(body["logs"].([]any)) != 1 {
		t.Errorf("unexpected body %v", body)
	}
}

func TestQueryLogsBadRequests(t *testing.T) {
	h := newTestServer(t, &fakeBackend{})
	for name, body := range map[string]string{
		"no namespace":          `{` + window + `,"searchScope":{"namespace":""}}`,
		"workflow no namespace": `{` + window + `,"searchScope":{"namespace":" ","workflowRunName":"r"}}`,
		"workflow no run":       `{` + window + `,"searchScope":{"namespace":"d","workflowRunName":" "}}`,
		"inverted window":       `{"startTime":"2026-01-01T01:00:00Z","endTime":"2026-01-01T00:00:00Z","searchScope":{"namespace":"d"}}`,
	} {
		if code, _ := do(t, h, http.MethodPost, "/api/v1/logs/query", body); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, code)
		}
	}
}

func TestQueryLogsBackendError(t *testing.T) {
	h := newTestServer(t, &fakeBackend{err: errors.New("boom")})
	for _, body := range []string{
		`{` + window + `,"searchScope":{"namespace":"d"}}`,
		`{` + window + `,"searchScope":{"namespace":"d","workflowRunName":"r"}}`,
	} {
		code, resp := do(t, h, http.MethodPost, "/api/v1/logs/query", body)
		if code != http.StatusInternalServerError || strings.Contains(resp["message"].(string), "boom") {
			t.Errorf("status %d body %v", code, resp)
		}
	}
}

func TestQueryEventsScopes(t *testing.T) {
	b := &fakeBackend{}
	h := newTestServer(t, b)

	code, body := do(t, h, http.MethodPost, "/api/v1/events/query", `{`+window+`,"searchScope":{"namespace":"d","componentUid":"c"}}`)
	if code != http.StatusOK || b.eventsParams.Component == nil || b.eventsParams.Component.ComponentID != "c" {
		t.Fatalf("component scope: %d %v %+v", code, body, b.eventsParams)
	}
	if body["total"].(float64) != 1 {
		t.Errorf("unexpected body %v", body)
	}

	code, _ = do(t, h, http.MethodPost, "/api/v1/events/query", `{`+window+`,"searchScope":{"namespace":"d","workflowRunName":"r","taskName":"t"}}`)
	if code != http.StatusOK || b.eventsParams.Workflow == nil || b.eventsParams.Workflow.TaskName != "t" {
		t.Fatalf("workflow scope: %d %+v", code, b.eventsParams)
	}

	code, _ = do(t, h, http.MethodPost, "/api/v1/events/query", `{`+window+`,"reasons":["DeploymentSucceeded"],"sortOrder":"asc"}`)
	if code != http.StatusOK || b.eventsParams.Component != nil || b.eventsParams.Workflow != nil || len(b.eventsParams.Reasons) != 1 {
		t.Fatalf("unscoped sweep: %d %+v", code, b.eventsParams)
	}
}

func TestQueryEventsBadRequests(t *testing.T) {
	h := newTestServer(t, &fakeBackend{})
	for name, body := range map[string]string{
		"unscoped without reasons": `{` + window + `}`,
		"no namespace":             `{` + window + `,"searchScope":{"namespace":""}}`,
		"workflow no run":          `{` + window + `,"searchScope":{"namespace":"d","workflowRunName":""}}`,
		"inverted window":          `{"startTime":"2026-01-01T01:00:00Z","endTime":"2026-01-01T00:00:00Z","reasons":["x"]}`,
	} {
		if code, _ := do(t, h, http.MethodPost, "/api/v1/events/query", body); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, code)
		}
	}
	if code, _ := do(t, newTestServer(t, &fakeBackend{err: errors.New("x")}), http.MethodPost, "/api/v1/events/query",
		`{`+window+`,"reasons":["x"]}`); code != http.StatusInternalServerError {
		t.Errorf("backend error: status %d", code)
	}
}

func TestAlertEndpointsNotImplemented(t *testing.T) {
	h := newTestServer(t, &fakeBackend{})
	rule := `{"metadata":{"name":"r","namespace":"n","projectUid":"8d7b5a4e-3f33-4c5e-9d1a-2b3c4d5e6f70",` +
		`"environmentUid":"8d7b5a4e-3f33-4c5e-9d1a-2b3c4d5e6f70","componentUid":"8d7b5a4e-3f33-4c5e-9d1a-2b3c4d5e6f70"},` +
		`"source":{"query":"x"},"condition":{"enabled":true,"window":"5m","interval":"1m","operator":"gt","threshold":1}}`
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1alpha1/alerts/rules", rule},
		{http.MethodGet, "/api/v1alpha1/alerts/rules/r", ""},
		{http.MethodPut, "/api/v1alpha1/alerts/rules/r", rule},
		{http.MethodDelete, "/api/v1alpha1/alerts/rules/r", ""},
		{http.MethodPost, "/api/v1alpha1/alerts/webhook", `{}`},
	} {
		code, body := do(t, h, c.method, c.path, c.body)
		if code != http.StatusNotImplemented || body["title"] != "notImplemented" {
			t.Errorf("%s %s: %d %v", c.method, c.path, code, body)
		}
	}
}

func TestQueryPlatformLogs(t *testing.T) {
	b := &fakeBackend{}
	h := newTestServer(t, b)
	code, body := do(t, h, http.MethodPost, "/api/v1alpha1/platform-logs/query",
		`{`+window+`,"namespace":["a"],"labels":{"app":"x"},"logLevels":["WARN"]}`)
	if code != http.StatusOK || b.platformParams.Labels["app"] != "x" || b.platformParams.Namespaces[0] != "a" {
		t.Fatalf("status %d: %v %+v", code, body, b.platformParams)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/v1alpha1/platform-logs/query", `{`+window+`,"labels":{"a=b":"x"}}`); code != http.StatusBadRequest {
		t.Errorf("invalid label key: status %d", code)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/v1alpha1/platform-logs/query",
		`{"startTime":"2026-01-01T01:00:00Z","endTime":"2026-01-01T00:00:00Z"}`); code != http.StatusBadRequest {
		t.Errorf("inverted window: status %d", code)
	}
}

func TestQueryPlatformLogFilterValues(t *testing.T) {
	b := &fakeBackend{}
	h := newTestServer(t, b)
	code, body := do(t, h, http.MethodPost, "/api/v1alpha1/platform-logs/filter-values",
		`{"query":{`+window+`,"namespace":["a"],"podName":["p"]},"filter":"namespace","maxValues":5000}`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, body)
	}
	if b.platformParams.Namespaces != nil || len(b.platformParams.PodNames) != 1 {
		t.Errorf("own filter selections should be cleared: %+v", b.platformParams)
	}
	if b.valueField != "k8s.namespace.name" || b.maxValues != dynatrace.MaxMaxFilterValues {
		t.Errorf("field %q maxValues %d", b.valueField, b.maxValues)
	}
	if body["filter"] != "namespace" {
		t.Errorf("filter not echoed: %v", body)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/v1alpha1/platform-logs/filter-values",
		`{"query":{"startTime":"2026-01-01T01:00:00Z","endTime":"2026-01-01T00:00:00Z"},"filter":"namespace"}`); code != http.StatusBadRequest {
		t.Errorf("inverted window: status %d", code)
	}
}

func TestQueryAuditLogs(t *testing.T) {
	b := &fakeBackend{}
	h := newTestServer(t, b)
	code, body := do(t, h, http.MethodPost, "/api/v1alpha1/audit-logs/query",
		`{`+window+`,"includeTimeline":true,"timelineInterval":"15m","actor":{"id":["alice"]},"result":["denied"],"limit":5000}`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, body)
	}
	if b.timeline != "15m" || b.auditParams.ActorIDs[0] != "alice" || b.auditParams.Limit != 1000 {
		t.Errorf("unexpected params %+v timeline %q", b.auditParams, b.timeline)
	}
	record := body["records"].([]any)[0].(map[string]any)
	if record["collector"] == nil || record["http"] == nil || body["timeline"] == nil {
		t.Errorf("unexpected record %v", record)
	}

	for name, req := range map[string]string{
		"bad category":    `{` + window + `,"category":["nope"]}`,
		"bad interval":    `{` + window + `,"includeTimeline":true,"timelineInterval":"5s"}`,
		"inverted window": `{"startTime":"2026-01-01T01:00:00Z","endTime":"2026-01-01T00:00:00Z"}`,
	} {
		if code, _ := do(t, h, http.MethodPost, "/api/v1alpha1/audit-logs/query", req); code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, code)
		}
	}
}

func TestQueryAuditLogFilterValues(t *testing.T) {
	b := &fakeBackend{}
	h := newTestServer(t, b)
	code, body := do(t, h, http.MethodPost, "/api/v1alpha1/audit-logs/filter-values",
		`{"query":{`+window+`,"actor":{"id":["alice"],"type":["user"]}},"filter":"actor.id"}`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, body)
	}
	if b.auditParams.ActorIDs != nil || len(b.auditParams.ActorTypes) != 1 || b.auditFilter != "actor.id" {
		t.Errorf("unexpected params %+v", b.auditParams)
	}
	if code, _ := do(t, newTestServer(t, &fakeBackend{err: errors.New("x")}), http.MethodPost, "/api/v1alpha1/audit-logs/filter-values",
		`{"query":{`+window+`},"filter":"action"}`); code != http.StatusInternalServerError {
		t.Errorf("backend error: status %d", code)
	}
}
