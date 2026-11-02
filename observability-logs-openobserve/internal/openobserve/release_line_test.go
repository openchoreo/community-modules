// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const testReleaseLineID = "93f65294-926f-475b-98f1-dc4f152f38da"

var everyLineColumn = lineColumns{line: true, lineComponent: true}

func TestReleaseLineConditions(t *testing.T) {
	f := releaseLineFields{component: "c_uid", line: "l_uid", lineComponent: "lc_uid"}
	tests := []struct {
		name         string
		stored       lineColumns
		componentIDs []string
		lineID       string
		allLines     bool
		want         []string
	}{
		{name: "no component or line", stored: everyLineColumn},
		{name: "every line without a component", stored: everyLineColumn, allLines: true},
		{name: "one line matches on its UID alone", stored: everyLineColumn, componentIDs: []string{"comp"}, lineID: "next",
			want: []string{"l_uid = 'next'"}},
		{name: "one line takes precedence over every line", stored: everyLineColumn, componentIDs: []string{"comp"}, lineID: "next", allLines: true,
			want: []string{"l_uid = 'next'"}},
		{name: "main line", stored: everyLineColumn, componentIDs: []string{"comp"},
			want: []string{"c_uid = 'comp'", "l_uid IS NULL"}},
		{name: "main line before any record carries a line", componentIDs: []string{"comp"},
			want: []string{"c_uid = 'comp'"}},
		{name: "every line", stored: everyLineColumn, componentIDs: []string{"comp"}, allLines: true,
			want: []string{"(c_uid = 'comp' OR lc_uid = 'comp')"}},
		{name: "every line before any record carries a line", componentIDs: []string{"comp"}, allLines: true,
			want: []string{"c_uid = 'comp'"}},
		{name: "main lines of several components", stored: everyLineColumn, componentIDs: []string{"a", "b"},
			want: []string{"(c_uid = 'a' OR c_uid = 'b')", "l_uid IS NULL"}},
		{name: "values are escaped", stored: everyLineColumn, lineID: "it's",
			want: []string{"l_uid = 'it''s'"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := releaseLineConditions(f, tt.stored, tt.componentIDs, tt.lineID, tt.allLines)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateComponentLogsQuery_ReleaseLine(t *testing.T) {
	base := ComponentLogsParams{
		Namespace: "test-ns", ComponentIDs: []string{"comp-uid"},
		StartTime: evStart, EndTime: evEnd, Limit: 10, stored: everyLineColumn,
	}
	tests := map[string]struct {
		edit    func(*ComponentLogsParams)
		want    []string
		notWant []string
	}{
		"main line": {
			edit: func(*ComponentLogsParams) {},
			want: []string{
				"kubernetes_labels_openchoreo_dev_component_uid = 'comp-uid'",
				"kubernetes_labels_openchoreo_dev_release_line_uid IS NULL",
			},
			notWant: []string{"release_line_component_uid"},
		},
		"every line": {
			edit: func(p *ComponentLogsParams) { p.AllReleaseLines = true },
			want: []string{"(kubernetes_labels_openchoreo_dev_component_uid = 'comp-uid' OR " +
				"kubernetes_labels_openchoreo_dev_release_line_component_uid = 'comp-uid')"},
			notWant: []string{"IS NULL"},
		},
		"one line": {
			edit:    func(p *ComponentLogsParams) { p.ReleaseLineID = testReleaseLineID },
			want:    []string{"kubernetes_labels_openchoreo_dev_release_line_uid = '" + testReleaseLineID + "'"},
			notWant: []string{"component_uid"},
		},
	}
	generators := map[string]func(ComponentLogsParams, string, *slog.Logger) ([]byte, error){
		"query": generateComponentLogsQuery,
		"count": generateComponentLogsCountQuery,
	}
	for name, tt := range tests {
		for genName, generate := range generators {
			t.Run(name+"/"+genName, func(t *testing.T) {
				params := base
				tt.edit(&params)
				raw, err := generate(params, "default", testLogger())
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				sql, _ := sqlOf(t, raw)
				for _, w := range tt.want {
					if !strings.Contains(sql, w) {
						t.Errorf("missing %q in %s", w, sql)
					}
				}
				for _, nw := range tt.notWant {
					if strings.Contains(sql, nw) {
						t.Errorf("unexpected %q in %s", nw, sql)
					}
				}
			})
		}
	}
}

func TestGenerateComponentEventsQuery_ReleaseLine(t *testing.T) {
	params := EventsQueryParams{
		Namespace: "default", ComponentID: "comp-uid",
		StartTime: evStart, EndTime: evEnd, Limit: 50, stored: everyLineColumn,
	}
	sqlFor := func(p EventsQueryParams) string {
		t.Helper()
		raw, err := generateComponentEventsQuery(p, "k8s_events", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := sqlOf(t, raw)
		return sql
	}

	if sql := sqlFor(params); !strings.Contains(sql, "k8s_object_label_openchoreo_dev_component_uid = 'comp-uid' AND "+
		"k8s_object_label_openchoreo_dev_release_line_uid IS NULL") {
		t.Errorf("a component query must cover its main line only: %s", sql)
	}

	params.AllReleaseLines = true
	if sql := sqlFor(params); !strings.Contains(sql, "(k8s_object_label_openchoreo_dev_component_uid = 'comp-uid' OR "+
		"k8s_object_label_openchoreo_dev_release_line_component_uid = 'comp-uid')") || strings.Contains(sql, "IS NULL") {
		t.Errorf("an every line query must cover its main line's and release lines' objects: %s", sql)
	}

	params.ReleaseLineID = testReleaseLineID
	sql := sqlFor(params)
	if !strings.Contains(sql, "k8s_object_label_openchoreo_dev_release_line_uid = '"+testReleaseLineID+"'") ||
		strings.Contains(sql, "component_uid") {
		t.Errorf("a release line query must match the line UID alone: %s", sql)
	}
}

func TestGenerateAlertConfig_ReleaseLine(t *testing.T) {
	enabled := true
	name := "rule"
	params := LogAlertParams{
		Name: &name, Namespace: "ns", ProjectUID: "proj-uid", EnvironmentUID: "env-uid", ComponentUID: "comp-uid",
		SearchPattern: "error", Operator: "gt", ThresholdValue: 1, Window: "5m", Interval: "1m", Enabled: &enabled,
	}
	alert := func(p LogAlertParams) (string, map[string]interface{}) {
		t.Helper()
		raw, err := generateAlertConfig(p, "default", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var cfg map[string]interface{}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		sql, _ := cfg["query_condition"].(map[string]interface{})["sql"].(string)
		attrs, _ := cfg["context_attributes"].(map[string]interface{})
		return sql, attrs
	}

	sql, attrs := alert(params)
	if !strings.HasSuffix(sql, "AND kubernetes_labels_openchoreo_dev_component_uid = 'comp-uid'") {
		t.Errorf("a main line rule created before any record carries a line matches the component UID: %s", sql)
	}
	if _, ok := attrs["releaseLineUid"]; ok {
		t.Errorf("a main line rule stores a release line: %v", attrs)
	}

	params.stored = everyLineColumn
	sql, _ = alert(params)
	if !strings.HasSuffix(sql, "AND kubernetes_labels_openchoreo_dev_component_uid = 'comp-uid' AND "+
		"kubernetes_labels_openchoreo_dev_release_line_uid IS NULL") {
		t.Errorf("a main line rule must exclude release line records: %s", sql)
	}

	params.ReleaseLineUID = testReleaseLineID
	sql, attrs = alert(params)
	if !strings.HasSuffix(sql, "AND kubernetes_labels_openchoreo_dev_release_line_uid = '"+testReleaseLineID+"'") ||
		strings.Contains(sql, "component_uid") {
		t.Errorf("a release line rule must match the line UID alone: %s", sql)
	}
	if got := ExtractSearchPattern(sql); got != "error" {
		t.Errorf("search pattern = %q, want %q", got, "error")
	}
	if attrs["releaseLineUid"] != testReleaseLineID || attrs["componentUid"] != "comp-uid" {
		t.Errorf("context_attributes = %v, want the line and its component", attrs)
	}
}

// schemaServer fakes OpenObserve with a stream schema of the given columns and records every search.
func schemaServer(t *testing.T, columns ...string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/schema") {
			fields := make([]map[string]string, 0, len(columns))
			for _, c := range columns {
				fields = append(fields, map[string]string{"name": c, "type": "Utf8"})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"schema": fields})
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		searches = append(searches, string(b))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(OpenObserveResponse{Hits: []map[string]interface{}{}})
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), searches...)
	}
}

// A stream's release line columns are cached: one lacking a column is asked again after
// lineColumnsRecheck, one with both after lineColumnsRefresh, and a failed lookup uses the last
// answer.
func TestStoredLineColumns_Cache(t *testing.T) {
	var (
		mu      sync.Mutex
		columns = []string{colComponentID}
		failing bool
		lookups int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		lookups++
		if failing {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fields := make([]map[string]string, 0, len(columns))
		for _, c := range columns {
			fields = append(fields, map[string]string{"name": c})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"schema": fields})
	}))
	t.Cleanup(server.Close)

	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	client := newTestClient(server.URL)
	client.lineColumns.now = func() time.Time { return now }
	lookup := func(wantLookups int, want lineColumns) {
		t.Helper()
		got, err := client.storedLineColumns(context.Background(), "default", logReleaseLineFields)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if got != want || lookups != wantLookups {
			t.Errorf("got %+v after %d lookups, want %+v after %d", got, lookups, want, wantLookups)
		}
	}

	lookup(1, lineColumns{})
	lookup(1, lineColumns{})

	mu.Lock()
	columns = append(columns, logReleaseLineFields.line, logReleaseLineFields.lineComponent)
	mu.Unlock()
	now = now.Add(lineColumnsRecheck)
	lookup(2, everyLineColumn)

	now = now.Add(lineColumnsRefresh - time.Second)
	lookup(2, everyLineColumn)

	mu.Lock()
	failing = true
	mu.Unlock()
	now = now.Add(time.Second)
	lookup(3, everyLineColumn)

	if _, err := newTestClient(server.URL).storedLineColumns(context.Background(), "default", logReleaseLineFields); err == nil {
		t.Error("a failed lookup without a cached answer must fail")
	}
}

