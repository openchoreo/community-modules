// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-metrics-aws-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-aws-cloudwatch/internal/cloudwatchmetrics"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

func TestQueryMetrics_ReleaseLineScope(t *testing.T) {
	lineUID, componentUID, all := testReleaseLineUID, testComponentUID, true
	tests := map[string]struct {
		scope      gen.ComponentSearchScope
		wantLine   string
		wantAll    bool
		wantHeader string
	}{
		"one line":   {scope: gen.ComponentSearchScope{ComponentUid: &componentUID, ReleaseLineUid: &lineUID}, wantLine: lineUID, wantHeader: "true"},
		"every line": {scope: gen.ComponentSearchScope{ComponentUid: &componentUID, AllReleaseLines: &all}, wantAll: true, wantHeader: "true"},
		"main line":  {scope: gen.ComponentSearchScope{ComponentUid: &componentUID}, wantHeader: "true"},
		// allReleaseLines has no effect without a component.
		"namespace": {scope: gen.ComponentSearchScope{AllReleaseLines: &all}},
	}
	for name, tt := range tests {
		for _, metric := range []gen.MetricsQueryRequestMetric{gen.MetricsQueryRequestMetricResource, gen.MetricsQueryRequestMetricHttp} {
			t.Run(name+"/"+string(metric), func(t *testing.T) {
				var gotLine string
				var gotAll bool
				client := &stubMetricsClient{
					getResourceFn: func(_ context.Context, p cloudwatchmetrics.MetricsQueryParams) (*cloudwatchmetrics.ResourceMetricsResult, error) {
						gotLine, gotAll = p.ReleaseLineUID, p.AllReleaseLines
						return &cloudwatchmetrics.ResourceMetricsResult{}, nil
					},
					getHTTPMetricsFn: func(_ context.Context, p cloudwatchmetrics.HTTPMetricsQueryParams) (*cloudwatchmetrics.HTTPMetricsResult, error) {
						gotLine, gotAll = p.ReleaseLineUID, p.AllReleaseLines
						return &cloudwatchmetrics.HTTPMetricsResult{}, nil
					},
				}
				scope := tt.scope
				scope.Namespace = "payments"
				resp, err := newTestHandler(client, nil).QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: &gen.MetricsQueryRequest{
					Metric: metric, StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), SearchScope: scope,
				}})
				if err != nil {
					t.Fatalf("QueryMetrics() error = %v", err)
				}
				if gotLine != tt.wantLine || gotAll != tt.wantAll {
					t.Errorf("ReleaseLineUID, AllReleaseLines = %q, %v, want %q, %v", gotLine, gotAll, tt.wantLine, tt.wantAll)
				}
				rec := httptest.NewRecorder()
				if err := resp.VisitQueryMetricsResponse(rec); err != nil {
					t.Fatalf("VisitQueryMetricsResponse: %v", err)
				}
				if rec.Code != 200 {
					t.Fatalf("status = %d, want 200", rec.Code)
				}
				if got := rec.Header().Get("OpenChoreo-Release-Line-Applied"); got != tt.wantHeader {
					t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, tt.wantHeader)
				}
			})
		}
	}
}

func TestAlertRule_ReleaseLineScope(t *testing.T) {
	for name, line := range map[string]string{"release line": testReleaseLineUID, "main line": ""} {
		t.Run(name, func(t *testing.T) {
			var got cloudwatchmetrics.MetricAlertParams
			client := &stubMetricsClient{
				getAlertFn: func(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error) {
					return nil, cloudwatchmetrics.ErrAlertNotFound
				},
				createAlertFn: func(_ context.Context, p cloudwatchmetrics.MetricAlertParams) (string, error) {
					got = p
					return "arn", nil
				},
				updateAlertFn: func(_ context.Context, _, _ string, p cloudwatchmetrics.MetricAlertParams) (string, error) {
					got = p
					return "arn", nil
				},
			}
			h := newTestHandler(client, nil)
			req := alertRuleRequest()
			if line != "" {
				uid := openapi_types.UUID(uuid.MustParse(line))
				req.Metadata.ReleaseLineUid = &uid
			}

			createResp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: req})
			if err != nil {
				t.Fatalf("CreateAlertRule() error = %v", err)
			}
			created, ok := createResp.(gen.CreateAlertRule201JSONResponse)
			if !ok {
				t.Fatalf("expected 201, got %T", createResp)
			}
			if v := created.Headers.OpenChoreoReleaseLineApplied; v != "true" {
				t.Errorf("create: OpenChoreo-Release-Line-Applied = %q, want true", v)
			}
			if got.ReleaseLineUID != line || got.ComponentUID != req.Metadata.ComponentUid.String() {
				t.Errorf("create: ReleaseLineUID, ComponentUID = %q, %q, want %q, %q",
					got.ReleaseLineUID, got.ComponentUID, line, req.Metadata.ComponentUid.String())
			}

			updateResp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{RuleName: req.Metadata.Name, Body: req})
			if err != nil {
				t.Fatalf("UpdateAlertRule() error = %v", err)
			}
			updated, ok := updateResp.(gen.UpdateAlertRule200JSONResponse)
			if !ok {
				t.Fatalf("expected 200, got %T", updateResp)
			}
			if v := updated.Headers.OpenChoreoReleaseLineApplied; v != "true" {
				t.Errorf("update: OpenChoreo-Release-Line-Applied = %q, want true", v)
			}
			if got.ReleaseLineUID != line {
				t.Errorf("update: ReleaseLineUID = %q, want %q", got.ReleaseLineUID, line)
			}
		})
	}
}

type stubScopeResolver struct {
	got ScopeResolutionParams
}

func (s *stubScopeResolver) Resolve(_ context.Context, p ScopeResolutionParams) (ScopeResolutionResult, bool, error) {
	s.got = p
	return ScopeResolutionResult{}, false, nil
}

func TestAlertRule_ReleaseLineResolvesDimensionNamespaceThroughLine(t *testing.T) {
	var gotLine string
	var got cloudwatchmetrics.MetricAlertParams
	resolver := &stubScopeResolver{}
	client := &stubMetricsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error) {
			return nil, cloudwatchmetrics.ErrAlertNotFound
		},
		createAlertFn: func(_ context.Context, p cloudwatchmetrics.MetricAlertParams) (string, error) {
			got = p
			return "arn", nil
		},
		resolveNamespaceDimensionFn: func(_ context.Context, _, _, releaseLineUID string) (string, error) {
			gotLine = releaseLineUID
			return "line-namespace", nil
		},
	}
	h := NewMetricsHandler(client, HandlerOptions{ScopeResolver: resolver}, discardLogger())
	req := alertRuleRequest()
	uid := openapi_types.UUID(uuid.MustParse(testReleaseLineUID))
	req.Metadata.ReleaseLineUid = &uid

	if _, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: req}); err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if resolver.got.ReleaseLineUID != testReleaseLineUID {
		t.Errorf("scope resolver ReleaseLineUID = %q, want %q", resolver.got.ReleaseLineUID, testReleaseLineUID)
	}
	if gotLine != testReleaseLineUID {
		t.Errorf("ResolveNamespaceDimension releaseLineUID = %q, want %q", gotLine, testReleaseLineUID)
	}
	if got.DimensionNamespace != "line-namespace" {
		t.Errorf("DimensionNamespace = %q, want %q", got.DimensionNamespace, "line-namespace")
	}
}
