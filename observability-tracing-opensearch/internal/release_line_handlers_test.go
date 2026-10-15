// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/opensearch"
)

// newReleaseLineTestHandler serves an empty OpenSearch result and records the last search body.
func newReleaseLineTestHandler(t *testing.T, searchBody *string) *TracingHandler {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*searchBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"took":1,"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`))
	}))
	t.Cleanup(server.Close)
	client, err := opensearch.NewClient(server.URL, "", "", true, testLogger())
	if err != nil {
		t.Fatalf("failed to create opensearch client: %v", err)
	}
	return NewTracingHandler(client, testLogger())
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

			var searchBody string
			handler := newReleaseLineTestHandler(t, &searchBody)

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
			if line != nil && !strings.Contains(searchBody, lineUID) {
				t.Errorf("the release line did not reach the traces query: %s", searchBody)
			}

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
			if line != nil && !strings.Contains(searchBody, lineUID) {
				t.Errorf("the release line did not reach the spans query: %s", searchBody)
			}
		})
	}
}
