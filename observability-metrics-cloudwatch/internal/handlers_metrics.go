// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/cloudwatchmetrics"
)

const (
	defaultMetricsStep = 5 * time.Minute
	maxStepSeconds     = 86400
)

type metricsClient interface {
	Ping(context.Context) error
	GetResourceMetrics(context.Context, cloudwatchmetrics.MetricsQueryParams) (*cloudwatchmetrics.ResourceMetricsResult, error)
	CreateAlert(context.Context, cloudwatchmetrics.MetricAlertParams) (string, error)
	UpdateAlert(context.Context, string, string, cloudwatchmetrics.MetricAlertParams) (string, error)
	DeleteAlert(context.Context, string, string) (string, error)
	GetAlert(context.Context, string, string) (*cloudwatchmetrics.AlertDetail, error)
	GetAlarmTagsByName(context.Context, string) (map[string]string, error)
}

type observerForwarder interface {
	ForwardAlert(context.Context, string, string, float64, time.Time) error
}

// MetricsHandler implements the generated StrictServerInterface.
type MetricsHandler struct {
	client                   metricsClient
	observerClient           observerForwarder
	scopeResolver            scopeResolver
	snsAllowSubscribeConfirm bool
	forwardRecovery          bool
	logger                   *slog.Logger
}

type HandlerOptions struct {
	ObserverClient           observerForwarder
	ScopeResolver            scopeResolver
	SNSAllowSubscribeConfirm bool
	ForwardRecovery          bool
}

func NewMetricsHandler(client metricsClient, opts HandlerOptions, logger *slog.Logger) *MetricsHandler {
	observerClient := opts.ObserverClient
	if isNilObserverForwarder(observerClient) {
		observerClient = nil
	}
	return &MetricsHandler{
		client:                   client,
		observerClient:           observerClient,
		scopeResolver:            opts.ScopeResolver,
		snsAllowSubscribeConfirm: opts.SNSAllowSubscribeConfirm,
		forwardRecovery:          opts.ForwardRecovery,
		logger:                   logger,
	}
}

