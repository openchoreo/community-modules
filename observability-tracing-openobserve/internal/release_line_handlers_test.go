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
	"sync"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/openobserve"
)

// newReleaseLineTestHandler serves a traces stream that has the release line columns and an empty
// search result, and records every search body.
func newReleaseLineTestHandler(t *testing.T) (*TracingHandler, func() string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/schema") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"schema": []map[string]string{
				{"name": "service_openchoreo_dev_component_uid"},
				{"name": "service_openchoreo_dev_release_line_uid"},
				{"name": "service_openchoreo_dev_release_line_component_uid"},
			}})
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(openobserve.OpenObserveResponse{Took: 1})
	}))
	t.Cleanup(server.Close)
	client := openobserve.NewClient(server.URL, "default", "default", "admin", "pass", testLogger())
	return NewTracingHandler(client, testLogger()), func() string {
		mu.Lock()
		defer mu.Unlock()
		defer func() { bodies = nil }()
		return strings.Join(bodies, "\n")
	}
}

func TestTraceHandlers_ReleaseLineHeader(t *testing.T) {
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	component := "comp-uid"
	for name, tt := range map[string]struct {
		line         *string
		allLines     *bool
		wantInTraces string
		wantInSpans  string
	}{
		"one line": {
			line:         ptr(lineUID),
			wantInTraces: "service_openchoreo_dev_release_line_uid = '" + lineUID + "'",
			wantInSpans:  "(service_openchoreo_dev_release_line_uid = '" + lineUID + "' OR ",
		},
		"every line": {
			allLines:     ptr(true),
			wantInTraces: "service_openchoreo_dev_release_line_component_uid = 'comp-uid'",
			wantInSpans:  "WHERE trace_id = 'trace-1' ORDER BY",
		},
		"main line": {
			wantInTraces: "service_openchoreo_dev_release_line_uid IS NULL",
			wantInSpans:  "service_openchoreo_dev_release_line_component_uid != 'comp-uid'",
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := &gen.TracesQueryRequest{
				StartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				EndTime:   time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
				SearchScope: gen.ComponentSearchScope{
					Namespace: "default", Component: &component, ReleaseLine: tt.line, AllReleaseLines: tt.allLines,
				},
			}
			handler, sent := newReleaseLineTestHandler(t)

			traces, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: body})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tracesResp, ok := traces.(gen.QueryTraces200JSONResponse)
			if !ok {
				t.Fatalf("expected 200 traces response, got %T", traces)
			}
			if got := tracesResp.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("traces header = %q, want true", got)
			}
			if s := sent(); !strings.Contains(s, tt.wantInTraces) {
				t.Errorf("the line scope did not reach the traces query, want %s in %s", tt.wantInTraces, s)
			}

			spans, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{TraceId: "trace-1", Body: body})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			spansResp, ok := spans.(gen.QuerySpansForTrace200JSONResponse)
			if !ok {
				t.Fatalf("expected 200 spans response, got %T", spans)
			}
			if got := spansResp.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("spans header = %q, want true", got)
			}
			if s := sent(); !strings.Contains(s, tt.wantInSpans) {
				t.Errorf("spans query = %s, want %s", s, tt.wantInSpans)
			}
		})
	}
}

func TestQueryTraces_NoComponentNoReleaseLineHeader(t *testing.T) {
	handler, _ := newReleaseLineTestHandler(t)
	resp, err := handler.QueryTraces(context.Background(), gen.QueryTracesRequestObject{Body: &gen.TracesQueryRequest{
		StartTime:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndTime:     time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
		SearchScope: gen.ComponentSearchScope{Namespace: "default", AllReleaseLines: ptr(true)},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.QueryTraces200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty", got)
	}
}
