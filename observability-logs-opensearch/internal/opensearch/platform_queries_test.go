// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// platformMustConditions pulls the bool/must array out of a built query.
func platformMustConditions(t *testing.T, query map[string]interface{}) []map[string]interface{} {
	t.Helper()
	boolQuery, ok := query["query"].(map[string]interface{})["bool"].(map[string]interface{})
	if !ok {
		t.Fatalf("query has no bool clause: %v", query)
	}
	conds, ok := boolQuery["must"].([]map[string]interface{})
	if !ok {
		t.Fatalf("bool clause has no must array: %v", boolQuery)
	}
	return conds
}

// findClause returns the first must-condition of the given type whose field matches.
func findClause(conds []map[string]interface{}, clauseType, field string) interface{} {
	for _, c := range conds {
		inner, ok := c[clauseType].(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := inner[field]; ok {
			return v
		}
	}
	return nil
}

func TestBuildPlatformLogsQuery_AllFilters(t *testing.T) {
	qb := NewQueryBuilder("container-logs-")

	query := qb.BuildPlatformLogsQuery(PlatformLogsQueryParams{
		StartTime:        "2026-08-14T16:30:00Z",
		EndTime:          "2026-08-14T17:30:00Z",
		ClusterInstances: []string{"cluster1", "cluster2"},
		Namespaces:       []string{"openchoreo-control-plane"},
		PodNames:         []string{"controller-manager-abc"},
		ContainerNames:   []string{"manager"},
		Labels:           map[string]string{"openchoreo.dev/plane": "controlplane"},
		SearchPhrase:     "reconcile",
		LogLevels:        []string{"ERROR"},
		Limit:            50,
		SortOrder:        "asc",
	})

	if query["size"] != 50 {
		t.Errorf("size = %v, want 50", query["size"])
	}
	sortOrder := query["sort"].([]map[string]interface{})[0]["@timestamp"].(map[string]interface{})["order"]
	if sortOrder != "asc" {
		t.Errorf("sort order = %v, want asc", sortOrder)
	}

	conds := platformMustConditions(t, query)

	for _, tc := range []struct {
		field string
		want  []string
	}{
		{ClusterInstanceField, []string{"cluster1", "cluster2"}},
		{KubernetesNamespaceName, []string{"openchoreo-control-plane"}},
		{KubernetesPodName, []string{"controller-manager-abc"}},
		{KubernetesContainerName, []string{"manager"}},
	} {
		got := findClause(conds, "terms", tc.field)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("terms on %s = %v, want %v", tc.field, got, tc.want)
		}
	}

	if got := findClause(conds, "wildcard", "log"); got != "*reconcile*" {
		t.Errorf("search phrase clause = %v, want *reconcile*", got)
	}
}

// TestBuildPlatformLogsQuery_LabelKeysAreDotReplaced pins the mapping between how
// Kubernetes spells a label and how Fluent Bit stores it. The OpenSearch output runs with
// Replace_Dots On, so a query built with the Kubernetes spelling would match nothing.
func TestBuildPlatformLogsQuery_LabelKeysAreDotReplaced(t *testing.T) {
	qb := NewQueryBuilder("container-logs-")

	query := qb.BuildPlatformLogsQuery(PlatformLogsQueryParams{
		StartTime: "2026-08-14T16:30:00Z",
		EndTime:   "2026-08-14T17:30:00Z",
		Labels: map[string]string{
			"openchoreo.dev/plane":    "dataplane",
			"openchoreo.dev/plane-id": "prod",
			"app.kubernetes.io/name":  "openbao",
		},
	})

	conds := platformMustConditions(t, query)
	for field, want := range map[string]string{
		"kubernetes.labels.openchoreo_dev/plane":    "dataplane",
		"kubernetes.labels.openchoreo_dev/plane-id": "prod",
		"kubernetes.labels.app_kubernetes_io/name":  "openbao",
	} {
		if got := findClause(conds, "term", field); got != want {
			t.Errorf("term on %s = %v, want %v", field, got, want)
		}
	}
}

