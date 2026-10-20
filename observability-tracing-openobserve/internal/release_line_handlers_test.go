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

// newReleaseLineTestHandler serves an empty OpenObserve result and records every search body.
func newReleaseLineTestHandler(t *testing.T) (*TracingHandler, func() string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
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
	for name, line := range map[string]*string{"with a line": ptr(lineUID), "without a line": nil} {
		t.Run(name, func(t *testing.T) {
			body := &gen.TracesQueryRequest{
				StartTime:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				EndTime:     time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
				SearchScope: gen.ComponentSearchScope{Namespace: "default", Component: &component, ReleaseLine: line},
			}
			wantHeader := ""
			if line != nil {
				wantHeader = "true"
			}
			checkSent := func(kind, sent string) {
				t.Helper()
				hasLine := strings.Contains(sent, "service_openchoreo_dev_release_line_uid = '"+lineUID+"'")
				if line != nil && !hasLine {
					t.Errorf("the release line did not reach the %s query: %s", kind, sent)
				}
				if line == nil && strings.Contains(sent, "release_line_uid") {
					t.Errorf("%s query without a release line filters on one: %s", kind, sent)
				}
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
			if got := tracesResp.Headers.OpenChoreoReleaseLineApplied; got != wantHeader {
				t.Errorf("traces header = %q, want %q", got, wantHeader)
			}
			checkSent("traces", sent())

			spans, err := handler.QuerySpansForTrace(context.Background(), gen.QuerySpansForTraceRequestObject{TraceId: "trace-1", Body: body})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			spansResp, ok := spans.(gen.QuerySpansForTrace200JSONResponse)
			if !ok {
				t.Fatalf("expected 200 spans response, got %T", spans)
			}
			if got := spansResp.Headers.OpenChoreoReleaseLineApplied; got != wantHeader {
				t.Errorf("spans header = %q, want %q", got, wantHeader)
			}
			checkSent("spans", sent())
		})
	}
}