func isNilObserverForwarder(f observerForwarder) bool {
	if f == nil {
		return true
	}
	v := reflect.ValueOf(f)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

var _ gen.StrictServerInterface = (*MetricsHandler)(nil)

// Health verifies AWS connectivity at startup; the handler simply confirms
// the process is up. This matches the logs adapter's behaviour and avoids
// tickling AWS on every readiness poll.
func (h *MetricsHandler) Health(_ context.Context, _ gen.HealthRequestObject) (gen.HealthResponseObject, error) {
	status := "healthy"
	return gen.Health200JSONResponse{Status: &status}, nil
}

// QueryMetrics handles POST /api/v1/metrics/query.
func (h *MetricsHandler) QueryMetrics(ctx context.Context, request gen.QueryMetricsRequestObject) (gen.QueryMetricsResponseObject, error) {
	if request.Body == nil {
		return badRequestMetrics("request body is required"), nil
	}
	if request.Body.SearchScope.Namespace == "" {
		return badRequestMetrics("searchScope.namespace is required"), nil
	}

	step := defaultMetricsStep
	if request.Body.Step != nil && *request.Body.Step != "" {
		parsed, err := time.ParseDuration(*request.Body.Step)
		if err != nil {
			return badRequestMetrics(fmt.Sprintf("invalid step format: %s", *request.Body.Step)), nil
		}
		if parsed <= 0 {
			return badRequestMetrics("step must be greater than 0"), nil
		}
		step = parsed
	}
	stepSec := int32(step.Seconds())
	if stepSec < 1 {
		stepSec = 1
	}
	if stepSec > maxStepSeconds {
		stepSec = maxStepSeconds
	}

	switch request.Body.Metric {
	case gen.Resource:
		return h.queryResourceMetrics(ctx, request.Body, stepSec)
	case gen.Http:
		return h.queryHTTPMetrics(), nil
	default:
		return badRequestMetrics(fmt.Sprintf("unknown metric type: %s", request.Body.Metric)), nil
	}
}

func (h *MetricsHandler) queryResourceMetrics(ctx context.Context, req *gen.MetricsQueryRequest, stepSec int32) (gen.QueryMetricsResponseObject, error) {
	params := cloudwatchmetrics.MetricsQueryParams{
		Namespace:      req.SearchScope.Namespace,
		ComponentUID:   derefString(req.SearchScope.ComponentUid),
		ProjectUID:     derefString(req.SearchScope.ProjectUid),
		EnvironmentUID: derefString(req.SearchScope.EnvironmentUid),
		StartTime:      req.StartTime,
		EndTime:        req.EndTime,
		StepSeconds:    stepSec,
	}
	if h.scopeResolver != nil {
		resolved, ok, err := h.scopeResolver.Resolve(ctx, ScopeResolutionParams{
			Namespace:      req.SearchScope.Namespace,
			Component:      derefString(req.SearchScope.Component),
			Project:        derefString(req.SearchScope.Project),
			Environment:    derefString(req.SearchScope.Environment),
			ComponentUID:   params.ComponentUID,
			ProjectUID:     params.ProjectUID,
			EnvironmentUID: params.EnvironmentUID,
		})
		if err != nil {
			h.logger.Warn("Failed to resolve metrics search scope", slog.Any("error", err))
		} else if ok {
			params.Namespace = resolved.Namespace
			params.ComponentUID = resolved.ComponentUID
			params.ProjectUID = resolved.ProjectUID
			params.EnvironmentUID = resolved.EnvironmentUID
		}
	}
	result, err := h.client.GetResourceMetrics(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query resource metrics",
			slog.String("namespace", params.Namespace),
			slog.Any("error", err),
		)
		return serverErrorMetrics(err.Error()), nil
	}

	resp := gen.ResourceMetricsTimeSeries{
		CpuUsage:       toItems(result.CPUUsage),
		CpuRequests:    toItems(result.CPURequests),
		CpuLimits:      toItems(result.CPULimits),
		MemoryUsage:    toItems(result.MemoryUsage),
		MemoryRequests: toItems(result.MemoryRequests),
		MemoryLimits:   toItems(result.MemoryLimits),
	}
	var union gen.MetricsQueryResponse
	if err := union.FromResourceMetricsTimeSeries(resp); err != nil {
		return serverErrorMetrics(fmt.Sprintf("failed to build response: %v", err)), nil
	}
	return metricsQueryOKResponse{union}, nil
}

// queryHTTPMetrics returns an empty HttpMetricsTimeSeries. HTTP RED metrics
// are not implemented in v0; the caller is informed via the response header.
func (h *MetricsHandler) queryHTTPMetrics() gen.QueryMetricsResponseObject {
	empty := []gen.MetricsTimeSeriesItem{}
	resp := gen.HttpMetricsTimeSeries{
		RequestCount:             &empty,
		SuccessfulRequestCount:   &empty,
		UnsuccessfulRequestCount: &empty,
		MeanLatency:              &empty,
		LatencyP50:               &empty,
		LatencyP90:               &empty,
		LatencyP99:               &empty,
	}
	var union gen.MetricsQueryResponse
	if err := union.FromHttpMetricsTimeSeries(resp); err != nil {
		return serverErrorMetrics(fmt.Sprintf("failed to build response: %v", err))
	}
	return httpMetricsQueryOKResponse{union}
}

// CreateAlertRule handles POST /api/v1alpha1/alerts/rules.
func (h *MetricsHandler) CreateAlertRule(ctx context.Context, request gen.CreateAlertRuleRequestObject) (gen.CreateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.CreateAlertRule400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr("request body is required"),
		}, nil
	}
	params := alertParamsFromRequest(request.Body)

	if existing, err := h.client.GetAlert(ctx, params.Namespace, params.Name); err == nil && existing != nil {
		return gen.CreateAlertRule409JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr("alert rule already exists"),
		}, nil
	} else if err != nil && !errors.Is(err, cloudwatchmetrics.ErrAlertNotFound) {
		h.logger.Warn("Duplicate-check failed for CreateAlertRule",
			slog.String("ruleName", params.Name),
			slog.Any("error", err),
		)
	}

	arn, err := h.client.CreateAlert(ctx, params)
	if err != nil {
		if isValidationError(err) {
			return gen.CreateAlertRule400JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr(err.Error()),
			}, nil
		}
		h.logger.Error("Failed to create alert", slog.String("alertName", params.Name), slog.Any("error", err))
		return gen.CreateAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr("internal server error"),
		}, nil
	}
	return gen.CreateAlertRule201JSONResponse(buildSyncResponse(gen.Created, params.Name, arn, gen.Synced)), nil
}

