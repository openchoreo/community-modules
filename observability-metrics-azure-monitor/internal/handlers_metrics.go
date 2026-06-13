// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/azuremonitor"
	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/perfmetrics"
)

const defaultStep = 5 * time.Minute

type metricsClient interface {
	GetResourceMetrics(context.Context, perfmetrics.MetricsQueryParams) (*perfmetrics.ResourceMetricsResult, error)
}

type alertClient interface {
	CreateRule(context.Context, azuremonitor.RuleInput) (*azuremonitor.RuleResult, error)
	UpdateRule(context.Context, azuremonitor.RuleInput) (*azuremonitor.RuleResult, error)
	FindRuleByName(context.Context, string) (*azuremonitor.RuleResult, string, error)
	DeleteRuleByAzureName(context.Context, string) error
}

type observerForwarder interface {
	ForwardAlert(ctx context.Context, ruleName, ruleNamespace string, alertValue float64, alertTimestamp time.Time) error
}

type MetricsHandler struct {
	client         metricsClient
	alertClient    alertClient
	observerClient observerForwarder
	logger         *slog.Logger
}

func NewMetricsHandler(client metricsClient, logger *slog.Logger) *MetricsHandler {
	return &MetricsHandler{client: client, logger: logger}
}

func NewMetricsHandlerWithAlerting(client metricsClient, ac alertClient, of observerForwarder, logger *slog.Logger) *MetricsHandler {
	return &MetricsHandler{client: client, alertClient: ac, observerClient: of, logger: logger}
}

var _ gen.StrictServerInterface = (*MetricsHandler)(nil)

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

	step := defaultStep
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

	switch request.Body.Metric {
	case gen.MetricsQueryRequestMetricResource:
		return h.queryResourceMetrics(ctx, request.Body, step)
	case gen.MetricsQueryRequestMetricHttp:
		return h.queryHTTPMetrics(), nil
	default:
		return badRequestMetrics(fmt.Sprintf("unknown metric type: %s", request.Body.Metric)), nil
	}
}

func (h *MetricsHandler) queryResourceMetrics(ctx context.Context, req *gen.MetricsQueryRequest, step time.Duration) (gen.QueryMetricsResponseObject, error) {
	params := perfmetrics.MetricsQueryParams{
		Namespace:      req.SearchScope.Namespace,
		ComponentUID:   derefString(req.SearchScope.ComponentUid),
		ProjectUID:     derefString(req.SearchScope.ProjectUid),
		EnvironmentUID: derefString(req.SearchScope.EnvironmentUid),
		StartTime:      req.StartTime,
		EndTime:        req.EndTime,
		Step:           step,
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
// are not implemented in v1; the caller is informed via the response header.
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

// QueryRuntimeTopology handles POST /api/v1alpha1/metrics/runtime-topology.
//
// Runtime topology is a *traffic* graph (request counts and latencies between
// components), which is derived from L7/RED traffic metrics or traces. The
// Container Insights Log Analytics backend (Perf / KubePodInventory) carries
// only CPU/memory counters and pod inventory — there is no pod-to-pod traffic
// data in the workspace to build nodes/edges from. This data source is
// therefore not supported for topology.
func (h *MetricsHandler) QueryRuntimeTopology(_ context.Context, _ gen.QueryRuntimeTopologyRequestObject) (gen.QueryRuntimeTopologyResponseObject, error) {
	return gen.QueryRuntimeTopology501JSONResponse(
		makeError(gen.NotImplemented, errCodeNotImplemented, "runtime topology is not supported by the Azure Container Insights backend"),
	), nil
}

// --- mapping helpers -----------------------------------------------------

func toItems(points []perfmetrics.TimeValuePoint) *[]gen.MetricsTimeSeriesItem {
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

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string { return &s }

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
// MarshalJSON that the generated 200 response would otherwise lose.
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
