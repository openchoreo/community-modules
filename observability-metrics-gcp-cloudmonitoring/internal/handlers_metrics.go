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

	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/cloudmonitoring"
)

const defaultStep = 5 * time.Minute

// Error codes follow the OBS-V1-M-GCP-<status> convention shared by the
// OpenChoreo metrics adapters.
const (
	errCodeBadRequest     = "OBS-V1-M-GCP-400"
	errCodeNotFound       = "OBS-V1-M-GCP-404"
	errCodeConflict       = "OBS-V1-M-GCP-409"
	errCodeInternal       = "OBS-V1-M-GCP-500"
	errCodeNotImplemented = "OBS-V1-M-GCP-501"
)

type metricsClient interface {
	GetResourceMetrics(context.Context, cloudmonitoring.MetricsQueryParams) (*cloudmonitoring.ResourceMetricsResult, error)
}

// alertClient is the alert-rule management surface. When nil, the
// alert-rule endpoints answer "not implemented" (nil-means-disabled).
type alertClient interface {
	CreateRule(context.Context, cloudmonitoring.RuleInput) (*cloudmonitoring.RuleResult, error)
	UpdateRule(context.Context, cloudmonitoring.RuleInput) (*cloudmonitoring.RuleResult, error)
	FindRuleByName(ctx context.Context, ruleName string) (*cloudmonitoring.RuleResult, string, error)
	DeleteRule(ctx context.Context, ruleName string) (*cloudmonitoring.RuleResult, error)
}

// observerForwarder forwards a fired alert to the OpenChoreo Observer.
type observerForwarder interface {
	ForwardAlert(ctx context.Context, ruleName, ruleNamespace string, alertValue float64, alertTimestamp time.Time) error
}

// MetricsHandler implements the generated strict server interface on top of
// the Cloud Monitoring query client. Alerting (alertClient/observerClient) is
// optional; when unset the alert-rule endpoints report not-implemented.
type MetricsHandler struct {
	client         metricsClient
	alertClient    alertClient
	observerClient observerForwarder
	logger         *slog.Logger
}

// NewMetricsHandler constructs a metrics-only handler. The alert-rule
// endpoints report not-implemented.
func NewMetricsHandler(client metricsClient, logger *slog.Logger) *MetricsHandler {
	return &MetricsHandler{client: client, logger: logger}
}

// NewMetricsHandlerWithAlerting constructs a handler with alert-rule management
// wired. A nil alertClient or observerClient leaves the corresponding
// endpoints reporting not-implemented.
func NewMetricsHandlerWithAlerting(client metricsClient, alerts alertClient, observer observerForwarder, logger *slog.Logger) *MetricsHandler {
	return &MetricsHandler{client: client, alertClient: alerts, observerClient: observer, logger: logger}
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
	if request.Body.StartTime.IsZero() || request.Body.EndTime.IsZero() {
		return badRequestMetrics("startTime and endTime are required"), nil
	}
	if !request.Body.EndTime.After(request.Body.StartTime) {
		return badRequestMetrics("endTime must be after startTime"), nil
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
	params := cloudmonitoring.MetricsQueryParams{
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
		h.logger.Error("failed to query resource metrics",
			slog.String("namespace", params.Namespace),
			slog.Any("error", err),
		)
		return serverErrorMetrics("internal server error"), nil
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
// components) derived from L7/RED traffic metrics. GKE system metrics carry
// only per-container CPU/memory counters — there is no pod-to-pod traffic
// data in Cloud Monitoring to build nodes/edges from.
func (h *MetricsHandler) QueryRuntimeTopology(_ context.Context, _ gen.QueryRuntimeTopologyRequestObject) (gen.QueryRuntimeTopologyResponseObject, error) {
	return gen.QueryRuntimeTopology501JSONResponse(
		makeError(gen.NotImplemented, errCodeNotImplemented, "runtime topology is not supported by the GCP Cloud Monitoring backend"),
	), nil
}

// The alert-rule endpoints (CreateAlertRule/GetAlertRule/UpdateAlertRule/
// DeleteAlertRule/HandleAlertWebhook) live in handlers_alerts.go.

// --- mapping helpers ---

func toItems(points []cloudmonitoring.TimeValuePoint) *[]gen.MetricsTimeSeriesItem {
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

func makeError(title gen.ErrorResponseTitle, code, detail string) gen.ErrorResponse {
	return gen.ErrorResponse{Title: &title, ErrorCode: strPtr(code), Detail: strPtr(detail)}
}

func badRequestMetrics(detail string) gen.QueryMetrics400JSONResponse {
	return gen.QueryMetrics400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, detail))
}

func serverErrorMetrics(detail string) gen.QueryMetrics500JSONResponse {
	return gen.QueryMetrics500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, detail))
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
