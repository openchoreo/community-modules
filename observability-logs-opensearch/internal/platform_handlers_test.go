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

var (
	platformStart = time.Date(2026, 8, 14, 16, 30, 0, 0, time.UTC)
	platformEnd   = time.Date(2026, 8, 14, 17, 30, 0, 0, time.UTC)
)

// platformSearchServer stands in for OpenSearch, returning the given hits and recording
// the query body it was sent.
func platformSearchServer(t *testing.T, captured *map[string]interface{}, hits []map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read request body: %v", err)
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Errorf("parse request body: %v", err)
			}
			*captured = parsed
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"took":      3,
			"timed_out": false,
			"hits": map[string]interface{}{
				"total": map[string]interface{}{"value": len(hits), "relation": "eq"},
				"hits":  hits,
			},
		})
	}))
}

func platformHandler(t *testing.T, serverURL string) *LogsHandler {
	t.Helper()
	return NewLogsHandler(
		newTestOSClient(t, serverURL),
		osearch.NewQueryBuilder("container-logs-"),
		nil, nil, testLogger(),
	)
}

func TestQueryPlatformLogs_NilBody(t *testing.T) {
	handler := NewLogsHandler(nil, nil, nil, nil, testLogger())

	resp, err := handler.QueryPlatformLogs(context.Background(), gen.QueryPlatformLogsRequestObject{Body: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryPlatformLogs400JSONResponse); !ok {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestQueryPlatformLogs_Success(t *testing.T) {
	server := platformSearchServer(t, nil, []map[string]interface{}{
		{
			"_id":    "hit-1",
			"_score": 1.0,
			"_source": map[string]interface{}{
				"log":                         "2026-08-14T16:31:00Z\tERROR\treconcile failed",
				"@timestamp":                  "2026-08-14T16:31:00Z",
				"openchoreo_cluster_instance": "cluster1",
				"kubernetes": map[string]interface{}{
					"namespace_name":  "openchoreo-control-plane",
					"pod_name":        "controller-manager-7f58b689b5-pwsb5",
					"container_name":  "manager",
					"pod_ip":          "10.0.0.7",
					"host":            "k3d-openchoreo-server-0",
					"container_image": "ghcr.io/openchoreo/controller:latest-dev",
					"labels": map[string]interface{}{
						"openchoreo_dev/plane": "controlplane",
					},
				},
			},
		},
	})
	defer server.Close()

	resp, err := platformHandler(t, server.URL).QueryPlatformLogs(
		context.Background(),
		gen.QueryPlatformLogsRequestObject{Body: &gen.PlatformLogsQueryRequest{
			StartTime: platformStart,
			EndTime:   platformEnd,
		}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queryResp, ok := resp.(gen.QueryPlatformLogs200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if queryResp.Total != 1 {
		t.Errorf("total = %d, want 1", queryResp.Total)
	}
	if queryResp.TookMs != 3 {
		t.Errorf("tookMs = %d, want 3", queryResp.TookMs)
	}
	if len(queryResp.Logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(queryResp.Logs))
	}

	entry := queryResp.Logs[0]
	for _, tc := range []struct {
		name string
		got  *string
		want string
	}{
		{"clusterInstance", entry.ClusterInstance, "cluster1"},
		{"namespaceName", entry.NamespaceName, "openchoreo-control-plane"},
		{"podName", entry.PodName, "controller-manager-7f58b689b5-pwsb5"},
		{"containerName", entry.ContainerName, "manager"},
		{"level", entry.Level, "ERROR"},
		{"podIp", entry.PodIp, "10.0.0.7"},
		{"nodeName", entry.NodeName, "k3d-openchoreo-server-0"},
		{"containerImage", entry.ContainerImage, "ghcr.io/openchoreo/controller:latest-dev"},
	} {
		if tc.got == nil || *tc.got != tc.want {
			t.Errorf("%s = %v, want %q", tc.name, tc.got, tc.want)
		}
	}

	// Labels come back keyed as Kubernetes spells them, not as OpenSearch stores them,
	// so a key copied out of the response works verbatim in the label filter.
	if entry.Labels == nil {
		t.Fatal("expected labels on the entry")
	}
	if got := (*entry.Labels)["openchoreo.dev/plane"]; got != "controlplane" {
		t.Errorf("labels[openchoreo.dev/plane] = %q, want %q", got, "controlplane")
	}
}

// TestQueryPlatformLogs_FiltersReachOpenSearch checks the whole mapping in one go: every
// filter on the request has to arrive in the query body, and label keys have to be
// rewritten to the form Fluent Bit stores them in.
func TestQueryPlatformLogs_FiltersReachOpenSearch(t *testing.T) {
	var captured map[string]interface{}
	server := platformSearchServer(t, &captured, nil)
	defer server.Close()

	levels := []gen.PlatformLogsQueryRequestLogLevels{"ERROR"}
	sortOrder := gen.PlatformLogsQueryRequestSortOrder("asc")
	limit := 25
	phrase := "reconcile"
	body := gen.PlatformLogsQueryRequest{
		StartTime:       platformStart,
		EndTime:         platformEnd,
		ClusterInstance: &[]string{"cluster1"},
		Namespace:       &[]string{"openchoreo-control-plane"},
		PodName:         &[]string{"controller-manager-abc"},
		ContainerName:   &[]string{"manager"},
		Labels:          &map[string]string{"openchoreo.dev/plane": "controlplane"},
		LogLevels:       &levels,
		SearchPhrase:    &phrase,
		Limit:           &limit,
		SortOrder:       &sortOrder,
	}

	if _, err := platformHandler(t, server.URL).QueryPlatformLogs(
		context.Background(), gen.QueryPlatformLogsRequestObject{Body: &body},
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured == nil {
		t.Fatal("no query captured")
	}
	rendered, err := json.Marshal(captured)
	if err != nil {
		t.Fatalf("marshal captured query: %v", err)
	}
	got := string(rendered)

	for _, want := range []string{
		`"openchoreo_cluster_instance":["cluster1"]`,
		`"kubernetes.namespace_name":["openchoreo-control-plane"]`,
		`"kubernetes.pod_name":["controller-manager-abc"]`,
		`"kubernetes.container_name":["manager"]`,
		`"kubernetes.labels.openchoreo_dev/plane":"controlplane"`,
		`"size":25`,
		`"order":"asc"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("query missing %s\ngot: %s", want, got)
		}
	}
}

func TestQueryPlatformLogs_SearchFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	resp, err := platformHandler(t, server.URL).QueryPlatformLogs(
		context.Background(),
		gen.QueryPlatformLogsRequestObject{Body: &gen.PlatformLogsQueryRequest{
			StartTime: platformStart,
			EndTime:   platformEnd,
		}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryPlatformLogs500JSONResponse); !ok {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

// TestQueryPlatformLogs_UnknownFieldsOmitted pins that a record with no cluster stamp -
// anything collected before the collector change - serialises without the field rather
// than with an empty string.
func TestQueryPlatformLogs_UnknownFieldsOmitted(t *testing.T) {
	server := platformSearchServer(t, nil, []map[string]interface{}{
		{
			"_id":     "hit-1",
			"_score":  1.0,
			"_source": map[string]interface{}{"log": "plain line", "@timestamp": "2026-08-14T16:31:00Z"},
		},
	})
	defer server.Close()

	resp, err := platformHandler(t, server.URL).QueryPlatformLogs(
		context.Background(),
		gen.QueryPlatformLogsRequestObject{Body: &gen.PlatformLogsQueryRequest{
			StartTime: platformStart,
			EndTime:   platformEnd,
		}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entry := resp.(gen.QueryPlatformLogs200JSONResponse).Logs[0]
	if entry.ClusterInstance != nil {
		t.Errorf("clusterInstance = %v, want nil", *entry.ClusterInstance)
	}
	if entry.PodName != nil {
		t.Errorf("podName = %v, want nil", *entry.PodName)
	}
	if entry.Log != "plain line" {
		t.Errorf("log = %v", entry.Log)
	}
}

// TestQueryPlatformLogs_SkipsMalformedDocuments pins the contract guarantees this
// module owns. timestamp and log are both required on PlatformLog, so a document
// missing either must not be emitted - serving it would put 0001-01-01T00:00:00Z on
// the wire, or a blank line that was never logged.
//
// A genuinely empty log message is not malformed: blank lines are real container
// output and must survive, which is why the check is on presence rather than on the
// value being non-empty.
func TestQueryPlatformLogs_SkipsMalformedDocuments(t *testing.T) {
	server := platformSearchServer(t, nil, []map[string]interface{}{
		{
			"_id":     "timestamp-unparseable",
			"_source": map[string]interface{}{"log": "x", "@timestamp": "14/08/2026 16:31"},
		},
		{
			"_id":     "timestamp-missing",
			"_source": map[string]interface{}{"log": "x"},
		},
		{
			"_id":     "log-missing",
			"_source": map[string]interface{}{"@timestamp": "2026-08-14T16:31:00Z"},
		},
		{
			"_id":     "log-not-a-string",
			"_source": map[string]interface{}{"log": 42, "@timestamp": "2026-08-14T16:31:00Z"},
		},
		{
			// A blank line is real output, not a malformed document.
			"_id":     "log-genuinely-blank",
			"_source": map[string]interface{}{"log": "", "@timestamp": "2026-08-14T16:32:00Z"},
		},
		{
			"_id":     "good",
			"_source": map[string]interface{}{"log": "usable", "@timestamp": "2026-08-14T16:33:00Z"},
		},
	})
	defer server.Close()

	resp, err := platformHandler(t, server.URL).QueryPlatformLogs(
		context.Background(),
		gen.QueryPlatformLogsRequestObject{Body: &gen.PlatformLogsQueryRequest{
			StartTime: platformStart,
			EndTime:   platformEnd,
		}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queryResp, ok := resp.(gen.QueryPlatformLogs200JSONResponse)
	if !ok {
		t.Fatalf("expected 200 response, got %T", resp)
	}

	var got []string
	for _, l := range queryResp.Logs {
		got = append(got, l.Log)
		if l.Timestamp.IsZero() {
			t.Errorf("served a record with no timestamp: %q", l.Log)
		}
	}
	want := []string{"", "usable"}
	if len(got) != len(want) {
		t.Fatalf("served %d records %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d = %q, want %q", i, got[i], want[i])
		}
	}
}
