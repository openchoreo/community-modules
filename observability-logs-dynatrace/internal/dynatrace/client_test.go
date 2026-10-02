// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueryPollsUntilSucceeded(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case executePath:
			var req executeRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.DefaultTimeframeStart != "2026-01-01T00:00:00Z" || req.DefaultTimeframeEnd != "2026-01-01T01:00:00Z" {
				t.Errorf("unexpected timeframe %s - %s", req.DefaultTimeframeStart, req.DefaultTimeframeEnd)
			}
			if req.Timezone != "UTC" || req.MaxResultRecords != 7 {
				t.Errorf("unexpected request %+v", req)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"state":"RUNNING","requestToken":"tok/1"}`))
		case pollPath:
			if r.URL.Query().Get("request-token") != "tok/1" {
				t.Errorf("unexpected token %q", r.URL.Query().Get("request-token"))
			}
			if polls.Add(1) < 2 {
				_, _ = w.Write([]byte(`{"state":"RUNNING","requestToken":"tok/1"}`))
				return
			}
			_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[{"a":"1"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(Config{PlatformURL: srv.URL + "/"}, StaticToken("t"), srv.Client(), discardLogger())
	res, err := c.Query(context.Background(), QueryRequest{
		Query:      "fetch logs",
		Start:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:        time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
		MaxRecords: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 1 || res.Records[0]["a"] != "1" {
		t.Errorf("unexpected records %v", res.Records)
	}
	if polls.Load() != 2 {
		t.Errorf("expected 2 polls, got %d", polls.Load())
	}
}

func TestQueryReportsAPIError(t *testing.T) {
	f := newFakeGrail(t, func(string) ([]map[string]any, int) { return nil, http.StatusBadRequest })
	_, err := f.client().Query(context.Background(), QueryRequest{Query: "fetch logs"})
	if err == nil || !IsAPIError(err, http.StatusBadRequest) {
		t.Fatalf("expected 400 APIError, got %v", err)
	}
	if !strings.Contains(err.Error(), "PARSE_ERROR: bad dql") {
		t.Errorf("error should carry the Grail message: %v", err)
	}
}

func TestQueryFailedState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"state":"FAILED"}`))
	}))
	defer srv.Close()
	c := NewClient(Config{PlatformURL: srv.URL}, StaticToken("t"), srv.Client(), discardLogger())
	if _, err := c.Query(context.Background(), QueryRequest{Query: "x"}); err == nil {
		t.Fatal("expected error for FAILED state")
	}
}

func TestQueryRunningWithoutToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"state":"RUNNING"}`))
	}))
	defer srv.Close()
	c := NewClient(Config{PlatformURL: srv.URL}, StaticToken("t"), srv.Client(), discardLogger())
	if _, err := c.Query(context.Background(), QueryRequest{Query: "x"}); err == nil {
		t.Fatal("expected error for RUNNING without token")
	}
}

func TestPing(t *testing.T) {
	f := newFakeGrail(t, func(string) ([]map[string]any, int) { return nil, 0 })
	if err := f.client().Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q := f.queryTexts(); len(q) != 1 || !strings.HasPrefix(q[0], "fetch logs") {
		t.Errorf("unexpected ping query %v", q)
	}
}

func TestStaticTokenEmpty(t *testing.T) {
	if _, err := StaticToken("").Token(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestOAuthTokenSourceCaches(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "id" ||
			r.Form.Get("client_secret") != "secret" || r.Form.Get("scope") != DefaultOAuthScope ||
			r.Form.Get("resource") != "urn:dtaccount:x" {
			t.Errorf("unexpected form %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"access_token":"at","expires_in":300}`))
	}))
	defer srv.Close()

	s := NewOAuthTokenSource(OAuthConfig{TokenURL: srv.URL, ClientID: "id", ClientSecret: "secret", Resource: "urn:dtaccount:x"}, srv.Client())
	now := time.Now()
	s.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		tok, err := s.Token(context.Background())
		if err != nil || tok != "at" {
			t.Fatalf("token %q err %v", tok, err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("expected cached token, endpoint called %d times", calls.Load())
	}

	// Inside the refresh margin the token is renewed.
	now = now.Add(250 * time.Second)
	if _, err := s.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected refresh, endpoint called %d times", calls.Load())
	}
}