func TestGetComponentLogs_BeforeAnyRecordCarriesALine(t *testing.T) {
	server, searches := schemaServer(t, colComponentID)
	client := newTestClient(server.URL)
	params := ComponentLogsParams{
		Namespace: "test-ns", ComponentIDs: []string{"comp-uid"},
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
	}

	if _, err := client.GetComponentLogs(context.Background(), params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range searches() {
		if strings.Contains(s, "release_line") {
			t.Errorf("a query names a release line column the stream lacks: %s", s)
		}
	}

	params.ReleaseLineID = testReleaseLineID
	before := len(searches())
	result, err := client.GetComponentLogs(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Logs) != 0 || result.TotalCount != 0 {
		t.Errorf("expected no logs, got %+v", result)
	}
	if len(searches()) != before {
		t.Errorf("a release line query ran though no record carries a line")
	}
}

func TestGetComponentEvents_BeforeAnyEventCarriesALine(t *testing.T) {
	server, searches := schemaServer(t, evComponentID)
	client := newTestClient(server.URL)

	result, err := client.GetComponentEvents(context.Background(), EventsQueryParams{
		Namespace: "default", ComponentID: "comp-uid", ReleaseLineID: testReleaseLineID,
		StartTime: evStart, EndTime: evEnd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Events) != 0 || result.TotalCount != 0 || len(searches()) != 0 {
		t.Errorf("expected no events and no search, got %+v after %d searches", result, len(searches()))
	}
}

// A release line's log and event entries are attributed to their component like the main line's.
func TestParseEntries_ReleaseLineComponentUID(t *testing.T) {
	const componentUID = "d2108e2c-f912-4cab-a0b4-71afde25c5ac"
	tests := map[string]struct{ log, event map[string]interface{} }{
		"main line": {
			log:   map[string]interface{}{colComponentID: componentUID},
			event: map[string]interface{}{evComponentID: componentUID},
		},
		"release line": {
			log:   map[string]interface{}{colReleaseLineComponentID: componentUID, colReleaseLineID: testReleaseLineID},
			event: map[string]interface{}{evReleaseLineComponentID: componentUID, evReleaseLineID: testReleaseLineID},
		},
	}
	client := newTestClient("http://localhost")
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := client.parseApplicationLogEntry(0, tt.log).ComponentUID; got != componentUID {
				t.Errorf("log entry component UID = %q, want %q", got, componentUID)
			}
			if got := parseEventEntry(0, tt.event).ComponentID; got != componentUID {
				t.Errorf("event entry component UID = %q, want %q", got, componentUID)
			}
		})
	}
}

