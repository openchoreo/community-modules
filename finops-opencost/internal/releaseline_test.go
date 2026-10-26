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
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/finops-opencost/internal/api/gen"
	"github.com/openchoreo/community-modules/finops-opencost/internal/observer"
	"github.com/openchoreo/community-modules/finops-opencost/internal/opencost"
)

const releaseLineUID = "9f8e7d6c-5b4a-4321-8fed-cba987654321"

const lineAllocations = `{"code":200,"data":[{
  "pod-a": {"properties":{"labels":{
      "openchoreo_dev_component_uid":"` + componentUID + `",
      "openchoreo_dev_component":"checkout",
      "openchoreo_dev_project_uid":"` + projectUID + `",
      "openchoreo_dev_project":"gcp"}},
    "cpuCost":24.0,"cpuCoreHours":24.0,"ramCost":24.0,"ramByteHours":24.0}
}]}`

func uuidPtr(s string) *openapi_types.UUID {
	u := openapi_types.UUID(mustUUID(s))
	return &u
}

func TestGetComponentCostsFiltersByReleaseLine(t *testing.T) {
	var filter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter = r.URL.Query().Get("filter")
		_, _ = io.WriteString(w, lineAllocations)
	}))
	defer srv.Close()

	h := newTestHandler(srv.URL, "http://unused")
	resp, err := h.GetComponentCosts(context.Background(), gen.GetComponentCostsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetComponentCostsParams{
			ProjectUid:     uuidPtr(projectUID),
			ComponentUid:   uuidPtr(componentUID),
			ReleaseLineUid: uuidPtr(releaseLineUID),
			StartTime:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:        time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("GetComponentCosts error: %v", err)
	}
	ok, is := resp.(gen.GetComponentCosts200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if ok.Headers.OpenChoreoReleaseLineApplied != "true" {
		t.Errorf("release line applied header = %q, want true", ok.Headers.OpenChoreoReleaseLineApplied)
	}
	if !strings.Contains(filter, `label[openchoreo_dev_release_line_uid]:"`+releaseLineUID+`"`) {
		t.Errorf("OpenCost filter %q does not select the release line", filter)
	}
}

func TestGetComponentCostsRejectsReleaseLineWithoutComponent(t *testing.T) {
	h := newTestHandler("http://unused", "http://unused")
	resp, err := h.GetComponentCosts(context.Background(), gen.GetComponentCostsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetComponentCostsParams{
			ReleaseLineUid: uuidPtr(releaseLineUID),
			StartTime:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:        time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("GetComponentCosts error: %v", err)
	}
	if _, is := resp.(gen.GetComponentCosts400JSONResponse); !is {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestGetRecommendationsForwardsReleaseLine(t *testing.T) {
	openCostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, lineAllocations)
	}))
	defer openCostSrv.Close()

	var metricsReq observer.MetricsQueryRequest
	observerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&metricsReq)
		_, _ = io.WriteString(w, `{"cpuUsage":[{"value":0.1}],"cpuRequests":[{"value":1.0}],"cpuLimits":[{"value":2.0}],
		  "memoryUsage":[{"value":100000000}],"memoryRequests":[{"value":536870912}],"memoryLimits":[{"value":1073741824}]}`)
	}))
	defer observerSrv.Close()

	line := "next"
	h := newTestHandler(openCostSrv.URL, observerSrv.URL)
	ctx := context.WithValue(context.Background(), bearerTokenKey, "test-token")
	resp, err := h.GetRecommendations(ctx, gen.GetRecommendationsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetRecommendationsParams{
			StartTime:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:        time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Environment:    "development",
			ProjectUid:     uuidPtr(projectUID),
			ComponentUid:   uuidPtr(componentUID),
			ReleaseLineUid: uuidPtr(releaseLineUID),
			ReleaseLine:    &line,
		},
	})
	if err != nil {
		t.Fatalf("GetRecommendations error: %v", err)
	}
	ok, is := resp.(gen.GetRecommendations200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if ok.Headers.OpenChoreoReleaseLineApplied != "true" {
		t.Errorf("release line applied header = %q, want true", ok.Headers.OpenChoreoReleaseLineApplied)
	}
	if metricsReq.SearchScope.ReleaseLine != "next" {
		t.Errorf("metrics query release line = %q, want next", metricsReq.SearchScope.ReleaseLine)
	}
}

