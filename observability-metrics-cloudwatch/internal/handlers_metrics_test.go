// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/cloudwatchmetrics"
)

type stubMetricsClient struct {
	pingErr            error
	getResourceFn      func(context.Context, cloudwatchmetrics.MetricsQueryParams) (*cloudwatchmetrics.ResourceMetricsResult, error)
	createAlertFn      func(context.Context, cloudwatchmetrics.MetricAlertParams) (string, error)
	getAlertFn         func(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error)
	updateAlertFn      func(context.Context, string, string, cloudwatchmetrics.MetricAlertParams) (string, error)
	deleteAlertFn      func(context.Context, string, string) (string, error)
	getAlarmTagsByName func(context.Context, string) (map[string]string, error)
}

func (s *stubMetricsClient) Ping(context.Context) error { return s.pingErr }

func (s *stubMetricsClient) GetResourceMetrics(ctx context.Context, p cloudwatchmetrics.MetricsQueryParams) (*cloudwatchmetrics.ResourceMetricsResult, error) {
	if s.getResourceFn == nil {
		return nil, errors.New("unexpected GetResourceMetrics call")
	}
	return s.getResourceFn(ctx, p)
}

func (s *stubMetricsClient) CreateAlert(ctx context.Context, p cloudwatchmetrics.MetricAlertParams) (string, error) {
	if s.createAlertFn == nil {
		return "", errors.New("unexpected CreateAlert call")
	}
	return s.createAlertFn(ctx, p)
}

func (s *stubMetricsClient) UpdateAlert(ctx context.Context, ns, name string, p cloudwatchmetrics.MetricAlertParams) (string, error) {
	if s.updateAlertFn == nil {
		return "", errors.New("unexpected UpdateAlert call")
	}
	return s.updateAlertFn(ctx, ns, name, p)
}

func (s *stubMetricsClient) DeleteAlert(ctx context.Context, ns, name string) (string, error) {
	if s.deleteAlertFn == nil {
		return "", errors.New("unexpected DeleteAlert call")
	}
	return s.deleteAlertFn(ctx, ns, name)
}

func (s *stubMetricsClient) GetAlert(ctx context.Context, ns, name string) (*cloudwatchmetrics.AlertDetail, error) {
	if s.getAlertFn == nil {
		return nil, errors.New("unexpected GetAlert call")
	}
	return s.getAlertFn(ctx, ns, name)
}

func (s *stubMetricsClient) GetAlarmTagsByName(ctx context.Context, name string) (map[string]string, error) {
	if s.getAlarmTagsByName == nil {
		return nil, errors.New("unexpected GetAlarmTagsByName call")
	}
	return s.getAlarmTagsByName(ctx, name)
}