// TestBuildPlatformLogsQuery_LabelOrderIsDeterministic keeps the built query comparable:
// Go map iteration order is randomised, so without sorting the same filters would produce
// different queries run to run.
func TestBuildPlatformLogsQuery_LabelOrderIsDeterministic(t *testing.T) {
	qb := NewQueryBuilder("container-logs-")
	params := PlatformLogsQueryParams{
		StartTime: "2026-08-14T16:30:00Z",
		EndTime:   "2026-08-14T17:30:00Z",
		Labels:    map[string]string{"c": "3", "a": "1", "b": "2"},
	}

	first, err := json.Marshal(qb.BuildPlatformLogsQuery(params))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := json.Marshal(qb.BuildPlatformLogsQuery(params))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(next) != string(first) {
			t.Fatalf("query is not deterministic:\n%s\n%s", first, next)
		}
	}
}

// TestBuildPlatformLogsQuery_EmptyFiltersAreNotFilters guards the difference between "no
// filter" and "match nothing": an absent filter must not narrow the query at all.
func TestBuildPlatformLogsQuery_EmptyFiltersAreNotFilters(t *testing.T) {
	qb := NewQueryBuilder("container-logs-")

	query := qb.BuildPlatformLogsQuery(PlatformLogsQueryParams{
		StartTime:        "2026-08-14T16:30:00Z",
		EndTime:          "2026-08-14T17:30:00Z",
		ClusterInstances: []string{},
		Namespaces:       nil,
		Labels:           map[string]string{},
	})

	conds := platformMustConditions(t, query)
	if len(conds) != 1 {
		t.Fatalf("expected only the time range clause, got %d: %v", len(conds), conds)
	}
	if _, ok := conds[0]["range"]; !ok {
		t.Errorf("expected a range clause, got %v", conds[0])
	}
}

func TestBuildPlatformLogsQuery_Defaults(t *testing.T) {
	qb := NewQueryBuilder("container-logs-")

	query := qb.BuildPlatformLogsQuery(PlatformLogsQueryParams{
		StartTime: "2026-08-14T16:30:00Z",
		EndTime:   "2026-08-14T17:30:00Z",
	})

	if query["size"] != 100 {
		t.Errorf("default size = %v, want 100", query["size"])
	}
	sortOrder := query["sort"].([]map[string]interface{})[0]["@timestamp"].(map[string]interface{})["order"]
	if sortOrder != "desc" {
		t.Errorf("default sort order = %v, want desc", sortOrder)
	}
}

func TestParsePlatformLogEntry(t *testing.T) {
	hit := Hit{Source: map[string]interface{}{
		"@timestamp":                  "2026-08-14T16:31:00Z",
		"log":                         "2026-08-14T16:31:00Z\tERROR\treconcile failed",
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
				"pod-template-hash":    "5565c9cdb",
			},
		},
	}}

	entry, err := ParsePlatformLogEntry(hit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := PlatformLogEntry{
		Timestamp:       time.Date(2026, 8, 14, 16, 31, 0, 0, time.UTC),
		Log:             "2026-08-14T16:31:00Z\tERROR\treconcile failed",
		LogLevel:        "ERROR",
		ClusterInstance: "cluster1",
		NamespaceName:   "openchoreo-control-plane",
		PodName:         "controller-manager-7f58b689b5-pwsb5",
		ContainerName:   "manager",
		PodIP:           "10.0.0.7",
		NodeName:        "k3d-openchoreo-server-0",
		ContainerImage:  "ghcr.io/openchoreo/controller:latest-dev",
		Labels: map[string]string{
			// Stored as openchoreo_dev/plane; handed back as Kubernetes spells it.
			"openchoreo.dev/plane": "controlplane",
			"pod-template-hash":    "5565c9cdb",
		},
	}
	if !entry.Timestamp.Equal(want.Timestamp) {
		t.Errorf("timestamp = %v, want %v", entry.Timestamp, want.Timestamp)
	}
	entry.Timestamp, want.Timestamp = time.Time{}, time.Time{}
	if !reflect.DeepEqual(entry, want) {
		t.Errorf("entry = %+v, want %+v", entry, want)
	}
}

