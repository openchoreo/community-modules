// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/cloudmonitoring"
)

const (
	testComponentUID   = "f3b8e2a4-6c1d-4e9f-9a2b-3d5c7e8f0a1c"
	testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
)

func TestQueryMetrics_ReleaseLineHeader(t *testing.T) {
	allLines := true
	tests := map[string]struct {
		line       *string
		allLines   *bool
		wantHeader string
	}{
		"one line":   {line: strPtr(testReleaseLineUID), wantHeader: "true"},
		"every line": {allLines: &allLines, wantHeader: "true"},
		// The filter cannot exclude release line series, so the main line is not confirmed.
		"main line": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := &fakeMetricsClient{result: &cloudmonitoring.ResourceMetricsResult{}}
			h := newTestHandler(client)
			req := validQueryRequest()
			req.Body.SearchScope.ComponentUid = strPtr(testComponentUID)
			req.Body.SearchScope.ReleaseLineUid = tt.line
			req.Body.SearchScope.AllReleaseLines = tt.allLines

			resp, err := h.QueryMetrics(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client.params.ReleaseLineUID != derefString(tt.line) || client.params.AllReleaseLines != (tt.allLines != nil) {
				t.Errorf("line scope not forwarded: %+v", client.params)
			}
			rec := httptest.NewRecorder()
			if err := resp.VisitQueryMetricsResponse(rec); err != nil {
				t.Fatalf("visit: %v", err)
			}
			if got := rec.Header().Get("OpenChoreo-Release-Line-Applied"); got != tt.wantHeader {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, tt.wantHeader)
			}
		})
	}
}

// HTTP metrics are not served, so no scope is applied to confirm.
func TestQueryMetrics_HTTPNeverSetsReleaseLineHeader(t *testing.T) {
	h := newTestHandler(&fakeMetricsClient{})
	req := validQueryRequest()
	req.Body.Metric = gen.MetricsQueryRequestMetricHttp
	req.Body.SearchScope.ComponentUid = strPtr(testComponentUID)
	req.Body.SearchScope.ReleaseLineUid = strPtr(testReleaseLineUID)

	resp, err := h.QueryMetrics(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitQueryMetricsResponse(rec); err != nil {
		t.Fatalf("visit: %v", err)
	}
	if got := rec.Header().Get("OpenChoreo-Release-Line-Applied"); got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty", got)
	}
}

func TestAlertRule_ReleaseLineHeader(t *testing.T) {
	lineUID := uuid.MustParse(testReleaseLineUID)
	for name, line := range map[string]*uuid.UUID{"release line": &lineUID, "main line": nil} {
		t.Run(name, func(t *testing.T) {
			wantHeader := ""
			if line != nil {
				wantHeader = "true"
			}
			ac := &fakeAlertClient{backendID: "projects/p/alertPolicies/1"}
			h := newAlertingHandler(ac, &fakeObserver{})
			body := validAlertRequestBody()
			body.Metadata.ReleaseLineUid = line

			resp, _ := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: body})
			created, ok := resp.(gen.CreateAlertRule201JSONResponse)
			if !ok {
				t.Fatalf("create resp = %T, want 201", resp)
			}
			if got := created.Headers.OpenChoreoReleaseLineApplied; got != wantHeader {
				t.Errorf("create OpenChoreo-Release-Line-Applied = %q, want %q", got, wantHeader)
			}
			if line != nil && ac.lastInput.ReleaseLineUID != testReleaseLineUID {
				t.Errorf("release line not forwarded: %+v", ac.lastInput)
			}

			update := gen.UpdateAlertRuleJSONRequestBody(*body)
			resp2, _ := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{RuleName: body.Metadata.Name, Body: &update})
			updated, ok := resp2.(gen.UpdateAlertRule200JSONResponse)
			if !ok {
				t.Fatalf("update resp = %T, want 200", resp2)
			}
			if got := updated.Headers.OpenChoreoReleaseLineApplied; got != wantHeader {
				t.Errorf("update OpenChoreo-Release-Line-Applied = %q, want %q", got, wantHeader)
			}
		})
	}
}