func (h *MetricsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	alert, err := h.client.GetAlert(ctx, "", request.RuleName)
	if err != nil {
		if errors.Is(err, cloudwatchmetrics.ErrAlertNotFound) {
			return gen.GetAlertRule404JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr("alert rule not found"),
			}, nil
		}
		h.logger.Error("Failed to get alert", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.GetAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr("internal server error"),
		}, nil
	}
	return gen.GetAlertRule200JSONResponse(toAlertRuleResponse(alert)), nil
}

func (h *MetricsHandler) UpdateAlertRule(ctx context.Context, request gen.UpdateAlertRuleRequestObject) (gen.UpdateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.UpdateAlertRule400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr("request body is required"),
		}, nil
	}
	params := alertParamsFromRequest(request.Body)
	params.Name = request.RuleName
	arn, err := h.client.UpdateAlert(ctx, params.Namespace, request.RuleName, params)
	if err != nil {
		if errors.Is(err, cloudwatchmetrics.ErrAlertNotFound) {
			return gen.UpdateAlertRule400JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr("alert rule not found"),
			}, nil
		}
		if isValidationError(err) {
			return gen.UpdateAlertRule400JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr(err.Error()),
			}, nil
		}
		h.logger.Error("Failed to update alert", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.UpdateAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr("internal server error"),
		}, nil
	}
	return gen.UpdateAlertRule200JSONResponse(buildSyncResponse(gen.Updated, request.RuleName, arn, gen.Synced)), nil
}

func (h *MetricsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	arn, err := h.client.DeleteAlert(ctx, "", request.RuleName)
	if err != nil {
		if errors.Is(err, cloudwatchmetrics.ErrAlertNotFound) {
			return gen.DeleteAlertRule404JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr("alert rule not found"),
			}, nil
		}
		h.logger.Error("Failed to delete alert", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.DeleteAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr("internal server error"),
		}, nil
	}
	return gen.DeleteAlertRule200JSONResponse(buildSyncResponse(gen.Deleted, request.RuleName, arn, gen.Synced)), nil
}

// --- mapping helpers -----------------------------------------------------

func alertParamsFromRequest(req *gen.AlertRuleRequest) cloudwatchmetrics.MetricAlertParams {
	params := cloudwatchmetrics.MetricAlertParams{
		Name:           req.Metadata.Name,
		Namespace:      req.Metadata.Namespace,
		ProjectUID:     req.Metadata.ProjectUid.String(),
		EnvironmentUID: req.Metadata.EnvironmentUid.String(),
		ComponentUID:   req.Metadata.ComponentUid.String(),
		Metric:         string(req.Source.Metric),
		Operator:       string(req.Condition.Operator),
		Threshold:      float64(req.Condition.Threshold),
		Enabled:        req.Condition.Enabled,
	}
	if d, err := cloudwatchmetrics.ParseDurationStrict(req.Condition.Window); err == nil {
		params.Window = d
	}
	if d, err := cloudwatchmetrics.ParseDurationStrict(req.Condition.Interval); err == nil {
		params.Interval = d
	}
	return params
}