// TestParsePlatformLogEntry_MissingFields pins that a record without kubernetes metadata
// or a cluster stamp still parses - records predating the collector change have no
// openchoreo_cluster_instance and must not break the response.
func TestParsePlatformLogEntry_MissingFields(t *testing.T) {
	entry, err := ParsePlatformLogEntry(Hit{Source: map[string]interface{}{
		"@timestamp": "2026-08-14T16:31:00Z",
		"log":        "no metadata here",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.ClusterInstance != "" || entry.NamespaceName != "" || entry.PodName != "" {
		t.Errorf("expected empty coordinates, got %+v", entry)
	}
	if entry.Log != "no metadata here" {
		t.Errorf("log = %q", entry.Log)
	}
}

// TestRestoreLabelKey pins the rule that makes a returned label key paste-able straight
// back into the label filter. Fluent Bit's Replace_Dots mangles keys on the way in, and
// reversing it naively would corrupt every key whose name part legitimately contains an
// underscore - "version_id" being one this module already relies on for workload logs.
func TestRestoreLabelKey(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{
		// Prefixes are DNS subdomains and cannot contain "_", so every one was a ".".
		{"openchoreo_dev/plane", "openchoreo.dev/plane"},
		{"openchoreo_dev/plane-id", "openchoreo.dev/plane-id"},
		{"app_kubernetes_io/name", "app.kubernetes.io/name"},
		// Name parts may contain "_" legitimately and must survive untouched.
		{"version_id", "version_id"},
		{"openchoreo_dev/version_id", "openchoreo.dev/version_id"},
		{"pod-template-hash", "pod-template-hash"},
		{"", ""},
	} {
		if got := RestoreLabelKey(tc.stored); got != tc.want {
			t.Errorf("RestoreLabelKey(%q) = %q, want %q", tc.stored, got, tc.want)
		}
	}
}

func TestParsePlatformLogEntry_NoLabels(t *testing.T) {
	entry, err := ParsePlatformLogEntry(Hit{Source: map[string]interface{}{
		"@timestamp": "2026-08-14T16:31:00Z",
		"log":        "no metadata",
		"kubernetes": map[string]interface{}{"pod_name": "p"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Labels != nil {
		t.Errorf("expected nil labels, got %v", entry.Labels)
	}
}

// TestParsePlatformLogEntry_RejectsMalformed pins the two fields the contract
// requires. A blank log line is real container output and must parse; an absent or
// non-string field is a malformed document and must not become an entry.
func TestParsePlatformLogEntry_RejectsMalformed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  map[string]interface{}
		wantErr string
	}{
		{
			name:    "timestamp absent",
			source:  map[string]interface{}{"log": "x"},
			wantErr: "@timestamp is absent or not a string",
		},
		{
			name:    "timestamp not a string",
			source:  map[string]interface{}{"log": "x", "@timestamp": 1755188000},
			wantErr: "@timestamp is absent or not a string",
		},
		{
			name:    "timestamp not RFC3339",
			source:  map[string]interface{}{"log": "x", "@timestamp": "14/08/2026 16:31"},
			wantErr: "is not RFC3339",
		},
		{
			name:    "log absent",
			source:  map[string]interface{}{"@timestamp": "2026-08-14T16:31:00Z"},
			wantErr: "log is absent or not a string",
		},
		{
			name:    "log not a string",
			source:  map[string]interface{}{"@timestamp": "2026-08-14T16:31:00Z", "log": 42},
			wantErr: "log is absent or not a string",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePlatformLogEntry(Hit{Source: tc.source})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A genuinely blank line is not malformed: it is output the container produced.
func TestParsePlatformLogEntry_BlankLineIsValid(t *testing.T) {
	entry, err := ParsePlatformLogEntry(Hit{Source: map[string]interface{}{
		"@timestamp": "2026-08-14T16:31:00Z",
		"log":        "",
	}})
	if err != nil {
		t.Fatalf("a blank log line must parse, got %v", err)
	}
	if entry.Log != "" {
		t.Errorf("log = %q, want empty", entry.Log)
	}
}
