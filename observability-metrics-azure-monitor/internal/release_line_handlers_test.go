// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/perfmetrics"
)

const (
	testComponentUID   = "3f9c2b1e-8a4d-4c6e-9b2f-1d7e5a0c4b8f"
	testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
)

func boolPtr(b bool) *bool { return &b }

func queryResourceMetrics(t *testing.T, fc *fakeClient, scope gen.ComponentSearchScope) *httptest.ResponseRecorder {
	t.Helper()
	resp, err := newHandler(fc).QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &gen.MetricsQueryRequest{
		Metric:      gen.MetricsQueryRequestMetricResource,
		SearchScope: scope,
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitQueryMetricsResponse(rec); err != nil {
		t.Fatalf("visit error: %v", err)
	}
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func TestQueryMetrics_ReleaseLineScope(t *testing.T) {
	cases := map[string]struct {
		scope      gen.ComponentSearchScope
		wantLine   string
		wantAll    bool
		wantHeader string
	}{
		"main line": {
			scope:      gen.ComponentSearchScope{Namespace: "ns", ComponentUid: strPtr(testComponentUID)},
			wantHeader: "true",
		},
		"every line": {
			scope:      gen.ComponentSearchScope{Namespace: "ns", ComponentUid: strPtr(testComponentUID), AllReleaseLines: boolPtr(true)},
			wantAll:    true,
			wantHeader: "true",
		},
		"one line": {
			scope:      gen.ComponentSearchScope{Namespace: "ns", ComponentUid: strPtr(testComponentUID), ReleaseLineUid: strPtr(testReleaseLineUID)},
			wantLine:   testReleaseLineUID,
			wantHeader: "true",
		},
		"no component": {
			scope:   gen.ComponentSearchScope{Namespace: "ns", AllReleaseLines: boolPtr(true)},
			wantAll: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fc := &fakeClient{result: &perfmetrics.ResourceMetricsResult{}}
			rec := queryResourceMetrics(t, fc, tc.scope)
			if fc.params.ReleaseLineUID != tc.wantLine || fc.params.AllReleaseLines != tc.wantAll {
				t.Errorf("release line scope not passed: line %q all %v", fc.params.ReleaseLineUID, fc.params.AllReleaseLines)
			}
			if got := rec.Header().Get("OpenChoreo-Release-Line-Applied"); got != tc.wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, tc.wantHeader)
			}
		})
	}
}

// HTTP metrics are not served, so a release line scope is not claimed for them.
func TestQueryMetrics_HTTPNeverSetsReleaseLineHeader(t *testing.T) {
	resp, err := newHandler(&fakeClient{}).QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &gen.MetricsQueryRequest{
		Metric:      gen.MetricsQueryRequestMetricHttp,
		SearchScope: gen.ComponentSearchScope{Namespace: "ns", ComponentUid: strPtr(testComponentUID), ReleaseLineUid: strPtr(testReleaseLineUID)},
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitQueryMetricsResponse(rec); err != nil {
		t.Fatalf("visit error: %v", err)
	}
	if got := rec.Header().Get("OpenChoreo-Release-Line-Applied"); got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q on an http query, want empty", got)
	}
}

func TestCreateAndUpdateAlertRule_ReleaseLineScope(t *testing.T) {
	componentUID := openapi_types.UUID{}
	if err := componentUID.Scan(testComponentUID); err != nil {
		t.Fatal(err)
	}
	lineUID := openapi_types.UUID{}
	if err := lineUID.Scan(testReleaseLineUID); err != nil {
		t.Fatal(err)
	}
	for name, line := range map[string]*openapi_types.UUID{"main line": nil, "one line": &lineUID} {
		t.Run(name, func(t *testing.T) {
			body := validAlertBody()
			body.Metadata.ComponentUid = componentUID
			body.Metadata.ReleaseLineUid = line
			wantLine := ""
			if line != nil {
				wantLine = testReleaseLineUID
			}

			ac := &fakeAlertClient{}
			h := newAlertingHandler(ac, &fakeObserver{})
			resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: body})
			created, ok := resp.(gen.CreateAlertRule201JSONResponse)
			if !ok {
				t.Fatalf("expected 201, got %T", resp)
			}
			if ac.lastCreated.ReleaseLineUID != wantLine {
				t.Errorf("release line not passed through: %q", ac.lastCreated.ReleaseLineUID)
			}
			if got := created.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("create: OpenChoreo-Release-Line-Applied = %q, want true", got)
			}

			resp2, _ := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
				RuleName: "high-cpu", Body: (*gen.UpdateAlertRuleJSONRequestBody)(body),
			})
			updated, ok := resp2.(gen.UpdateAlertRule200JSONResponse)
			if !ok {
				t.Fatalf("expected 200, got %T", resp2)
			}
			if got := updated.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("update: OpenChoreo-Release-Line-Applied = %q, want true", got)
			}
		})
	}
}

// A rule with neither a component nor a line is not scoped by line.
func TestCreateAlertRule_NoComponentLeavesHeaderUnset(t *testing.T) {
	h := newAlertingHandler(&fakeAlertClient{}, &fakeObserver{})
	resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: validAlertBody()})
	created, ok := resp.(gen.CreateAlertRule201JSONResponse)
	if !ok {
		t.Fatalf("expected 201, got %T", resp)
	}
	if got := created.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty", got)
	}
}