func toAlertRuleResponse(alert *cloudwatchmetrics.AlertDetail) gen.AlertRuleResponse {
	operator := gen.AlertRuleResponseConditionOperator(alert.Operator)
	threshold := float32(alert.Threshold)
	window := cloudwatchmetrics.FormatDuration(alert.Window)
	interval := cloudwatchmetrics.FormatDuration(alert.Interval)
	enabled := alert.Enabled
	metric := gen.AlertRuleResponseSourceMetric(alert.Metric)

	resp := gen.AlertRuleResponse{}
	resp.Metadata = &struct {
		ComponentUid   *openapi_types.UUID `json:"componentUid,omitempty"`
		EnvironmentUid *openapi_types.UUID `json:"environmentUid,omitempty"`
		Name           *string             `json:"name,omitempty"`
		Namespace      *string             `json:"namespace,omitempty"`
		ProjectUid     *openapi_types.UUID `json:"projectUid,omitempty"`
	}{
		Name:      &alert.Name,
		Namespace: strPtrOrNil(alert.Namespace),
	}
	if uid, ok := parseUUID(alert.ProjectUID); ok {
		resp.Metadata.ProjectUid = &uid
	}
	if uid, ok := parseUUID(alert.EnvironmentUID); ok {
		resp.Metadata.EnvironmentUid = &uid
	}
	if uid, ok := parseUUID(alert.ComponentUID); ok {
		resp.Metadata.ComponentUid = &uid
	}
	resp.Source = &struct {
		Metric *gen.AlertRuleResponseSourceMetric `json:"metric,omitempty"`
	}{
		Metric: &metric,
	}
	resp.Condition = &struct {
		Enabled   *bool                                   `json:"enabled,omitempty"`
		Interval  *string                                 `json:"interval,omitempty"`
		Operator  *gen.AlertRuleResponseConditionOperator `json:"operator,omitempty"`
		Threshold *float32                                `json:"threshold,omitempty"`
		Window    *string                                 `json:"window,omitempty"`
	}{
		Enabled:   &enabled,
		Operator:  &operator,
		Threshold: &threshold,
		Window:    &window,
		Interval:  &interval,
	}
	return resp
}

func toItems(points []cloudwatchmetrics.TimeValuePoint) *[]gen.MetricsTimeSeriesItem {
	if len(points) == 0 {
		empty := []gen.MetricsTimeSeriesItem{}
		return &empty
	}
	items := make([]gen.MetricsTimeSeriesItem, 0, len(points))
	for i := range points {
		ts := points[i].Timestamp
		val := points[i].Value
		items = append(items, gen.MetricsTimeSeriesItem{Timestamp: &ts, Value: &val})
	}
	return &items
}

func buildSyncResponse(action gen.AlertingRuleSyncResponseAction, ruleName, backendID string, status gen.AlertingRuleSyncResponseStatus) gen.AlertingRuleSyncResponse {
	now := time.Now().UTC().Format(time.RFC3339)
	return gen.AlertingRuleSyncResponse{
		Action:        &action,
		Status:        &status,
		RuleLogicalId: &ruleName,
		RuleBackendId: &backendID,
		LastSyncedAt:  &now,
	}
}

func isValidationError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "invalid:")
}

func parseUUID(s string) (openapi_types.UUID, bool) {
	if s == "" {
		return openapi_types.UUID{}, false
	}
	parsed, err := uuid.Parse(s)
	if err != nil {
		return openapi_types.UUID{}, false
	}
	return openapi_types.UUID(parsed), true
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string { return &s }

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func errorTitle(t gen.ErrorResponseTitle) *gen.ErrorResponseTitle { return &t }

func badRequestMetrics(detail string) gen.QueryMetrics400JSONResponse {
	return gen.QueryMetrics400JSONResponse{
		Title:  errorTitle(gen.BadRequest),
		Detail: strPtr(detail),
	}
}

func serverErrorMetrics(detail string) gen.QueryMetrics500JSONResponse {
	return gen.QueryMetrics500JSONResponse{
		Title:  errorTitle(gen.InternalServerError),
		Detail: strPtr(detail),
	}
}

// metricsQueryOKResponse + httpMetricsQueryOKResponse preserve the union's
// MarshalJSON the generated 200 response would otherwise lose.
type metricsQueryOKResponse struct {
	gen.MetricsQueryResponse
}

func (r metricsQueryOKResponse) VisitQueryMetricsResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.MetricsQueryResponse)
}

type httpMetricsQueryOKResponse struct {
	gen.MetricsQueryResponse
}

func (r httpMetricsQueryOKResponse) VisitQueryMetricsResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-OpenChoreo-Adapter-Notice", "http-metrics-not-implemented")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.MetricsQueryResponse)
}