// A recommendation's usage metrics cover the same lines as its costs.
func TestGetRecommendationsMetricsCoverTheCostsLines(t *testing.T) {
	openCostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, lineAllocations)
	}))
	defer openCostSrv.Close()

	all := true
	line := "next"
	tests := []struct {
		name     string
		params   gen.GetRecommendationsParams
		wantAll  bool
		wantLine string
	}{
		{name: "project", params: gen.GetRecommendationsParams{ProjectUid: uuidPtr(projectUID)}, wantAll: true},
		{name: "default line", params: gen.GetRecommendationsParams{ProjectUid: uuidPtr(projectUID), ComponentUid: uuidPtr(componentUID)}},
		{name: "every line", params: gen.GetRecommendationsParams{
			ProjectUid: uuidPtr(projectUID), ComponentUid: uuidPtr(componentUID), AllReleaseLines: &all,
		}, wantAll: true},
		{name: "one line", params: gen.GetRecommendationsParams{
			ProjectUid: uuidPtr(projectUID), ComponentUid: uuidPtr(componentUID), ReleaseLineUid: uuidPtr(releaseLineUID), ReleaseLine: &line,
		}, wantLine: line},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var metricsReq observer.MetricsQueryRequest
			observerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&metricsReq)
				_, _ = io.WriteString(w, `{"cpuUsage":[{"value":0.1}],"cpuRequests":[{"value":1.0}],"cpuLimits":[{"value":2.0}],
				  "memoryUsage":[{"value":100000000}],"memoryRequests":[{"value":536870912}],"memoryLimits":[{"value":1073741824}]}`)
			}))
			defer observerSrv.Close()

			params := tt.params
			params.StartTime = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
			params.EndTime = time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
			params.Environment = "development"
			h := newTestHandler(openCostSrv.URL, observerSrv.URL)
			ctx := context.WithValue(context.Background(), bearerTokenKey, "test-token")
			resp, err := h.GetRecommendations(ctx, gen.GetRecommendationsRequestObject{
				Namespace:      "default",
				EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
				Params:         params,
			})
			if err != nil {
				t.Fatalf("GetRecommendations error: %v", err)
			}
			if _, is := resp.(gen.GetRecommendations200JSONResponse); !is {
				t.Fatalf("expected 200 response, got %T", resp)
			}
			if got := metricsReq.SearchScope.AllReleaseLines; got != tt.wantAll {
				t.Errorf("metrics query allReleaseLines = %v, want %v", got, tt.wantAll)
			}
			if got := metricsReq.SearchScope.ReleaseLine; got != tt.wantLine {
				t.Errorf("metrics query release line = %q, want %q", got, tt.wantLine)
			}
		})
	}
}

func TestAllocationComponentUID(t *testing.T) {
	alloc := func(labels map[string]string) opencost.Allocation {
		return opencost.Allocation{Properties: opencost.AllocationProperties{Labels: labels}}
	}
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"default line pod", map[string]string{opencost.LabelComponentUID: "comp-1"}, "comp-1"},
		{"release line pod", map[string]string{opencost.LabelReleaseLineUID: "line-1", opencost.LabelReleaseLineComponentUID: "comp-1"}, "comp-1"},
		{"pod of no component", map[string]string{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allocationComponentUID(alloc(tt.labels)); got != tt.want {
				t.Errorf("allocationComponentUID() = %q, want %q", got, tt.want)
			}
		})
	}
}