func TestAuditConditions_FiltersByReleaseLine(t *testing.T) {
	conditions, ok := auditConditions(AuditLogsParams{
		ResourceComponents:   []string{"hello"},
		ResourceReleaseLines: []string{"next"},
	}, auditColumns("resource_component", "resource_release_line"))
	if !ok {
		t.Fatal("expected the conditions to be buildable")
	}
	want := []string{`(resource_component = 'hello')`, `(resource_release_line = 'next')`}
	if !reflect.DeepEqual(conditions, want) {
		t.Errorf("got %v, want %v", conditions, want)
	}

	if source, ok := auditValuesSource("audit_logs", "resource.release_line", nil, auditColumns("resource_release_line")); !ok ||
		!strings.Contains(source, "resource_release_line as value") {
		t.Errorf("source = %q, want it to read resource_release_line", source)
	}
}

func TestParseAuditRecord_ReadsTheReleaseLine(t *testing.T) {
	line := strings.Replace(auditLine, `"resource":{`, `"resource":{"release_line":"next",`, 1)
	record, err := ParseAuditRecord(map[string]interface{}{"log": line})
	if err != nil {
		t.Fatal(err)
	}
	if record.Resource == nil || record.Resource.ReleaseLine != "next" {
		t.Errorf("resource = %+v, want release_line next", record.Resource)
	}
}
