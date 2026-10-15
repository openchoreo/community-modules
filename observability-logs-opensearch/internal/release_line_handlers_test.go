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

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
	osearch "github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

func TestQueryLogs_ReleaseLineHeader(t *testing.T) {
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	for name, line := range map[string]*string{"with a line": ptr(lineUID), "without a line": nil} {
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
				Namespace: "default", ComponentUid: ptr("comp-uid"), ReleaseLineUid: line,
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

			wantHeader := ""
			if line != nil {
				wantHeader = "true"
				if !strings.Contains(searchBody, lineUID) {
					t.Errorf("the release line did not reach the OpenSearch query: %s", searchBody)
				}
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, wantHeader)
			}
		})
	}
}