func TestOAuthTokenSourceErrors(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"status":   func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"no token": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) },
		"bad json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{`)) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			s := NewOAuthTokenSource(OAuthConfig{TokenURL: srv.URL, ClientID: "id", ClientSecret: "s"}, srv.Client())
			if _, err := s.Token(context.Background()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestNewOAuthTokenSourceDefaults(t *testing.T) {
	s := NewOAuthTokenSource(OAuthConfig{}, nil)
	if s.cfg.TokenURL != DefaultOAuthTokenURL || s.cfg.Scope != DefaultOAuthScope {
		t.Errorf("defaults not applied: %+v", s.cfg)
	}
	if s.httpClient.Timeout != 15*time.Second {
		t.Errorf("default timeout %s", s.httpClient.Timeout)
	}
}

// redirectTrap answers every request with a 307 to a second server, and counts the requests
// that second server receives.
func redirectTrap(t *testing.T) (redirector *httptest.Server, followed *atomic.Int32) {
	t.Helper()
	followed = &atomic.Int32{}
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	t.Cleanup(target.Close)
	redirector = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)
	return redirector, followed
}

func TestDefaultClientsDoNotFollowRedirects(t *testing.T) {
	srv, followed := redirectTrap(t)

	s := NewOAuthTokenSource(OAuthConfig{TokenURL: srv.URL, ClientID: "id", ClientSecret: "secret"}, nil)
	if _, err := s.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("OAuth: expected a 307 error, got %v", err)
	}

	c := NewClient(Config{PlatformURL: srv.URL}, StaticToken("t"), nil, discardLogger())
	if c.httpClient.Timeout != 60*time.Second {
		t.Errorf("default timeout %s", c.httpClient.Timeout)
	}
	if err := c.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("query: expected a 307 error, got %v", err)
	}

	if n := followed.Load(); n != 0 {
		t.Errorf("redirect target received %d requests", n)
	}
}

func TestGetComponentLogs(t *testing.T) {
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		if isCount(q) {
			return total(42), 0
		}
		return []map[string]any{{
			"timestamp":                  "2026-01-01T00:00:01.123456789Z",
			"content":                    "WARN disk low",
			"k8s.namespace.name":         "dp-default",
			"k8s.pod.name":               "api-1",
			"k8s.container.name":         "main",
			"openchoreo.namespace":       "default",
			"openchoreo.component":       "api",
			"openchoreo.component_uid":   "c-1",
			"openchoreo.project":         "shop",
			"openchoreo.project_uid":     "p-1",
			"openchoreo.environment":     "dev",
			"openchoreo.environment_uid": "e-1",
		}}, 0
	})

	res, err := f.client().GetComponentLogs(context.Background(), ComponentLogsParams{Namespace: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCount != 42 || len(res.Logs) != 1 {
		t.Fatalf("unexpected result %+v", res)
	}
	l := res.Logs[0]
	if l.LogLevel != "WARN" || l.ComponentName != "api" || l.PodNamespace != "dp-default" || l.EnvironmentUID != "e-1" {
		t.Errorf("unexpected entry %+v", l)
	}
	if l.Timestamp.Nanosecond() != 123456789 {
		t.Errorf("timestamp precision lost: %v", l.Timestamp)
	}
	if len(f.queryTexts()) != 2 {
		t.Errorf("expected page and count queries, got %d", len(f.queryTexts()))
	}
}

func TestGetComponentLogsError(t *testing.T) {
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		if isCount(q) {
			return nil, http.StatusBadRequest
		}
		return nil, 0
	})
	if _, err := f.client().GetComponentLogs(context.Background(), ComponentLogsParams{Namespace: "default"}); err == nil {
		t.Fatal("expected error when the count query fails")
	}
	if _, err := f.client().GetComponentLogs(context.Background(), ComponentLogsParams{}); err == nil {
		t.Fatal("expected error without namespace")
	}
}

