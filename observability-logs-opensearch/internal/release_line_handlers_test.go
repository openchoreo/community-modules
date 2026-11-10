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
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
	osearch "github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

func TestQueryLogs_ReleaseLineHeader(t *testing.T) {
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	tests := map[string]struct {
		line       *string
		allLines   *bool
		wantInBody string
	}{
		"one line":   {line: ptr(lineUID), wantInBody: lineUID},
		"every line": {allLines: ptr(true), wantInBody: "release-line-component-uid"},
		"main line":  {wantInBody: `"must_not"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var searchBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				searchBody = string(b)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"took":1,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`))
			}))
			defer server.Close()
			handler := NewLogsHandler(newTestOSClient(t, server.URL), osearch.NewQueryBuilder("logs-"), nil, nil, nil, testLogger())

			scope := gen.LogsQueryRequest_SearchScope{}
			_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: ptr("comp-uid"), ReleaseLineUid: tt.line, AllReleaseLines: tt.allLines,
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

			if !strings.Contains(searchBody, tt.wantInBody) {
				t.Errorf("the line scope did not reach the OpenSearch query, want %s in %s", tt.wantInBody, searchBody)
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", got)
			}
		})
	}
}

// Each log entry names the release line of the pod that wrote it, and a main line entry names none.
func TestQueryLogs_EntryReleaseLine(t *testing.T) {
	const componentUID = "550e8400-e29b-41d4-a716-446655440000"
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	hit := func(log string, labels map[string]string) map[string]any {
		return map[string]any{"_source": map[string]any{
			"@timestamp": "2025-06-15T00:30:00Z",
			"log":        log,
			"kubernetes": map[string]any{"labels": labels},
		}}
	}
	searchResponse, err := json.Marshal(map[string]any{
		"took": 1,
		"hits": map[string]any{
			"total": map[string]any{"value": 2, "relation": "eq"},
			"hits": []any{
				hit("from next", map[string]string{
					osearch.ReplaceDots(osearch.ReleaseLineComponentID): componentUID,
					osearch.ReplaceDots(osearch.ReleaseLineName):        "next",
					osearch.ReplaceDots(osearch.ReleaseLineID):          lineUID,
				}),
				hit("from main", map[string]string{osearch.ReplaceDots(osearch.ComponentID): componentUID}),
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal search response: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(searchResponse)
	}))
	defer server.Close()
	handler := NewLogsHandler(newTestOSClient(t, server.URL), osearch.NewQueryBuilder("logs-"), nil, nil, nil, testLogger())

	scope := gen.LogsQueryRequest_SearchScope{}
	_ = scope.FromComponentSearchScope(gen.ComponentSearchScope{
		Namespace: "default", ComponentUid: ptr(componentUID), AllReleaseLines: ptr(true),
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
	entries, err := ok.Body.Logs.AsLogsQueryResponseLogs0()
	if err != nil {
		t.Fatalf("read log entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	line := entries[0].Metadata
	if line.ReleaseLineName == nil || *line.ReleaseLineName != "next" {
		t.Errorf("releaseLineName = %v, want next", line.ReleaseLineName)
	}
	if line.ReleaseLineUid == nil || line.ReleaseLineUid.String() != lineUID {
		t.Errorf("releaseLineUid = %v, want %s", line.ReleaseLineUid, lineUID)
	}
	if line.ComponentUid == nil || line.ComponentUid.String() != componentUID {
		t.Errorf("componentUid = %v, want %s", line.ComponentUid, componentUID)
	}

	main := entries[1].Metadata
	if main.ReleaseLineName != nil || main.ReleaseLineUid != nil {
		t.Errorf("main line entry has release line %v / %v, want none", main.ReleaseLineName, main.ReleaseLineUid)
	}
}

// An alert rule read back names its component, though its query does not name it at the top level.
func TestParseMonitor_ReleaseLineComponentUID(t *testing.T) {
	const componentUID = "d2108e2c-f912-4cab-a0b4-71afde25c5ac"
	qb := osearch.NewQueryBuilder("logs-")
	for name, lineUID := range map[string]string{"main line": "", "release line": "93f65294-926f-475b-98f1-dc4f152f38da"} {
		t.Run(name, func(t *testing.T) {
			body, err := qb.BuildLogAlertingRuleMonitorBody(osearch.AlertingRuleRequest{
				Metadata: osearch.AlertingRuleMetadata{
					Name: "rule", Namespace: "ns", ComponentUID: componentUID, ReleaseLineUID: lineUID,
					ProjectUID: "31e18226-49ce-48e7-a09b-d1f764d61a19", EnvironmentUID: "7d1b44c7-d5f5-4bbd-a659-015139e98e0e",
				},
				Source:    osearch.AlertingRuleSource{Query: "error"},
				Condition: osearch.AlertingRuleCondition{Enabled: true, Window: "5m", Interval: "1m", Operator: "gt", Threshold: 3},
			})
			if err != nil {
				t.Fatalf("build monitor: %v", err)
			}
			// Read the monitor back the way OpenSearch returns it.
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal monitor: %v", err)
			}
			var monitor map[string]interface{}
			if err := json.Unmarshal(raw, &monitor); err != nil {
				t.Fatalf("unmarshal monitor: %v", err)
			}

			resp, err := parseMonitorToAlertRuleResponse(monitor)
			if err != nil {
				t.Fatalf("parse monitor: %v", err)
			}
			if resp.Metadata == nil || resp.Metadata.ComponentUid == nil || resp.Metadata.ComponentUid.String() != componentUID {
				t.Errorf("component UID = %v, want %s", resp.Metadata.ComponentUid, componentUID)
			}
		})
	}
}