func newTestHandler(client metricsClient, observer observerForwarder) *MetricsHandler {
	return NewMetricsHandler(client, HandlerOptions{ObserverClient: observer}, discardLogger())
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func alertRuleRequest() *gen.AlertRuleRequest {
	project := openapi_types.UUID(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	environment := openapi_types.UUID(uuid.MustParse("22222222-2222-2222-2222-222222222222"))
	component := openapi_types.UUID(uuid.MustParse("33333333-3333-3333-3333-333333333333"))

	req := &gen.AlertRuleRequest{}
	req.Metadata.Name = "high-cpu"
	req.Metadata.Namespace = "payments"
	req.Metadata.ProjectUid = project
	req.Metadata.EnvironmentUid = environment
	req.Metadata.ComponentUid = component
	req.Source.Metric = gen.AlertRuleRequestSourceMetricCpuUsage
	req.Condition.Enabled = true
	req.Condition.Window = "5m"
	req.Condition.Interval = "1m"
	req.Condition.Operator = gen.AlertRuleRequestConditionOperatorGt
	req.Condition.Threshold = 0.5
	return req
}

func TestHealthReturnsHealthy(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	resp, err := h.Health(context.Background(), gen.HealthRequestObject{})
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	body, ok := resp.(gen.Health200JSONResponse)
	if !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if body.Status == nil || *body.Status != "healthy" {
		t.Fatalf("unexpected status: %#v", body.Status)
	}
}

func TestQueryMetricsRejectsNilBody(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	resp, err := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryMetricsRejectsEmptyNamespace(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	body := &gen.MetricsQueryRequest{
		Metric:      gen.Resource,
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: gen.ComponentSearchScope{Namespace: ""},
	}
	resp, err := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryMetricsRejectsInvalidStep(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	step := "garbage"
	body := &gen.MetricsQueryRequest{
		Metric:      gen.Resource,
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: gen.ComponentSearchScope{Namespace: "default"},
		Step:        &step,
	}
	resp, err := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryMetricsRejectsUnknownMetricType(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	body := &gen.MetricsQueryRequest{
		Metric:      gen.MetricsQueryRequestMetric("unknown"),
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: gen.ComponentSearchScope{Namespace: "default"},
	}
	resp, err := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	if _, ok := resp.(gen.QueryMetrics400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryMetricsResourceReturnsSeries(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	client := &stubMetricsClient{
		getResourceFn: func(_ context.Context, p cloudwatchmetrics.MetricsQueryParams) (*cloudwatchmetrics.ResourceMetricsResult, error) {
			if p.Namespace != "payments" || p.ComponentUID != "comp-1" {
				t.Fatalf("unexpected scope: %#v", p)
			}
			if p.StepSeconds != 60 {
				t.Fatalf("expected 60s step, got %d", p.StepSeconds)
			}
			return &cloudwatchmetrics.ResourceMetricsResult{
				CPUUsage: []cloudwatchmetrics.TimeValuePoint{{Timestamp: now, Value: 0.42}},
			}, nil
		},
	}
	h := newTestHandler(client, nil)
	componentUID := "comp-1"
	step := "1m"
	body := &gen.MetricsQueryRequest{
		Metric:    gen.Resource,
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
		SearchScope: gen.ComponentSearchScope{
			Namespace:    "payments",
			ComponentUid: &componentUID,
		},
		Step: &step,
	}
	resp, err := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	ok, isOK := resp.(metricsQueryOKResponse)
	if !isOK {
		t.Fatalf("expected resource OK response, got %T", resp)
	}
	rec := httptest.NewRecorder()
	if err := ok.VisitQueryMetricsResponse(rec); err != nil {
		t.Fatalf("VisitQueryMetricsResponse: %v", err)
	}
	if rec.Header().Get("X-OpenChoreo-Adapter-Notice") != "" {
		t.Fatalf("did not expect adapter notice header on resource response")
	}
	if !strings.Contains(rec.Body.String(), "cpuUsage") {
		t.Fatalf("expected cpuUsage in body, got %s", rec.Body.String())
	}
}

func TestQueryMetricsResourcePropagatesClientError(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		getResourceFn: func(context.Context, cloudwatchmetrics.MetricsQueryParams) (*cloudwatchmetrics.ResourceMetricsResult, error) {
			return nil, errors.New("aws boom")
		},
	}, nil)
	body := &gen.MetricsQueryRequest{
		Metric:      gen.Resource,
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: gen.ComponentSearchScope{Namespace: "default"},
	}
	resp, err := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	if _, ok := resp.(gen.QueryMetrics500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestQueryMetricsHTTPReturnsEmptyArraysAndNoticeHeader(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	body := &gen.MetricsQueryRequest{
		Metric:      gen.Http,
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: gen.ComponentSearchScope{Namespace: "default"},
	}
	resp, err := h.QueryMetrics(context.Background(), gen.QueryMetricsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	ok, isOK := resp.(httpMetricsQueryOKResponse)
	if !isOK {
		t.Fatalf("expected http OK response, got %T", resp)
	}
	rec := httptest.NewRecorder()
	if err := ok.VisitQueryMetricsResponse(rec); err != nil {
		t.Fatalf("VisitQueryMetricsResponse: %v", err)
	}
	if got := rec.Header().Get("X-OpenChoreo-Adapter-Notice"); got != "http-metrics-not-implemented" {
		t.Fatalf("expected notice header, got %q", got)
	}
	var asMap map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &asMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"requestCount", "successfulRequestCount", "unsuccessfulRequestCount", "meanLatency", "latencyP50", "latencyP90", "latencyP99"} {
		arr, ok := asMap[key].([]any)
		if !ok {
			t.Fatalf("expected %s to be an array, got %T", key, asMap[key])
		}
		if len(arr) != 0 {
			t.Fatalf("expected %s to be empty, got %d items", key, len(arr))
		}
	}
}

func TestCreateAlertRuleReturnsConflictWhenRuleExists(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error) {
			return &cloudwatchmetrics.AlertDetail{Name: "high-cpu"}, nil
		},
	}, nil)
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: alertRuleRequest()})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule409JSONResponse); !ok {
		t.Fatalf("expected 409, got %T", resp)
	}
}

func TestCreateAlertRuleSucceedsWhenNoConflict(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error) {
			return nil, cloudwatchmetrics.ErrAlertNotFound
		},
		createAlertFn: func(context.Context, cloudwatchmetrics.MetricAlertParams) (string, error) {
			return "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test", nil
		},
	}, nil)
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: alertRuleRequest()})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule201JSONResponse); !ok {
		t.Fatalf("expected 201, got %T", resp)
	}
}

