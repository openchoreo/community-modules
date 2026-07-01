// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/finops-opencost/internal/api/gen"
	"github.com/openchoreo/community-modules/finops-opencost/internal/observer"
	"github.com/openchoreo/community-modules/finops-opencost/internal/opencost"
	"github.com/openchoreo/community-modules/finops-opencost/internal/recommend"
)

const componentUID = "14e6fe2a-820a-481e-9a96-018e86a241fa"
const environmentUID = "3d9d7f27-f0ab-4310-ae0b-4980f4ccd302"
const projectUID = "74bd2a7e-4277-4982-9d16-8a41b979a55c"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestHandler(openCostURL, observerURL string) *CostHandler {
	return NewCostHandler(
		opencost.NewClient(openCostURL, discardLogger()),
		observer.NewClient(observerURL),
		recommend.Config{RecommendationCPUPercentile: 95, RecommendationMemoryPercentile: 95, RecommendationCPUHeadroom: 0.2, RecommendationMemoryHeadroom: 0.2},
		"5m",
		discardLogger(),
	)
}

func TestGetComponentCostsAggregatesPods(t *testing.T) {
	// Two pods of the same component should be summed with cost-weighted efficiency.
	body := `{"code":200,"data":[{
	  "pod-a": {"properties":{"labels":{
	      "openchoreo_dev_component_uid":"` + componentUID + `",
	      "openchoreo_dev_component":"checkout",
	      "openchoreo_dev_project_uid":"` + projectUID + `",
	      "openchoreo_dev_project":"gcp",
	      "openchoreo_dev_environment":"development"}},
	    "cpuCost":2.0,"ramCost":1.0,"cpuEfficiency":0.5,"ramEfficiency":0.8},
	  "pod-b": {"properties":{"labels":{
	      "openchoreo_dev_component_uid":"` + componentUID + `",
	      "openchoreo_dev_component":"checkout",
	      "openchoreo_dev_project_uid":"` + projectUID + `",
	      "openchoreo_dev_project":"gcp",
	      "openchoreo_dev_environment":"development"}},
	    "cpuCost":2.0,"ramCost":1.0,"cpuEfficiency":0.5,"ramEfficiency":0.8}
	}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	h := newTestHandler(srv.URL, "http://unused")
	resp, err := h.GetComponentCosts(context.Background(), gen.GetComponentCostsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetComponentCostsParams{
			StartTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("GetComponentCosts error: %v", err)
	}
	ok, is := resp.(gen.GetComponentCosts200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if len(ok.Items) != 1 {
		t.Fatalf("expected 1 component, got %d", len(ok.Items))
	}
	item := ok.Items[0]
	if item.CpuCost != 4.0 || item.MemoryCost != 2.0 {
		t.Errorf("costs not summed: cpu=%v mem=%v", item.CpuCost, item.MemoryCost)
	}
	// weighted efficiency = (4*0.5 + 2*0.8)/6 = 0.6
	if item.Efficiency < 0.599 || item.Efficiency > 0.601 {
		t.Errorf("efficiency = %v, want ~0.6", item.Efficiency)
	}
	if item.Component != "checkout" || item.Namespace != "default" {
		t.Errorf("unexpected metadata: %+v", item)
	}
	if item.EnvironmentUid.String() != environmentUID {
		t.Errorf("environment uid = %v, want %v", item.EnvironmentUid, environmentUID)
	}
}

func TestGetComponentCostsRejectsBadWindow(t *testing.T) {
	h := newTestHandler("http://unused", "http://unused")
	resp, err := h.GetComponentCosts(context.Background(), gen.GetComponentCostsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetComponentCostsParams{
			StartTime: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, is := resp.(gen.GetComponentCosts400JSONResponse); !is {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestGetRecommendationsRequiresToken(t *testing.T) {
	h := newTestHandler("http://unused", "http://unused")
	resp, err := h.GetRecommendations(context.Background(), gen.GetRecommendationsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetRecommendationsParams{
			StartTime:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Environment: "development",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, is := resp.(gen.GetRecommendations401JSONResponse); !is {
		t.Fatalf("expected 401 response, got %T", resp)
	}
}

func TestGetRecommendationsHappyPath(t *testing.T) {
	openCostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":[{
		  "pod-a": {"properties":{"labels":{
		      "openchoreo_dev_component_uid":"`+componentUID+`",
		      "openchoreo_dev_component":"checkout",
		      "openchoreo_dev_project_uid":"`+projectUID+`",
		      "openchoreo_dev_project":"gcp"}},
		    "cpuCost":24.0,"cpuCoreHours":24.0,"ramCost":24.0,"ramByteHours":24.0}
		}]}`)
	}))
	defer openCostSrv.Close()

	observerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
		  "cpuUsage":[{"value":0.1},{"value":0.12},{"value":0.15}],
		  "cpuRequests":[{"value":1.0}],
		  "cpuLimits":[{"value":2.0}],
		  "memoryUsage":[{"value":100000000},{"value":120000000}],
		  "memoryRequests":[{"value":536870912}],
		  "memoryLimits":[{"value":1073741824}]
		}`)
	}))
	defer observerSrv.Close()

	h := newTestHandler(openCostSrv.URL, observerSrv.URL)
	ctx := context.WithValue(context.Background(), bearerTokenKey, "test-token")
	resp, err := h.GetRecommendations(ctx, gen.GetRecommendationsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetRecommendationsParams{
			StartTime:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Environment: "development",
		},
	})
	if err != nil {
		t.Fatalf("GetRecommendations error: %v", err)
	}
	ok, is := resp.(gen.GetRecommendations200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if len(ok.Items) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(ok.Items))
	}
	rec := ok.Items[0]
	if rec.Current.CpuRequest != "1000m" {
		t.Errorf("current cpu request = %q, want 1000m", rec.Current.CpuRequest)
	}
	// recommended request should be right-sized well below current 1000m.
	if rec.Recommendation.CpuRequest == rec.Current.CpuRequest {
		t.Errorf("recommendation not right-sized: %q", rec.Recommendation.CpuRequest)
	}
	// unit price $1/core-hour over 24h => current cpu cost = 1 core * 24h * 1 = 24.
	if rec.Current.CpuCost < 23.9 || rec.Current.CpuCost > 24.1 {
		t.Errorf("current cpu cost = %v, want ~24", rec.Current.CpuCost)
	}
	if rec.Recommendation.CpuCost >= rec.Current.CpuCost {
		t.Errorf("recommended cost %v should be below current %v", rec.Recommendation.CpuCost, rec.Current.CpuCost)
	}
}

func TestGetComponentCostsRejectsComponentWithoutProject(t *testing.T) {
	h := newTestHandler("http://unused", "http://unused")
	cuid := openapi_types.UUID(mustUUID(componentUID))
	resp, err := h.GetComponentCosts(context.Background(), gen.GetComponentCostsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetComponentCostsParams{
			StartTime:    time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:      time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			ComponentUid: &cuid,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, is := resp.(gen.GetComponentCosts400JSONResponse); !is {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestGetComponentCostsRejectsBadGranularity(t *testing.T) {
	h := newTestHandler("http://unused", "http://unused")
	bad := "5x"
	resp, err := h.GetComponentCosts(context.Background(), gen.GetComponentCostsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetComponentCostsParams{
			StartTime:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Granularity: &bad,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, is := resp.(gen.GetComponentCosts400JSONResponse); !is {
		t.Fatalf("expected 400 response, got %T", resp)
	}
}

func TestGetComponentCostsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := newTestHandler(srv.URL, "http://unused")
	resp, err := h.GetComponentCosts(context.Background(), gen.GetComponentCostsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetComponentCostsParams{
			StartTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, is := resp.(gen.GetComponentCosts500JSONResponse); !is {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestGetComponentCostsWithGranularityAndSkipsUnlabeled(t *testing.T) {
	// One pod has no component UID label and must be skipped; the other is kept.
	// pod-a has zero cost, so efficiency falls back to the totalEfficiency average.
	body := `{"code":200,"data":[{
	  "unlabeled": {"properties":{"labels":{}},"cpuCost":1.0,"ramCost":1.0},
	  "pod-a": {"properties":{"labels":{
	      "openchoreo_dev_component_uid":"` + componentUID + `",
	      "openchoreo_dev_component":"checkout"}},
	    "cpuCost":0.0,"ramCost":0.0,"totalEfficiency":0.7}
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	h := newTestHandler(srv.URL, "http://unused")
	gran := "1d"
	resp, err := h.GetComponentCosts(context.Background(), gen.GetComponentCostsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetComponentCostsParams{
			StartTime:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Granularity: &gran,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.GetComponentCosts200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if len(ok.Items) != 1 {
		t.Fatalf("expected 1 component (unlabeled skipped), got %d", len(ok.Items))
	}
	// No cost-weighted efficiency present, so it falls back to totalEfficiency.
	if ok.Items[0].Efficiency < 0.699 || ok.Items[0].Efficiency > 0.701 {
		t.Errorf("efficiency = %v, want ~0.7", ok.Items[0].Efficiency)
	}
}

func TestGetRecommendationsValidation(t *testing.T) {
	h := newTestHandler("http://unused", "http://unused")
	ctx := context.WithValue(context.Background(), bearerTokenKey, "tok")
	cuid := openapi_types.UUID(mustUUID(componentUID))

	base := func() gen.GetRecommendationsParams {
		return gen.GetRecommendationsParams{
			StartTime:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Environment: "development",
		}
	}

	cases := []struct {
		name   string
		params gen.GetRecommendationsParams
	}{
		{"bad window", func() gen.GetRecommendationsParams {
			p := base()
			p.StartTime, p.EndTime = p.EndTime, p.StartTime
			return p
		}()},
		{"missing environment", func() gen.GetRecommendationsParams {
			p := base()
			p.Environment = ""
			return p
		}()},
		{"component without project", func() gen.GetRecommendationsParams {
			p := base()
			p.ComponentUid = &cuid
			return p
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := h.GetRecommendations(ctx, gen.GetRecommendationsRequestObject{
				Namespace:      "default",
				EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
				Params:         tc.params,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, is := resp.(gen.GetRecommendations400JSONResponse); !is {
				t.Fatalf("expected 400 response, got %T", resp)
			}
		})
	}
}

func TestGetRecommendationsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := newTestHandler(srv.URL, "http://unused")
	ctx := context.WithValue(context.Background(), bearerTokenKey, "tok")
	resp, err := h.GetRecommendations(ctx, gen.GetRecommendationsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetRecommendationsParams{
			StartTime:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Environment: "development",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, is := resp.(gen.GetRecommendations500JSONResponse); !is {
		t.Fatalf("expected 500 response, got %T", resp)
	}
}

func TestGetRecommendationsSkipsComponentOnMetricsError(t *testing.T) {
	openCostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"data":[{
		  "pod-a": {"properties":{"labels":{
		      "openchoreo_dev_component_uid":"`+componentUID+`",
		      "openchoreo_dev_component":"checkout"}},
		    "cpuCost":1.0,"cpuCoreHours":1.0}
		}]}`)
	}))
	defer openCostSrv.Close()

	// Observer returns 500, so the single component is dropped and Items is empty.
	observerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer observerSrv.Close()

	h := newTestHandler(openCostSrv.URL, observerSrv.URL)
	ctx := context.WithValue(context.Background(), bearerTokenKey, "tok")
	resp, err := h.GetRecommendations(ctx, gen.GetRecommendationsRequestObject{
		Namespace:      "default",
		EnvironmentUid: openapi_types.UUID(mustUUID(environmentUID)),
		Params: gen.GetRecommendationsParams{
			StartTime:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:     time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			Environment: "development",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, is := resp.(gen.GetRecommendations200JSONResponse)
	if !is {
		t.Fatalf("expected 200 response, got %T", resp)
	}
	if len(ok.Items) != 0 {
		t.Fatalf("expected 0 recommendations (component skipped), got %d", len(ok.Items))
	}
}

func TestConversionHelpers(t *testing.T) {
	if got := coresToQuantity(0.25); got != "250m" {
		t.Errorf("coresToQuantity = %q, want 250m", got)
	}
	if got := bytesToQuantity(2 * 1024 * 1024); got != "2Mi" {
		t.Errorf("bytesToQuantity = %q, want 2Mi", got)
	}
	if got := parseUUID("not-a-uuid"); got != (openapi_types.UUID{}) {
		t.Errorf("parseUUID of invalid string should be zero value, got %v", got)
	}
	if got := derefString(nil); got != "" {
		t.Errorf("derefString(nil) = %q, want empty", got)
	}
	s := "x"
	if got := derefString(&s); got != "x" {
		t.Errorf("derefString(&x) = %q, want x", got)
	}
	if got := uuidPtrString(nil); got != "" {
		t.Errorf("uuidPtrString(nil) = %q, want empty", got)
	}
	if got := clamp01(-1); got != 0 {
		t.Errorf("clamp01(-1) = %v, want 0", got)
	}
	if got := clamp01(2); got != 1 {
		t.Errorf("clamp01(2) = %v, want 1", got)
	}
	if got := maxValue(nil); got != 0 {
		t.Errorf("maxValue(nil) = %v, want 0", got)
	}
}

func mustUUID(s string) openapi_types.UUID {
	return parseUUID(s)
}
