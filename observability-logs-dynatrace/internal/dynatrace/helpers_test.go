// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeGrail is an httptest server that answers query:execute with canned records chosen
// by a matcher over the DQL text, and records every query it receives.
type fakeGrail struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	queries []executeRequest
	answer  func(q string) ([]map[string]any, int)
}

func newFakeGrail(t *testing.T, answer func(q string) ([]map[string]any, int)) *fakeGrail {
	f := &fakeGrail{t: t, answer: answer}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != executePath {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer t" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		var req executeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		f.mu.Lock()
		f.queries = append(f.queries, req)
		f.mu.Unlock()

		records, status := f.answer(req.Query)
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"PARSE_ERROR","details":{"errorMessage":"bad dql"}}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"state":  stateSucceeded,
			"result": map[string]any{"records": records},
		})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGrail) client() *Client {
	return NewClient(Config{
		PlatformURL:         f.srv.URL,
		ContainerLogsSource: "oc-logs",
		AuditLogsSource:     "oc-audit",
		QueryTimeout:        5 * time.Second,
	}, StaticToken("t"), f.srv.Client(), discardLogger())
}

func (f *fakeGrail) queryTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.queries))
	for _, q := range f.queries {
		out = append(out, q.Query)
	}
	return out
}

// isCount reports whether a query is a count or distinct-count query.
func isCount(q string) bool {
	return strings.Contains(q, "summarize total")
}

// total is a count query's answer. Grail serializes longs as strings.
func total(n int) []map[string]any {
	return []map[string]any{{"total": strconv.Itoa(n)}}
}