func TestGetWorkflowLogs(t *testing.T) {
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		if isCount(q) {
			return total(1), 0
		}
		return []map[string]any{{"timestamp": "2026-01-01T00:00:01Z", "content": "step 1"}}, 0
	})
	res, err := f.client().GetWorkflowLogs(context.Background(), WorkflowLogsParams{Namespace: "default", WorkflowRunName: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCount != 1 || res.Logs[0].Log != "step 1" {
		t.Errorf("unexpected result %+v", res)
	}
}

func TestGetPlatformLogsAndValues(t *testing.T) {
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		switch {
		case strings.Contains(q, "countDistinctExact"):
			return total(3), 0
		case isCount(q):
			return total(9), 0
		case strings.Contains(q, "record_count"):
			return []map[string]any{
				{"value": "ns-a", "record_count": "5"},
				{"value": "", "record_count": "1"},
				{"value": "ns-b", "record_count": 2.0},
			}, 0
		}
		return []map[string]any{{
			"timestamp":            "2026-01-01T00:00:01Z",
			"content":              "hello",
			"k8s.cluster.name":     "c1",
			"k8s.namespace.name":   "ns-a",
			"k8s.pod.ip":           "10.0.0.1",
			"k8s.node.name":        "node-1",
			"container.image.name": "img:1",
			"k8s.pod.labels":       []any{"app=api", "openchoreo.dev/plane=dp", "broken"},
		}}, 0
	})
	c := f.client()

	res, err := c.GetPlatformLogs(context.Background(), PlatformLogsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalCount != 9 || res.Logs[0].LogLevel != "INFO" || res.Logs[0].NodeName != "node-1" {
		t.Errorf("unexpected result %+v", res)
	}
	if got := res.Logs[0].Labels; len(got) != 2 || got["openchoreo.dev/plane"] != "dp" {
		t.Errorf("unexpected labels %v", got)
	}

	values, err := c.GetPlatformLogFilterValues(context.Background(), PlatformLogsParams{}, fNamespaceName, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if values.TotalValues != 3 || len(values.Values) != 2 || values.Values[0].Count != 5 || values.Values[1].Count != 2 {
		t.Errorf("unexpected values %+v", values)
	}
}

func eventRecord(ts, reason string) map[string]any {
	return map[string]any{
		"timestamp":          ts,
		"content":            "Started container",
		"loglevel":           "Normal",
		"k8s.event.reason":   reason,
		"k8s.object.kind":    "Pod",
		"k8s.object.name":    "api-1",
		"k8s.namespace.name": "dp-ns",
		"k8s.object.label.openchoreo.dev/component-uid": "c-1",
	}
}

func TestGetEventsExtendsTies(t *testing.T) {
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		switch {
		case isCount(q):
			return total(5), 0
		case strings.Contains(q, "toTimestamp"):
			return []map[string]any{
				eventRecord("2026-01-01T00:00:02Z", "B"),
				eventRecord("2026-01-01T00:00:02Z", "C"),
				eventRecord("2026-01-01T00:00:02Z", "D"),
			}, 0
		}
		return []map[string]any{
			eventRecord("2026-01-01T00:00:01Z", "A"),
			eventRecord("2026-01-01T00:00:02Z", "B"),
		}, 0
	})

	res, err := f.client().GetEvents(context.Background(), EventsQueryParams{
		Reasons: []string{"A", "B", "C", "D"}, Limit: 2, SortOrder: "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, e := range res.Events {
		reasons = append(reasons, e.Reason)
	}
	if strings.Join(reasons, ",") != "A,B,C,D" {
		t.Errorf("tie extension produced %v", reasons)
	}
	if res.TotalCount != 5 {
		t.Errorf("total %d", res.TotalCount)
	}
	e := res.Events[0]
	if e.Type != "Normal" || e.ObjectKind != "Pod" || e.ComponentID != "c-1" || e.ObjectNamespace != "dp-ns" {
		t.Errorf("unexpected event %+v", e)
	}
}

func TestGetEventsNoExtensionWhenComplete(t *testing.T) {
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		if isCount(q) {
			return total(2), 0
		}
		return []map[string]any{eventRecord("2026-01-01T00:00:01Z", "A"), eventRecord("2026-01-01T00:00:02Z", "B")}, 0
	})
	res, err := f.client().GetEvents(context.Background(), EventsQueryParams{Reasons: []string{"A"}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) != 2 || len(f.queryTexts()) != 2 {
		t.Errorf("unexpected extension: %d events, %d queries", len(res.Events), len(f.queryTexts()))
	}
}

func TestExtendTiesKeepsPageWhenTieReadShort(t *testing.T) {
	last := time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)
	page := []map[string]any{eventRecord("2026-01-01T00:00:02Z", "A"), eventRecord("2026-01-01T00:00:02Z", "B")}
	got := extendTies(page, last, []map[string]any{eventRecord("2026-01-01T00:00:02Z", "A")})
	if len(got) != 2 {
		t.Errorf("expected page unchanged, got %d records", len(got))
	}
}

func TestEventType(t *testing.T) {
	for in, want := range map[string]string{"Normal": "Normal", "INFO": "Normal", "NOTICE": "Normal", "Warning": "Warning", "WARN": "Warning", "NONE": ""} {
		if got := eventType(map[string]any{"loglevel": in}); got != want {
			t.Errorf("eventType(%q) = %q, want %q", in, got, want)
		}
	}
	if got := eventType(map[string]any{"severity": "Warning"}); got != "Warning" {
		t.Errorf("severity fallback: %q", got)
	}
}

