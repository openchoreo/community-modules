// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/prometheus"
)

func TestQueryMetrics_ReleaseLineHeader(t *testing.T) {
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	for name, line := range map[string]*string{"with a line": strPtr(lineUID), "without a line": nil} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var queries []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				mu.Lock()
				queries = append(queries, r.Form.Get("query"))
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
			}))
			defer server.Close()
			promClient, err := prometheus.NewClient(server.URL, testLogger())
			if err != nil {
				t.Fatalf("failed to create prometheus client: %v", err)
			}
			handler := NewMetricsHandler(promClient, nil, nil, "test-ns", testLogger())

			component := "comp-uid"
			resp, err := handler.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &gen.MetricsQueryRequest{
				Metric:      gen.MetricsQueryRequestMetricResource,
				StartTime:   time.Now().Add(-time.Hour),
				EndTime:     time.Now(),
				SearchScope: gen.ComponentSearchScope{Namespace: "default", ComponentUid: &component, ReleaseLineUid: line},
			}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			w := httptest.NewRecorder()
			if err := resp.VisitQueryMetricsResponse(w); err != nil {
				t.Fatalf("failed to write response: %v", err)
			}

			wantHeader := ""
			if line != nil {
				wantHeader = "true"
				for _, q := range queries {
					if !strings.Contains(q, `label_openchoreo_dev_release_line_uid="`+lineUID+`"`) {
						t.Errorf("query without the release line matcher: %s", q)
					}
				}
			}
			if got := w.Header().Get(releaseLineAppliedHeader); got != wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, wantHeader)
			}
		})
	}
}
