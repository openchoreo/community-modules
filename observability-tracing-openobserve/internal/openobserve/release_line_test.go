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
	tests := []struct {
		name  string
		scope Scope
		want  []string
	}{
		{name: "no component or line", scope: Scope{stored: everyLineColumn}},
		{name: "every line without a component", scope: Scope{AllReleaseLines: true, stored: everyLineColumn}},
		{name: "one line matches on its UID alone",
			scope: Scope{ComponentID: "comp", ReleaseLineID: "next", stored: everyLineColumn},
			want:  []string{"service_openchoreo_dev_release_line_uid = 'next'"}},
		{name: "one line takes precedence over every line",
			scope: Scope{ComponentID: "comp", ReleaseLineID: "next", AllReleaseLines: true, stored: everyLineColumn},
			want:  []string{"service_openchoreo_dev_release_line_uid = 'next'"}},
		{name: "main line",
			scope: Scope{ComponentID: "comp", stored: everyLineColumn},
			want:  []string{"service_openchoreo_dev_component_uid = 'comp'", "service_openchoreo_dev_release_line_uid IS NULL"}},
		{name: "main line before any span carries a line",
			scope: Scope{ComponentID: "comp"},
			want:  []string{"service_openchoreo_dev_component_uid = 'comp'"}},
		{name: "every line",
			scope: Scope{ComponentID: "comp", AllReleaseLines: true, stored: everyLineColumn},
			want: []string{"(service_openchoreo_dev_component_uid = 'comp' OR " +
				"service_openchoreo_dev_release_line_component_uid = 'comp')"}},
		{name: "every line before any span carries a line",
			scope: Scope{ComponentID: "comp", AllReleaseLines: true},
			want:  []string{"service_openchoreo_dev_component_uid = 'comp'"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := releaseLineConditions(tt.scope); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func sqlFor(t *testing.T, generate func(TracesQueryParams, string, *slog.Logger) ([]byte, error), params TracesQueryParams) string {
	t.Helper()
	raw, err := generate(params, "default", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var query struct {
		Query struct {
			SQL string `json:"sql"`
		} `json:"query"`
	}
	if err := json.Unmarshal(raw, &query); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return query.Query.SQL
}

func TestTraceQueries_ReleaseLine(t *testing.T) {
	for name, generate := range map[string]func(TracesQueryParams, string, *slog.Logger) ([]byte, error){
		"traces list":  generateTracesListQuery,
		"traces count": generateTracesCountQuery,
	} {
		t.Run(name, func(t *testing.T) {
			params := TracesQueryParams{
				StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				EndTime:   time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
				Scope:     Scope{Namespace: "default", ComponentID: "comp-uid", stored: everyLineColumn},
			}
			if sql := sqlFor(t, generate, params); !strings.Contains(sql, "service_openchoreo_dev_component_uid = 'comp-uid' AND "+
				"service_openchoreo_dev_release_line_uid IS NULL") {
				t.Errorf("a component query must cover its main line only: %s", sql)
			}

			params.Scope.AllReleaseLines = true
			if sql := sqlFor(t, generate, params); !strings.Contains(sql, "service_openchoreo_dev_release_line_component_uid = 'comp-uid'") ||
				strings.Contains(sql, "IS NULL") {
				t.Errorf("an every line query must cover its release lines' spans: %s", sql)
			}

			params.Scope.ReleaseLineID = testReleaseLineID
			if sql := sqlFor(t, generate, params); !strings.Contains(sql, "service_openchoreo_dev_release_line_uid = '"+testReleaseLineID+"'") ||
				strings.Contains(sql, "component_uid") {
				t.Errorf("a release line query must match the line UID alone: %s", sql)
			}
		})
	}
}

// The spans of a trace keep other components' spans, and narrow the scope's component to the
// requested line.
func TestSpanConditions(t *testing.T) {
	const (
		otherComponent = "(service_openchoreo_dev_component_uid IS NULL OR service_openchoreo_dev_component_uid != 'comp-uid')"
		notLineOf      = "(service_openchoreo_dev_release_line_component_uid IS NULL OR service_openchoreo_dev_release_line_component_uid != 'comp-uid')"
	)
	traceID := "trace_id = 'trace-1'"
	tests := []struct {
		name  string
		scope Scope
		want  []string
	}{
		{name: "main line leaves out the component's release lines",
			scope: Scope{ComponentID: "comp-uid", stored: everyLineColumn},
			want:  []string{traceID, notLineOf}},
		{name: "main line before any span carries a line",
			scope: Scope{ComponentID: "comp-uid"},
			want:  []string{traceID}},
		{name: "one line leaves out the component's other lines",
			scope: Scope{ComponentID: "comp-uid", ReleaseLineID: testReleaseLineID, stored: everyLineColumn},
			want: []string{traceID, "(service_openchoreo_dev_release_line_uid = '" + testReleaseLineID + "' OR (" +
				otherComponent + " AND " + notLineOf + "))"}},
		{name: "one line takes precedence over every line",
			scope: Scope{ComponentID: "comp-uid", ReleaseLineID: testReleaseLineID, AllReleaseLines: true, stored: lineColumns{line: true}},
			want:  []string{traceID, "(service_openchoreo_dev_release_line_uid = '" + testReleaseLineID + "' OR (" + otherComponent + "))"}},
		{name: "one line without a component",
			scope: Scope{ReleaseLineID: testReleaseLineID, stored: everyLineColumn},
			want:  []string{traceID, "service_openchoreo_dev_release_line_uid = '" + testReleaseLineID + "'"}},
		{name: "every line keeps the whole trace",
			scope: Scope{ComponentID: "comp-uid", AllReleaseLines: true, stored: everyLineColumn},
			want:  []string{traceID}},
		{name: "no component keeps the whole trace",
			scope: Scope{stored: everyLineColumn},
			want:  []string{traceID}},
		{name: "values are escaped",
			scope: Scope{ComponentID: "it's", stored: everyLineColumn},
			want: []string{traceID, "(service_openchoreo_dev_release_line_component_uid IS NULL OR " +
				"service_openchoreo_dev_release_line_component_uid != 'it''s')"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := spanConditions(TracesQueryParams{TraceID: "trace-1", Scope: tt.scope})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSpanQueries_UseSpanConditions(t *testing.T) {
	params := TracesQueryParams{
		StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
		TraceID:   "trace-1",
		Scope:     Scope{ComponentID: "comp-uid", ReleaseLineID: testReleaseLineID, stored: everyLineColumn},
	}
	want := "WHERE " + strings.Join(spanConditions(params), " AND ")
	for name, generate := range map[string]func(TracesQueryParams, string, *slog.Logger) ([]byte, error){
		"spans list":  generateSpansListQuery,
		"spans count": generateSpansCountQuery,
	} {
		if sql := sqlFor(t, generate, params); !strings.Contains(sql, want) {
			t.Errorf("%s = %s, want %s", name, sql, want)
		}
	}
}

// schemaServer fakes OpenObserve with a traces stream schema of the given columns and records
// every search.
func schemaServer(t *testing.T, status int, columns ...string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var searches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/default/streams/default/schema" {
			if r.URL.Query().Get("type") != "traces" {
				t.Errorf("unexpected schema stream type: %s", r.URL.RawQuery)
			}
			w.WriteHeader(status)
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

// The stream's release line columns are cached: a stream lacking a column is asked again after
// lineColumnsRecheck, one with both after lineColumnsRefresh, and a failed lookup uses the last
// answer.
func TestStoredLineColumns_Cache(t *testing.T) {
	var (
		mu      sync.Mutex
		columns = []string{componentUIDColumn}
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
		got, err := client.storedLineColumns(context.Background())
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
	columns = append(columns, releaseLineUIDColumn, releaseLineComponentUIDColumn)
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

	if _, err := newTestClient(server.URL).storedLineColumns(context.Background()); err == nil {
		t.Error("a failed lookup without a cached answer must fail")
	}
}

func TestGetTraces_ReleaseLineColumns(t *testing.T) {
	for name, tt := range map[string]struct {
		status  int
		columns []string
		want    string
	}{
		"stored":                       {status: http.StatusOK, columns: []string{componentUIDColumn, releaseLineUIDColumn}, want: "IS NULL"},
		"before any span carries line": {status: http.StatusOK, columns: []string{componentUIDColumn}},
		"no stream yet":                {status: http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			server, searches := schemaServer(t, tt.status, tt.columns...)
			client := newTestClient(server.URL)
			params := TracesQueryParams{
				StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
				Scope: Scope{Namespace: "default", ComponentID: "comp-uid"},
			}
			if _, err := client.GetTraces(context.Background(), params); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, s := range searches() {
				if got := strings.Contains(s, "release_line"); got != (tt.want != "") || !strings.Contains(s, tt.want) {
					t.Errorf("main line query = %s, want release line condition %q", s, tt.want)
				}
			}

			before := len(searches())
			params.Scope.ReleaseLineID = testReleaseLineID
			traces, err := client.GetTraces(context.Background(), params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			spans, err := client.GetSpans(context.Background(), TracesQueryParams{TraceID: "trace-1", Scope: params.Scope})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(traces.Traces) != 0 || len(spans.Spans) != 0 {
				t.Errorf("expected no traces or spans, got %+v and %+v", traces, spans)
			}
			if ran := len(searches()) - before; tt.want == "" && ran != 0 {
				t.Errorf("%d release line queries ran though no span carries a line", ran)
			}
		})
	}
}
