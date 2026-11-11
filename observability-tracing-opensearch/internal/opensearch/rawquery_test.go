// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
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
)

func prepare(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	out, err := PrepareRawQuery([]byte(body), 30*time.Second)
	if err != nil {
		t.Fatalf("PrepareRawQuery: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("prepared body is not JSON: %v", err)
	}
	return top
}

func TestPrepareRawQuery_KeepsTheBodyAsWritten(t *testing.T) {
	top := prepare(t, `{
		"query": {"bool": {"filter": [
			{"range": {"startTime": {"gte": "2026-10-20T08:00:00Z", "lte": "2026-10-20T09:00:00Z"}}},
			{"term": {"status.code": "Error"}}
		]}},
		"size": 50,
		"sort": [{"startTime": "desc"}],
		"aggs": {"by_service": {"terms": {"field": "resource.service.name", "size": 20}}},
		"collapse": {"field": "traceId"}
	}`)

	for key, want := range map[string]string{
		"query": `{"bool": {"filter": [
			{"range": {"startTime": {"gte": "2026-10-20T08:00:00Z", "lte": "2026-10-20T09:00:00Z"}}},
			{"term": {"status.code": "Error"}}
		]}}`,
		"size":     `50`,
		"sort":     `[{"startTime": "desc"}]`,
		"aggs":     `{"by_service": {"terms": {"field": "resource.service.name", "size": 20}}}`,
		"collapse": `{"field": "traceId"}`,
		"timeout":  `"30000ms"`,
	} {
		if compact(t, string(top[key])) != compact(t, want) {
			t.Errorf("%s = %s, want %s", key, top[key], want)
		}
	}
}

func TestPrepareRawQuery_KeepsLargeNumbersExact(t *testing.T) {
	top := prepare(t, `{"query": {"range": {"startTime": {"gte": 1792404839161624162}}}}`)
	if !strings.Contains(string(top["query"]), "1792404839161624162") {
		t.Errorf("number lost precision: %s", top["query"])
	}
}

func TestPrepareRawQuery_AddsNoQuery(t *testing.T) {
	top := prepare(t, `{"size": 0, "aggs": {"n": {"value_count": {"field": "traceId"}}}}`)
	if _, ok := top["query"]; ok {
		t.Errorf("no query expected: %s", top["query"])
	}
}

func TestPrepareRawQuery_OverridesTheTimeout(t *testing.T) {
	top := prepare(t, `{"query": {"match_all": {}}, "timeout": "10m"}`)
	if string(top["timeout"]) != `"30000ms"` {
		t.Errorf("timeout = %s", top["timeout"])
	}
}

func TestPrepareRawQuery_Rejects(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"not an object", `"SELECT 1"`, "the request must be an OpenSearch search body"},
		{"not JSON", `SELECT 1`, "the request must be an OpenSearch search body"},
		{"unknown top-level key", `{"query": {"match_all": {}}, "pit": {"id": "x"}}`, `"pit" is not allowed`},
		{"global aggregation", `{"aggs": {"all": {"global": {}, "aggs": {"n": {"value_count": {"field": "traceId"}}}}}}`,
			"aggs.all.global: global aggregations are not allowed, as they ignore the query"},
		{"nested global aggregation", `{"aggregations": {"a": {"terms": {"field": "traceId"}, "aggs": {"b": {"global": {}}}}}}`,
			"aggregations.a.aggs.b.global"},
		{"terms lookup", `{"query": {"terms": {"traceId": {"index": "logs-2026", "id": "1", "path": "ids"}}}}`,
			"query.terms.traceId.index: raw queries can only read the trace indices"},
		{"more_like_this from another index", `{"query": {"more_like_this": {"fields": ["name"], "like": [{"_index": "audit-logs", "_id": "1"}]}}}`,
			"query.more_like_this.like[0]._index"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PrepareRawQuery([]byte(tt.body), 30*time.Second)
			var invalid *InvalidRawQueryError
			if !errors.As(err, &invalid) {
				t.Fatalf("want InvalidRawQueryError, got %v", err)
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.HasPrefix(err.Error(), "invalid OpenSearch query DSL: ") {
				t.Errorf("error %q should contain %q", err, tt.want)
			}
		})
	}
}

func newRawSearchClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client, err := NewClient(srv.URL, "admin", "pass", true, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRawSearch_SendsToTheTraceIndicesAndReturnsTheBody(t *testing.T) {
	const response = `{"took":5,"timed_out":false,"hits":{"total":{"value":1},"hits":[]}}`
	var gotPath, gotQuery string
	client := newRawSearchClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = io.WriteString(w, response)
	})

	res, err := client.RawSearch(context.Background(), "otel-traces-*", []byte(`{}`), 30*time.Second, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/otel-traces-*/_search" {
		t.Errorf("path = %s", gotPath)
	}
	for _, p := range []string{"cancel_after_time_interval=30000ms", "ignore_unavailable=true", "allow_no_indices=true"} {
		if !strings.Contains(gotQuery, p) {
			t.Errorf("query string %q missing %q", gotQuery, p)
		}
	}
	if string(res) != response {
		t.Errorf("the response must be OpenSearch's, unchanged: %s", res)
	}
}

func TestRawSearch_Errors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, err error)
	}{
		{"OpenSearch rejects the query", http.StatusBadRequest,
			`{"error":{"root_cause":[{"type":"parsing_exception","reason":"unknown query [termz]"}],"type":"parsing_exception"},"status":400}`,
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				if !errors.As(err, &invalid) || invalid.Error() != "invalid OpenSearch query DSL: parsing_exception: unknown query [termz]" {
					t.Errorf("got %v", err)
				}
			}},
		{"timed out with partial results", http.StatusOK, `{"took":30000,"timed_out":true,"hits":{"hits":[]}}`,
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrRawQueryTimeout) {
					t.Errorf("got %v", err)
				}
			}},
		{"too large", http.StatusOK, `{"took":1,"timed_out":false,"hits":{"hits":["` + strings.Repeat("x", 2048) + `"]}}`,
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrRawQueryTooLarge) {
					t.Errorf("got %v", err)
				}
			}},
		{"an oversized rejection is still a rejection", http.StatusBadRequest,
			`{"error":{"root_cause":[{"type":"parsing_exception","reason":"` + strings.Repeat("x", 2048) + `"}]},"status":400}`,
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				if !errors.As(err, &invalid) || errors.Is(err, ErrRawQueryTooLarge) {
					t.Errorf("got %v", err)
				}
			}},
		{"read-only user not set up", http.StatusUnauthorized, `Unauthorized`,
			func(t *testing.T, err error) {
				var invalid *InvalidRawQueryError
				if err == nil || errors.As(err, &invalid) || !strings.Contains(err.Error(), "credentials") {
					t.Errorf("got %v", err)
				}
			}},
		{"server error", http.StatusInternalServerError, `{"error":{"type":"x","reason":"boom"}}`,
			func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "status 500") {
					t.Errorf("got %v", err)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newRawSearchClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := client.RawSearch(context.Background(), "otel-traces-*", []byte(`{}`), time.Second, 1024)
			tt.check(t, err)
		})
	}
}

func TestRawSearch_ContextDeadline(t *testing.T) {
	client := newRawSearchClient(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.RawSearch(ctx, "otel-traces-*", []byte(`{}`), time.Second, 1024)
	if !errors.Is(err, ErrRawQueryTimeout) {
		t.Errorf("got %v", err)
	}
}

func compact(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %s", s)
	}
	out, _ := json.Marshal(v)
	return string(out)
}