func TestCreateAlertRuleReturns400OnEqOperator(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error) {
			return nil, cloudwatchmetrics.ErrAlertNotFound
		},
		createAlertFn: func(context.Context, cloudwatchmetrics.MetricAlertParams) (string, error) {
			return "", errors.New("invalid: operator 'eq' not supported by CloudWatch metric alarms")
		},
	}, nil)
	req := alertRuleRequest()
	req.Condition.Operator = gen.AlertRuleRequestConditionOperatorEq

	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: req})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestCreateAlertRuleReturns500OnUnexpectedError(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error) {
			return nil, cloudwatchmetrics.ErrAlertNotFound
		},
		createAlertFn: func(context.Context, cloudwatchmetrics.MetricAlertParams) (string, error) {
			return "", errors.New("aws boom")
		},
	}, nil)
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: alertRuleRequest()})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestCreateAlertRuleRejectsNilBody(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	resp, err := h.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{})
	if err != nil {
		t.Fatalf("CreateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.CreateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestGetUpdateDeleteAlertRuleResponses(t *testing.T) {
	client := &stubMetricsClient{
		getAlertFn: func(_ context.Context, _, name string) (*cloudwatchmetrics.AlertDetail, error) {
			if name == "missing" {
				return nil, cloudwatchmetrics.ErrAlertNotFound
			}
			return &cloudwatchmetrics.AlertDetail{
				Name:           "high-cpu",
				Namespace:      "payments",
				ProjectUID:     "11111111-1111-1111-1111-111111111111",
				EnvironmentUID: "22222222-2222-2222-2222-222222222222",
				ComponentUID:   "33333333-3333-3333-3333-333333333333",
				Metric:         "cpu_usage",
				Operator:       "gt",
				Threshold:      0.5,
				Window:         5 * time.Minute,
				Interval:       time.Minute,
				Enabled:        true,
				AlarmARN:       "arn:test",
			}, nil
		},
		updateAlertFn: func(_ context.Context, _, name string, _ cloudwatchmetrics.MetricAlertParams) (string, error) {
			if name == "missing" {
				return "", cloudwatchmetrics.ErrAlertNotFound
			}
			return "arn:test", nil
		},
		deleteAlertFn: func(_ context.Context, _, name string) (string, error) {
			if name == "missing" {
				return "", cloudwatchmetrics.ErrAlertNotFound
			}
			return "arn:test", nil
		},
	}
	h := newTestHandler(client, nil)

	getResp, err := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "high-cpu"})
	if err != nil {
		t.Fatalf("GetAlertRule() error = %v", err)
	}
	if _, ok := getResp.(gen.GetAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 get response, got %T", getResp)
	}

	getMissing, err := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "missing"})
	if err != nil {
		t.Fatalf("GetAlertRule(missing) error = %v", err)
	}
	if _, ok := getMissing.(gen.GetAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 get response, got %T", getMissing)
	}

	updateResp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "high-cpu", Body: alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if _, ok := updateResp.(gen.UpdateAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 update response, got %T", updateResp)
	}

	updateMissing, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "missing", Body: alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule(missing) error = %v", err)
	}
	if _, ok := updateMissing.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400 update response, got %T", updateMissing)
	}

	deleteResp, err := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "high-cpu"})
	if err != nil {
		t.Fatalf("DeleteAlertRule() error = %v", err)
	}
	if _, ok := deleteResp.(gen.DeleteAlertRule200JSONResponse); !ok {
		t.Fatalf("expected 200 delete response, got %T", deleteResp)
	}

	deleteMissing, err := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "missing"})
	if err != nil {
		t.Fatalf("DeleteAlertRule(missing) error = %v", err)
	}
	if _, ok := deleteMissing.(gen.DeleteAlertRule404JSONResponse); !ok {
		t.Fatalf("expected 404 delete response, got %T", deleteMissing)
	}
}

