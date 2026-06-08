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

// metricsClient is the perfmetrics surface the handler depends on.
type metricsClient interface {
	GetResourceMetrics(context.Context, perfmetrics.MetricsQueryParams) (*perfmetrics.ResourceMetricsResult, error)
}

// alertClient is the azuremonitor surface the alert handlers depend on.
// Nil only in the query-only handler used by tests; the alert endpoints then
// return 501. main.go always wires a real client.
type alertClient interface {
	CreateRule(context.Context, azuremonitor.RuleInput) (*azuremonitor.RuleResult, error)
	UpdateRule(context.Context, azuremonitor.RuleInput) (*azuremonitor.RuleResult, error)
	FindRuleByName(context.Context, string) (*azuremonitor.RuleResult, string, error)
	DeleteRuleByAzureName(context.Context, string) error
}

// observerForwarder forwards a fired alert to the OpenChoreo Observer.
type observerForwarder interface {
	ForwardAlert(ctx context.Context, ruleName, ruleNamespace string, alertValue float64, alertTimestamp time.Time) error
}

// MetricsHandler implements the generated StrictServerInterface.
type MetricsHandler struct {
	client         metricsClient
	alertClient    alertClient
	observerClient observerForwarder
	logger         *slog.Logger
}

// NewMetricsHandler builds a query-only handler whose alert endpoints return
// 501. Used by tests that exercise only the metrics-query path; production
// uses NewMetricsHandlerWithAlerting.
func NewMetricsHandler(client metricsClient, logger *slog.Logger) *MetricsHandler {
	return &MetricsHandler{client: client, logger: logger}
}

// NewMetricsHandlerWithAlerting builds a handler with the alert + webhook path
// wired. This is what main.go constructs.
func NewMetricsHandlerWithAlerting(client metricsClient, ac alertClient, of observerForwarder, logger *slog.Logger) *MetricsHandler {
	return &MetricsHandler{client: client, alertClient: ac, observerClient: of, logger: logger}
}

var _ gen.StrictServerInterface = (*MetricsHandler)(nil)

// Health confirms the process is up. Azure/workspace reachability is verified
// once at boot (see perfmetrics.Client.Ping), so this stays cheap.
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
// therefore **not supported** for topology.
//
// We return a well-formed empty graph (200, populated summary window) rather
// than 404/501 so the Observer does not error when it probes the endpoint, and
// signal the limitation with the X-OpenChoreo-Adapter-Notice header. Populated
// topology on Azure requires a different backend (managed Prometheus fed by
// Cilium/Hubble L7 metrics, or Application Insights traces) — out of scope for
// this Log Analytics metrics module.
func (h *MetricsHandler) QueryRuntimeTopology(_ context.Context, request gen.QueryRuntimeTopologyRequestObject) (gen.QueryRuntimeTopologyResponseObject, error) {
	now := time.Now().UTC()
	start, end := now.Add(-1*time.Hour), now
	if request.Body != nil {
		start, end = request.Body.StartTime, request.Body.EndTime
	}
	nodes := []gen.RuntimeTopologyNode{}
	edges := []gen.RuntimeTopologyEdge{}
	return runtimeTopologyNotSupportedResponse{gen.QueryRuntimeTopology200JSONResponse{
		Nodes: &nodes,
		Edges: &edges,
		Summary: gen.RuntimeTopologySummary{
			StartTime:   start,
			EndTime:     end,
			GeneratedAt: now,
		},
	}}, nil
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

// runtimeTopologyNotSupportedResponse wraps the empty-graph 200 response with a
// notice header so callers can tell the empty graph is a backend limitation
// (the Log Analytics backend has no traffic data) rather than "no traffic in
// the window".
type runtimeTopologyNotSupportedResponse struct {
	gen.QueryRuntimeTopology200JSONResponse
}

func (r runtimeTopologyNotSupportedResponse) VisitQueryRuntimeTopologyResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-OpenChoreo-Adapter-Notice", "runtime-topology-not-supported")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.QueryRuntimeTopology200JSONResponse)
}