const auditLine = `{"time":"2026-01-01T00:00:01Z","level":"INFO","msg":"AUDIT-LOG","schema_version":"1.0",` +
	`"event_id":"01J","event_time":"2026-01-01T00:00:01Z","actor":{"type":"user","id":"alice",` +
	`"entitlements":{"groups":["admins"]}},"action":"create_project","category":"management",` +
	`"result":"success","resource":{"type":"project","namespace":"default","name":"shop","metadata":{"k":{"n":1}}},` +
	`"http":{"method":"POST","path":"/api/v1/projects"},"metadata":{"a":"b"}}`

func TestGetAuditLogsWithTimeline(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		switch {
		case isCount(q):
			return total(2), 0
		case strings.Contains(q, "bucket ="):
			return []map[string]any{
				{"bucket": "0", "result": "success", "record_count": "1"},
				{"bucket": "2", "result": "denied", "record_count": "4"},
				{"bucket": "99", "result": "denied", "record_count": "4"},
			}, 0
		}
		return []map[string]any{
			{"timestamp": "2026-01-01T00:00:01Z", "content": auditLine, "k8s.pod.name": "api-0", "k8s.container.name": "api-server"},
			{"timestamp": "2026-01-01T00:00:01Z", "content": `{"not":"audit"}`},
		}, 0
	})

	res, err := f.client().GetAuditLogs(context.Background(), AuditLogsParams{
		StartTime: start, EndTime: start.Add(time.Hour),
	}, "15m")
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 || len(res.Records) != 1 {
		t.Fatalf("unexpected result: total %d records %d", res.Total, len(res.Records))
	}
	r := res.Records[0]
	if r.Actor.ID != "alice" || r.Resource.Name != "shop" || r.HTTP.Method != "POST" || r.Collector.PodName != "api-0" {
		t.Errorf("unexpected record %+v", r)
	}
	if res.Timeline == nil || len(res.Timeline.Buckets) != 4 {
		t.Fatalf("unexpected timeline %+v", res.Timeline)
	}
	if res.Timeline.Buckets[0].Total != 1 || res.Timeline.Buckets[2].Counts["denied"] != 4 || res.Timeline.Buckets[1].Total != 0 {
		t.Errorf("unexpected buckets %+v", res.Timeline.Buckets)
	}
}

func TestGetAuditLogsTimelineFailureIsOmitted(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		switch {
		case strings.Contains(q, "bucket ="):
			return nil, http.StatusBadRequest
		case isCount(q):
			return total(0), 0
		}
		return nil, 0
	})
	res, err := f.client().GetAuditLogs(context.Background(), AuditLogsParams{StartTime: start, EndTime: start.Add(time.Hour)}, "1h")
	if err != nil {
		t.Fatal(err)
	}
	if res.Timeline != nil {
		t.Errorf("failed timeline should be omitted")
	}
}

func TestGetAuditLogFilterValues(t *testing.T) {
	f := newFakeGrail(t, func(q string) ([]map[string]any, int) {
		if isCount(q) {
			return total(1), 0
		}
		return []map[string]any{{"value": "alice", "record_count": "3"}}, 0
	})
	res, err := f.client().GetAuditLogFilterValues(context.Background(), AuditLogsParams{}, "actor.id", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalValues != 1 || res.Values[0].Value != "alice" || res.Values[0].Count != 3 {
		t.Errorf("unexpected %+v", res)
	}
	if _, err := f.client().GetAuditLogFilterValues(context.Background(), AuditLogsParams{}, "nope", "", 10); err == nil {
		t.Error("expected error for unknown filter")
	}
}

func TestParseAuditRecordRejectsIncomplete(t *testing.T) {
	for name, row := range map[string]map[string]any{
		"no content": {},
		"bad json":   {"content": "{"},
		"no id":      {"content": `{"schema_version":"1.0","event_time":"2026-01-01T00:00:00Z","actor":{"type":"user","id":"a"},"result":"success"}`},
		"no time":    {"content": `{"schema_version":"1.0","event_id":"x","actor":{"type":"user","id":"a"},"result":"success"}`},
	} {
		if _, err := ParseAuditRecord(row); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestIsAuditFilter(t *testing.T) {
	if !IsAuditFilter("actor.entitlements") || !IsAuditFilter("resource.name") || IsAuditFilter("event_id") {
		t.Error("unexpected audit filter set")
	}
}
