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
	for name, line := range map[string]*string{"one line": strPtr(lineUID), "every line": nil} {
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

			want := `label_openchoreo_dev_release_line_component_uid="comp-uid"`
			if line != nil {
				want = `label_openchoreo_dev_release_line_uid="` + lineUID + `"`
			}
			for _, q := range queries {
				if !strings.Contains(q, want) {
					t.Errorf("query without %s: %s", want, q)
				}
			}
			if got := w.Header().Get(releaseLineAppliedHeader); got != "true" {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", got)
			}
		})
	}
}

// An alert rule read back names its component, though a release line's expression does not.
func TestMapPrometheusRule_ReleaseLineComponentUID(t *testing.T) {
	const componentUID = "d2108e2c-f912-4cab-a0b4-71afde25c5ac"
	for name, lineUID := range map[string]string{"default line": "", "release line": "93f65294-926f-475b-98f1-dc4f152f38da"} {
		t.Run(name, func(t *testing.T) {
			pr, err := prometheus.BuildPrometheusRule(prometheus.AlertRuleParams{
				Name: "rule", Namespace: "ns", ComponentUID: componentUID, ReleaseLineUID: lineUID,
				ProjectUID: "31e18226-49ce-48e7-a09b-d1f764d61a19", EnvironmentUID: "7d1b44c7-d5f5-4bbd-a659-015139e98e0e",
				Metric: prometheus.MetricTypeCPUUsage, Enabled: true, Window: "5m", Interval: "1m", Operator: "gt", Threshold: 80,
			}, "monitoring")
			if err != nil {
				t.Fatalf("build rule: %v", err)
			}
			resp, err := mapPrometheusRuleToAlertRuleResponse(pr, "rule")
			if err != nil {
				t.Fatalf("map rule: %v", err)
			}
			if resp.Metadata.ComponentUid == nil || resp.Metadata.ComponentUid.String() != componentUID {
				t.Errorf("component UID = %v, want %s", resp.Metadata.ComponentUid, componentUID)
			}
		})
	}
}
