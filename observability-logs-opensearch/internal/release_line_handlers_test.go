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
		"one line":     {line: ptr(lineUID), wantInBody: lineUID},
		"every line":   {allLines: ptr(true), wantInBody: "release-line-component-uid"},
		"default line": {wantInBody: `"must_not"`},
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

// An alert rule read back names its component, though its query does not name it at the top level.
func TestParseMonitor_ReleaseLineComponentUID(t *testing.T) {
	const componentUID = "d2108e2c-f912-4cab-a0b4-71afde25c5ac"
	qb := osearch.NewQueryBuilder("logs-")
	for name, lineUID := range map[string]string{"default line": "", "release line": "93f65294-926f-475b-98f1-dc4f152f38da"} {
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