func TestUpdateAlertRuleRejectsNilBody(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{RuleName: "rule"})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestUpdateAlertRuleReturnsValidationError(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		updateAlertFn: func(context.Context, string, string, cloudwatchmetrics.MetricAlertParams) (string, error) {
			return "", errors.New("invalid: window must be >= interval")
		},
	}, nil)
	resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "rule", Body: alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestUpdateAlertRuleReturns500OnUnexpected(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		updateAlertFn: func(context.Context, string, string, cloudwatchmetrics.MetricAlertParams) (string, error) {
			return "", errors.New("aws boom")
		},
	}, nil)
	resp, err := h.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
		RuleName: "rule", Body: alertRuleRequest(),
	})
	if err != nil {
		t.Fatalf("UpdateAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.UpdateAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestDeleteAlertRuleReturns500OnUnexpected(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		deleteAlertFn: func(context.Context, string, string) (string, error) {
			return "", errors.New("aws boom")
		},
	}, nil)
	resp, err := h.DeleteAlertRule(context.Background(), gen.DeleteAlertRuleRequestObject{RuleName: "rule"})
	if err != nil {
		t.Fatalf("DeleteAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.DeleteAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestGetAlertRuleReturns500OnUnexpected(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{
		getAlertFn: func(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error) {
			return nil, errors.New("aws boom")
		},
	}, nil)
	resp, err := h.GetAlertRule(context.Background(), gen.GetAlertRuleRequestObject{RuleName: "rule"})
	if err != nil {
		t.Fatalf("GetAlertRule() error = %v", err)
	}
	if _, ok := resp.(gen.GetAlertRule500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

func TestIsValidationError(t *testing.T) {
	if isValidationError(nil) {
		t.Fatal("nil should not be a validation error")
	}
	if !isValidationError(errors.New("invalid: too short")) {
		t.Fatal("'invalid:' prefix should mark as validation error")
	}
	if isValidationError(errors.New("aws boom")) {
		t.Fatal("non-invalid error should not be flagged")
	}
}

func TestParseUUIDInvalidReturnsFalse(t *testing.T) {
	if _, ok := parseUUID("not-a-uuid"); ok {
		t.Fatal("expected invalid uuid to return ok=false")
	}
	if _, ok := parseUUID(""); ok {
		t.Fatal("expected empty string to return ok=false")
	}
	if _, ok := parseUUID("11111111-1111-1111-1111-111111111111"); !ok {
		t.Fatal("expected valid uuid to return ok=true")
	}
}

func TestStrPtrAndStrPtrOrNil(t *testing.T) {
	if strPtrOrNil("") != nil {
		t.Fatal("strPtrOrNil(empty) should be nil")
	}
	if got := strPtrOrNil("x"); got == nil || *got != "x" {
		t.Fatalf("strPtrOrNil() = %v", got)
	}
	if got := strPtr("hello"); got == nil || *got != "hello" {
		t.Fatalf("strPtr() = %v", got)
	}
}

func TestToAlertRuleResponsePopulatesUUIDsWhenValid(t *testing.T) {
	got := toAlertRuleResponse(&cloudwatchmetrics.AlertDetail{
		Name:           "high-cpu",
		Namespace:      "payments",
		ProjectUID:     "11111111-1111-1111-1111-111111111111",
		EnvironmentUID: "22222222-2222-2222-2222-222222222222",
		ComponentUID:   "33333333-3333-3333-3333-333333333333",
		Metric:         "cpu_usage",
		Operator:       "gt",
		Window:         5 * time.Minute,
		Interval:       time.Minute,
		Enabled:        true,
	})
	if got.Metadata == nil || got.Metadata.ProjectUid == nil || got.Metadata.EnvironmentUid == nil || got.Metadata.ComponentUid == nil {
		t.Fatalf("expected all UIDs to be populated: %#v", got.Metadata)
	}
}

func TestToAlertRuleResponseDropsInvalidUUIDs(t *testing.T) {
	got := toAlertRuleResponse(&cloudwatchmetrics.AlertDetail{
		Name:           "rule",
		Namespace:      "ns",
		ProjectUID:     "not-a-uuid",
		EnvironmentUID: "still-not",
		ComponentUID:   "nope",
		Operator:       "lt",
		Window:         time.Minute,
		Interval:       time.Minute,
	})
	if got.Metadata == nil {
		t.Fatal("expected metadata to be present")
	}
	if got.Metadata.ProjectUid != nil || got.Metadata.EnvironmentUid != nil || got.Metadata.ComponentUid != nil {
		t.Fatalf("expected invalid UIDs to be dropped: %#v", got.Metadata)
	}
}

func TestNewMetricsHandlerNilOutsTypedNilForwarder(t *testing.T) {
	var typedNil *stubObserver
	h := NewMetricsHandler(&stubMetricsClient{}, HandlerOptions{ObserverClient: typedNil}, discardLogger())
	if h.observerClient != nil {
		t.Fatalf("expected typed-nil to be normalised to interface-nil, got %#v", h.observerClient)
	}
}
