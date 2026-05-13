// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/k8s"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/observer"
	"github.com/openchoreo/community-modules/observability-metrics-prometheus/internal/prometheus"
)

const (
	defaultMetricsStep = 5 * time.Minute
)

type MetricsHandler struct {
	promClient     *prometheus.Client
	k8sClient      *k8s.Client
	observerClient *observer.Client
	alertNamespace string
	logger         *slog.Logger
}

func NewMetricsHandler(
	promClient *prometheus.Client,
	k8sClient *k8s.Client,
	observerClient *observer.Client,
	alertNamespace string,
	logger *slog.Logger,
) *MetricsHandler {
	return &MetricsHandler{
		promClient:     promClient,
		k8sClient:      k8sClient,
		observerClient: observerClient,
		alertNamespace: alertNamespace,
		logger:         logger,
	}
}

var _ gen.StrictServerInterface = (*MetricsHandler)(nil)

// Health checks Prometheus connectivity and returns health status.
func (h *MetricsHandler) Health(ctx context.Context, _ gen.HealthRequestObject) (gen.HealthResponseObject, error) {
	if h.promClient == nil {
		status := "unhealthy"
		errMsg := "prometheus client not configured"
		return gen.Health503JSONResponse{
			Status: &status,
			Error:  &errMsg,
		}, nil
	}

	if err := h.promClient.HealthCheck(ctx); err != nil {
		status := "unhealthy"
		errMsg := fmt.Sprintf("prometheus: %v", err)
		return gen.Health503JSONResponse{
			Status: &status,
			Error:  &errMsg,
		}, nil
	}

	status := "healthy"
	return gen.Health200JSONResponse{
		Status: &status,
	}, nil
}

// QueryMetrics queries Prometheus for resource or HTTP metrics.
func (h *MetricsHandler) QueryMetrics(ctx context.Context, request gen.QueryMetricsRequestObject) (gen.QueryMetricsResponseObject, error) {
	if h.promClient == nil {
		return serverErrorMetrics("Prometheus client not configured"), nil
	}

	if request.Body == nil {
		return badRequestMetrics("request body is required"), nil
	}

	scope := request.Body.SearchScope
	if scope.Namespace == "" {
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

	componentUID := derefString(scope.ComponentUid)
	projectUID := derefString(scope.ProjectUid)
	environmentUID := derefString(scope.EnvironmentUid)

	labelFilter := prometheus.BuildLabelFilter(scope.Namespace, componentUID, projectUID, environmentUID)
	scopeLabels := prometheus.BuildScopeLabelNames(componentUID, projectUID, environmentUID)
	groupLeftClause := prometheus.BuildGroupLeftClause(scopeLabels)
	sumByClause := prometheus.BuildSumByClause("", scopeLabels)

	startTime := request.Body.StartTime
	endTime := request.Body.EndTime

	switch request.Body.Metric {
	case gen.MetricsQueryRequestMetricResource:
		return h.queryResourceMetrics(ctx, labelFilter, sumByClause, groupLeftClause, startTime, endTime, step)
	case gen.MetricsQueryRequestMetricHttp:
		return h.queryHTTPMetrics(ctx, labelFilter, sumByClause, groupLeftClause, startTime, endTime, step)
	default:
		return badRequestMetrics(fmt.Sprintf("unknown metric type: %s", request.Body.Metric)), nil
	}
}

func (h *MetricsHandler) queryResourceMetrics(
	ctx context.Context,
	labelFilter, sumByClause, groupLeftClause string,
	startTime, endTime time.Time,
	step time.Duration,
) (gen.QueryMetricsResponseObject, error) {
	result := gen.ResourceMetricsTimeSeries{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	type querySpec struct {
		name    string
		queryFn func() string
		assign  func(items *[]gen.MetricsTimeSeriesItem)
	}

	queries := []querySpec{
		{"cpuUsage", func() string {
			return prometheus.BuildCPUUsageQuery(labelFilter, sumByClause, groupLeftClause)
		}, func(items *[]gen.MetricsTimeSeriesItem) { result.CpuUsage = items }},
		{"cpuRequests", func() string {
			return prometheus.BuildCPURequestsQuery(labelFilter, sumByClause, groupLeftClause)
		}, func(items *[]gen.MetricsTimeSeriesItem) { result.CpuRequests = items }},
		{"cpuLimits", func() string {
			return prometheus.BuildCPULimitsQuery(labelFilter, sumByClause, groupLeftClause)
		}, func(items *[]gen.MetricsTimeSeriesItem) { result.CpuLimits = items }},
		{"memoryUsage", func() string {
			return prometheus.BuildMemoryUsageQuery(labelFilter, sumByClause, groupLeftClause)
		}, func(items *[]gen.MetricsTimeSeriesItem) { result.MemoryUsage = items }},
		{"memoryRequests", func() string {
			return prometheus.BuildMemoryRequestsQuery(labelFilter, sumByClause, groupLeftClause)
		}, func(items *[]gen.MetricsTimeSeriesItem) { result.MemoryRequests = items }},
		{"memoryLimits", func() string {
			return prometheus.BuildMemoryLimitsQuery(labelFilter, sumByClause, groupLeftClause)
		}, func(items *[]gen.MetricsTimeSeriesItem) { result.MemoryLimits = items }},
	}

	for _, q := range queries {
		wg.Add(1)
		go func(q querySpec) {
			defer wg.Done()
			query := q.queryFn()
			h.logger.Debug("Resource metric query", "name", q.name, "query", query)
			resp, err := h.promClient.QueryRangeTimeSeries(ctx, query, startTime, endTime, step)
			if err != nil {
				h.logger.Error("Failed to query resource metric", "metric", q.name, "error", err)
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("sub-query %q failed: %w", q.name, err)
				}
				mu.Unlock()
				return
			}
			if len(resp.Data.Result) > 0 {
				items := convertToMetricsTimeSeriesItems(prometheus.ConvertTimeSeriesToTimeValuePoints(resp.Data.Result[0]))
				mu.Lock()
				q.assign(&items)
				mu.Unlock()
			}
		}(q)
	}

	wg.Wait()
	if firstErr != nil {
		return serverErrorMetrics(firstErr.Error()), nil
	}

	var response gen.MetricsQueryResponse
	if err := response.FromResourceMetricsTimeSeries(result); err != nil {
		return serverErrorMetrics(fmt.Sprintf("failed to build response: %v", err)), nil
	}
	return metricsQueryOKResponse{response}, nil
}

func (h *MetricsHandler) queryHTTPMetrics(
	ctx context.Context,
	labelFilter, sumByClause, groupLeftClause string,
	startTime, endTime time.Time,
	step time.Duration,
) (gen.QueryMetricsResponseObject, error) {
	result := gen.HttpMetricsTimeSeries{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	type querySpec struct {
		name    string
		queryFn func(string, string, string) string
		assign  func(items *[]gen.MetricsTimeSeriesItem)
	}

	queries := []querySpec{
		{"requestCount", prometheus.BuildHTTPRequestCountQuery, func(items *[]gen.MetricsTimeSeriesItem) { result.RequestCount = items }},
		{"successfulRequestCount", prometheus.BuildSuccessfulHTTPRequestCountQuery, func(items *[]gen.MetricsTimeSeriesItem) { result.SuccessfulRequestCount = items }},
		{"unsuccessfulRequestCount", prometheus.BuildUnsuccessfulHTTPRequestCountQuery, func(items *[]gen.MetricsTimeSeriesItem) { result.UnsuccessfulRequestCount = items }},
		{"meanLatency", prometheus.BuildMeanHTTPRequestLatencyQuery, func(items *[]gen.MetricsTimeSeriesItem) { result.MeanLatency = items }},
		{"latencyP50", prometheus.Build50thPercentileHTTPRequestLatencyQuery, func(items *[]gen.MetricsTimeSeriesItem) { result.LatencyP50 = items }},
		{"latencyP90", prometheus.Build90thPercentileHTTPRequestLatencyQuery, func(items *[]gen.MetricsTimeSeriesItem) { result.LatencyP90 = items }},
		{"latencyP99", prometheus.Build99thPercentileHTTPRequestLatencyQuery, func(items *[]gen.MetricsTimeSeriesItem) { result.LatencyP99 = items }},
	}

	for _, q := range queries {
		wg.Add(1)
		go func(q querySpec) {
			defer wg.Done()
			query := q.queryFn(labelFilter, sumByClause, groupLeftClause)
			h.logger.Debug("HTTP metric query", "name", q.name, "query", query)
			resp, err := h.promClient.QueryRangeTimeSeries(ctx, query, startTime, endTime, step)
			if err != nil {
				h.logger.Error("Failed to query HTTP metric", "metric", q.name, "error", err)
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("sub-query %q failed: %w", q.name, err)
				}
				mu.Unlock()
				return
			}
			if len(resp.Data.Result) > 0 {
				items := convertToMetricsTimeSeriesItems(prometheus.ConvertTimeSeriesToTimeValuePoints(resp.Data.Result[0]))
				mu.Lock()
				q.assign(&items)
				mu.Unlock()
			}
		}(q)
	}

	wg.Wait()
	if firstErr != nil {
		return serverErrorMetrics(firstErr.Error()), nil
	}

	var response gen.MetricsQueryResponse
	if err := response.FromHttpMetricsTimeSeries(result); err != nil {
		return serverErrorMetrics(fmt.Sprintf("failed to build response: %v", err)), nil
	}
	return metricsQueryOKResponse{response}, nil
}

// CreateAlertRule creates a new PrometheusRule CR.
func (h *MetricsHandler) CreateAlertRule(ctx context.Context, request gen.CreateAlertRuleRequestObject) (gen.CreateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.CreateAlertRule400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr("request body is required"),
		}, nil
	}

	if h.k8sClient == nil {
		return gen.CreateAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr("alert rule management is not available"),
		}, nil
	}

	params := alertRuleParamsFromRequest(*request.Body)
	rule, err := prometheus.BuildPrometheusRule(params, h.alertNamespace)
	if err != nil {
		return gen.CreateAlertRule400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr(fmt.Sprintf("failed to build alert rule: %v", err)),
		}, nil
	}

	exists, err := h.k8sClient.PrometheusRuleExists(ctx, params.Name)
	if err != nil {
		return gen.CreateAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr(fmt.Sprintf("failed to check existing rule: %v", err)),
		}, nil
	}
	if exists {
		return gen.CreateAlertRule409JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr(fmt.Sprintf("alert rule %q already exists", params.Name)),
		}, nil
	}

	if err := h.k8sClient.CreatePrometheusRule(ctx, rule); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return gen.CreateAlertRule409JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr(fmt.Sprintf("alert rule %q already exists", params.Name)),
			}, nil
		}
		return gen.CreateAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr(fmt.Sprintf("failed to create alert rule: %v", err)),
		}, nil
	}

	// Re-fetch to get the assigned UID
	created, err := h.k8sClient.GetPrometheusRule(ctx, params.Name)
	backendID := ""
	status := gen.Synced
	if err == nil {
		backendID = string(created.UID)
		h.logger.Info("PrometheusRule created", "rule_name", params.Name, "backend_id", backendID)
	} else {
		// Log the error with context
		h.logger.Error("Failed to retrieve backend ID after creating PrometheusRule", "error", err, "rule_name", params.Name)
		status = gen.Failed
	}

	return gen.CreateAlertRule201JSONResponse(buildSyncResponse(gen.Created, params.Name, backendID, status)), nil
}

// GetAlertRule retrieves a PrometheusRule CR.
func (h *MetricsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	if h.k8sClient == nil {
		return gen.GetAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr("alert rule management is not available"),
		}, nil
	}

	existing, err := h.k8sClient.GetPrometheusRule(ctx, request.RuleName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return gen.GetAlertRule404JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr(fmt.Sprintf("alert rule %q not found", request.RuleName)),
			}, nil
		}
		return gen.GetAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr(fmt.Sprintf("failed to get alert rule: %v", err)),
		}, nil
	}

	resp, err := mapPrometheusRuleToAlertRuleResponse(existing, request.RuleName)
	if err != nil {
		return gen.GetAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr(fmt.Sprintf("failed to map alert rule: %v", err)),
		}, nil
	}

	return gen.GetAlertRule200JSONResponse(*resp), nil
}

// UpdateAlertRule updates an existing PrometheusRule CR.
func (h *MetricsHandler) UpdateAlertRule(ctx context.Context, request gen.UpdateAlertRuleRequestObject) (gen.UpdateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.UpdateAlertRule400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr("request body is required"),
		}, nil
	}

	if h.k8sClient == nil {
		return gen.UpdateAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr("alert rule management is not available"),
		}, nil
	}

	params := alertRuleParamsFromRequest(*request.Body)
	if params.Name != request.RuleName {
		return gen.UpdateAlertRule400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr(fmt.Sprintf("metadata.name %q must match ruleName %q", params.Name, request.RuleName)),
		}, nil
	}

	newRule, err := prometheus.BuildPrometheusRule(params, h.alertNamespace)
	if err != nil {
		return gen.UpdateAlertRule400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr(fmt.Sprintf("failed to build alert rule: %v", err)),
		}, nil
	}

	existing, err := h.k8sClient.GetPrometheusRule(ctx, request.RuleName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return gen.UpdateAlertRule400JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr(fmt.Sprintf("alert rule %q not found", request.RuleName)),
			}, nil
		}
		return gen.UpdateAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr(fmt.Sprintf("failed to get existing alert rule: %v", err)),
		}, nil
	}

	backendID := string(existing.UID)

	if prometheusSpecsAreEqual(existing, newRule) {
		h.logger.Debug("PrometheusRule unchanged, skipping update", "rule_name", request.RuleName)
		return gen.UpdateAlertRule200JSONResponse(buildSyncResponse(gen.Unchanged, request.RuleName, backendID, gen.Synced)), nil
	}

	existing.Spec = newRule.Spec
	if err := h.k8sClient.UpdatePrometheusRule(ctx, existing); err != nil {
		return gen.UpdateAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr(fmt.Sprintf("failed to update alert rule: %v", err)),
		}, nil
	}

	h.logger.Info("PrometheusRule updated", "rule_name", request.RuleName, "backend_id", backendID)
	return gen.UpdateAlertRule200JSONResponse(buildSyncResponse(gen.Updated, request.RuleName, backendID, gen.Synced)), nil
}

// DeleteAlertRule deletes a PrometheusRule CR.
func (h *MetricsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	if h.k8sClient == nil {
		return gen.DeleteAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr("alert rule management is not available"),
		}, nil
	}

	existing, err := h.k8sClient.GetPrometheusRule(ctx, request.RuleName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return gen.DeleteAlertRule404JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr(fmt.Sprintf("alert rule %q not found", request.RuleName)),
			}, nil
		}
		return gen.DeleteAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr(fmt.Sprintf("failed to get alert rule: %v", err)),
		}, nil
	}

	backendID := string(existing.UID)
	if err := h.k8sClient.DeletePrometheusRule(ctx, request.RuleName); err != nil {
		return gen.DeleteAlertRule500JSONResponse{
			Title:  errorTitle(gen.InternalServerError),
			Detail: strPtr(fmt.Sprintf("failed to delete alert rule: %v", err)),
		}, nil
	}

	h.logger.Info("PrometheusRule deleted", "rule_name", request.RuleName, "backend_id", backendID)
	return gen.DeleteAlertRule200JSONResponse(buildSyncResponse(gen.Deleted, request.RuleName, backendID, gen.Synced)), nil
}

// HandleAlertWebhook receives an Alertmanager webhook and forwards it to the observer.
func (h *MetricsHandler) HandleAlertWebhook(ctx context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	if request.Body == nil {
		return gen.HandleAlertWebhook400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr("request body is required"),
		}, nil
	}

	body := *request.Body

	alerts, ok := body["alerts"]
	if !ok {
		return gen.HandleAlertWebhook400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr("missing 'alerts' field in webhook payload"),
		}, nil
	}

	alertsList, ok := alerts.([]interface{})
	if !ok || len(alertsList) == 0 {
		return gen.HandleAlertWebhook400JSONResponse{
			Title:  errorTitle(gen.BadRequest),
			Detail: strPtr("expected non-empty 'alerts' array in webhook payload"),
		}, nil
	}

	forwarded := 0
	for _, alertRaw := range alertsList {
		alertMap, ok := alertRaw.(map[string]interface{})
		if !ok {
			continue
		}

		status, _ := alertMap["status"].(string)
		if strings.ToLower(strings.TrimSpace(status)) != "firing" {
			continue
		}

		ruleName, ruleNamespace, alertValue, alertTimestamp, err := extractAlertFields(alertMap)
		if err != nil {
			return gen.HandleAlertWebhook400JSONResponse{
				Title:  errorTitle(gen.BadRequest),
				Detail: strPtr(fmt.Sprintf("malformed alert in webhook: %v", err)),
			}, nil
		}

		if h.observerClient == nil {
			h.logger.Error("Observer client not configured, cannot forward alert",
				slog.String("ruleName", ruleName),
				slog.String("ruleNamespace", ruleNamespace),
			)
			return gen.HandleAlertWebhook500JSONResponse{
				Title:  errorTitle(gen.InternalServerError),
				Detail: strPtr("observer client not configured"),
			}, nil
		}

		if err := h.observerClient.ForwardAlert(ctx, ruleName, ruleNamespace, alertValue, alertTimestamp); err != nil {
			h.logger.Error("Failed to forward alert webhook to observer API",
				slog.String("ruleName", ruleName),
				slog.String("ruleNamespace", ruleNamespace),
				slog.Any("error", err),
			)
			return gen.HandleAlertWebhook500JSONResponse{
				Title:  errorTitle(gen.InternalServerError),
				Detail: strPtr(fmt.Sprintf("failed to forward alert: %v", err)),
			}, nil
		}
		forwarded++
	}

	message := fmt.Sprintf("processed webhook successfully; forwarded %d firing alert(s)", forwarded)
	successStatus := gen.Success
	return gen.HandleAlertWebhook200JSONResponse{
		Status:  &successStatus,
		Message: &message,
	}, nil
}

// extractAlertFields extracts rule name, namespace, alert value, and timestamp from an Alertmanager alert.
func extractAlertFields(alert map[string]interface{}) (string, string, float32, time.Time, error) {
	annotations, ok := alert["annotations"].(map[string]interface{})
	if !ok {
		return "", "", 0, time.Time{}, fmt.Errorf("missing annotations")
	}

	ruleName, _ := annotations["rule_name"].(string)
	ruleName = strings.TrimSpace(ruleName)
	if ruleName == "" {
		return "", "", 0, time.Time{}, fmt.Errorf("missing rule_name annotation")
	}

	ruleNamespace, _ := annotations["rule_namespace"].(string)
	ruleNamespace = strings.TrimSpace(ruleNamespace)
	if ruleNamespace == "" {
		return "", "", 0, time.Time{}, fmt.Errorf("missing rule_namespace annotation")
	}

	alertValueStr, _ := annotations["alert_value"].(string)
	alertValueStr = strings.TrimSpace(alertValueStr)
	if alertValueStr == "" {
		return "", "", 0, time.Time{}, fmt.Errorf("missing alert_value annotation")
	}

	var alertValue float64
	if _, err := fmt.Sscanf(alertValueStr, "%f", &alertValue); err != nil {
		return "", "", 0, time.Time{}, fmt.Errorf("invalid alert_value: %w", err)
	}

	var alertTimestamp time.Time
	if startsAt, ok := alert["startsAt"].(string); ok {
		t, err := time.Parse(time.RFC3339, startsAt)
		if err == nil {
			alertTimestamp = t
		}
	}
	if alertTimestamp.IsZero() {
		alertTimestamp = time.Now().UTC()
	}

	return ruleName, ruleNamespace, float32(alertValue), alertTimestamp, nil
}

// --- Helper functions ---

func alertRuleParamsFromRequest(req gen.AlertRuleRequest) prometheus.AlertRuleParams {
	return prometheus.AlertRuleParams{
		Name:           req.Metadata.Name,
		Namespace:      req.Metadata.Namespace,
		ComponentUID:   req.Metadata.ComponentUid.String(),
		ProjectUID:     req.Metadata.ProjectUid.String(),
		EnvironmentUID: req.Metadata.EnvironmentUid.String(),
		Metric:         string(req.Source.Metric),
		Enabled:        req.Condition.Enabled,
		Window:         req.Condition.Window,
		Interval:       req.Condition.Interval,
		Operator:       string(req.Condition.Operator),
		Threshold:      float64(req.Condition.Threshold),
	}
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

func mapPrometheusRuleToAlertRuleResponse(pr *monitoringv1.PrometheusRule, ruleName string) (*gen.AlertRuleResponse, error) {
	if len(pr.Spec.Groups) == 0 || len(pr.Spec.Groups[0].Rules) == 0 {
		return nil, fmt.Errorf("PrometheusRule has no rule groups or rules")
	}

	group := pr.Spec.Groups[0]
	rule := group.Rules[0]

	resp := &gen.AlertRuleResponse{}

	name := ruleName
	namespace := rule.Annotations["rule_namespace"]

	resp.Metadata = &struct {
		ComponentUid   *openapi_types.UUID `json:"componentUid,omitempty"`
		EnvironmentUid *openapi_types.UUID `json:"environmentUid,omitempty"`
		Name           *string             `json:"name,omitempty"`
		Namespace      *string             `json:"namespace,omitempty"`
		ProjectUid     *openapi_types.UUID `json:"projectUid,omitempty"`
	}{
		Name:      &name,
		Namespace: &namespace,
	}

	// Extract UIDs from the PromQL expression label filters
	expr := rule.Expr.String()
	if uid := parseUUID(extractPromLabelValue(expr, "label_openchoreo_dev_component_uid")); uid != nil {
		resp.Metadata.ComponentUid = uid
	}
	if uid := parseUUID(extractPromLabelValue(expr, "label_openchoreo_dev_project_uid")); uid != nil {
		resp.Metadata.ProjectUid = uid
	}
	if uid := parseUUID(extractPromLabelValue(expr, "label_openchoreo_dev_environment_uid")); uid != nil {
		resp.Metadata.EnvironmentUid = uid
	}

	// Source
	metric := detectMetricType(expr)
	resp.Source = &struct {
		Metric *gen.AlertRuleResponseSourceMetric `json:"metric,omitempty"`
	}{
		Metric: &metric,
	}

	// Condition
	operator, threshold := extractPromOperatorAndThreshold(expr)
	enabled := true
	resp.Condition = &struct {
		Enabled   *bool                                   `json:"enabled,omitempty"`
		Interval  *string                                 `json:"interval,omitempty"`
		Operator  *gen.AlertRuleResponseConditionOperator `json:"operator,omitempty"`
		Threshold *float32                                `json:"threshold,omitempty"`
		Window    *string                                 `json:"window,omitempty"`
	}{
		Enabled: &enabled,
	}
	if group.Interval != nil {
		interval := string(*group.Interval)
		resp.Condition.Interval = &interval
	}
	if rule.For != nil {
		window := string(*rule.For)
		resp.Condition.Window = &window
	}
	if operator != "" {
		op := gen.AlertRuleResponseConditionOperator(operator)
		resp.Condition.Operator = &op
	}
	if threshold != nil {
		resp.Condition.Threshold = threshold
	}

	return resp, nil
}

func extractPromLabelValue(expr, labelName string) string {
	search := labelName + `="`
	idx := strings.Index(expr, search)
	if idx < 0 {
		return ""
	}
	start := idx + len(search)
	end := strings.Index(expr[start:], `"`)
	if end < 0 {
		return ""
	}
	return expr[start : start+end]
}

func detectMetricType(expr string) gen.AlertRuleResponseSourceMetric {
	if strings.Contains(expr, "node_cpu_hourly_cost") {
		return gen.AlertRuleResponseSourceMetricBudget
	}
	if strings.Contains(expr, "container_cpu_usage_seconds_total") {
		return gen.AlertRuleResponseSourceMetricCpuUsage
	}
	if strings.Contains(expr, "container_memory_working_set_bytes") {
		return gen.AlertRuleResponseSourceMetricMemoryUsage
	}
	return gen.AlertRuleResponseSourceMetricCpuUsage
}

func extractPromOperatorAndThreshold(expr string) (string, *float32) {
	operators := []struct {
		symbol string
		name   string
	}{
		{">=", "gte"},
		{"<=", "lte"},
		{">", "gt"},
		{"<", "lt"},
		{"==", "eq"},
	}

	// Try percentage-based pattern first (cpu_usage, memory_usage: "* 100 > 80")
	for _, op := range operators {
		pattern := "* 100 " + op.symbol + " "
		idx := strings.LastIndex(expr, pattern)
		if idx < 0 {
			continue
		}
		valueStr := strings.TrimSpace(expr[idx+len(pattern):])
		var value float64
		if _, err := fmt.Sscanf(valueStr, "%f", &value); err == nil {
			f32 := float32(value)
			return op.name, &f32
		}
	}

	// Try raw value pattern (budget: ") > 5")
	for _, op := range operators {
		pattern := ") " + op.symbol + " "
		idx := strings.LastIndex(expr, pattern)
		if idx < 0 {
			continue
		}
		valueStr := strings.TrimSpace(expr[idx+len(pattern):])
		var value float64
		if _, err := fmt.Sscanf(valueStr, "%f", &value); err == nil {
			f32 := float32(value)
			return op.name, &f32
		}
	}

	return "", nil
}

func prometheusSpecsAreEqual(existing, newRule *monitoringv1.PrometheusRule) bool {
	existingJSON, err := json.Marshal(existing.Spec)
	if err != nil {
		return false
	}
	newJSON, err := json.Marshal(newRule.Spec)
	if err != nil {
		return false
	}
	return string(existingJSON) == string(newJSON)
}

func convertToMetricsTimeSeriesItems(points []prometheus.TimeValuePoint) []gen.MetricsTimeSeriesItem {
	items := make([]gen.MetricsTimeSeriesItem, 0, len(points))
	for _, p := range points {
		t, err := time.Parse(time.RFC3339, p.Time)
		if err != nil {
			continue
		}
		value := p.Value
		items = append(items, gen.MetricsTimeSeriesItem{
			Timestamp: &t,
			Value:     &value,
		})
	}
	return items
}

func parseUUID(s string) *openapi_types.UUID {
	if s == "" {
		return nil
	}
	uid, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &uid
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string {
	return &s
}

func errorTitle(t gen.ErrorResponseTitle) *gen.ErrorResponseTitle {
	return &t
}

// metricsQueryOKResponse wraps MetricsQueryResponse to preserve its MarshalJSON method.
// The generated QueryMetrics200JSONResponse is a type definition (not alias), so it loses
// the custom MarshalJSON needed to serialize the internal union field.
type metricsQueryOKResponse struct {
	gen.MetricsQueryResponse
}

func (r metricsQueryOKResponse) VisitQueryMetricsResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	return json.NewEncoder(w).Encode(r.MetricsQueryResponse)
}

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

// ----------------------------
// Runtime topoloy
// ----------------------------

// QueryRuntimeTopology returns the live HTTP traffic topology (nodes + edges
// with aggregated metrics) for a project in a given environment.
//
// Current implementation scope (minimal scaffolding):
//   - component -> component edges via Hubble's hubble_http_requests_total,
//     aggregated by (source component UID, destination component UID).
//   - request count only — error count and latency percentiles are TODO.
//   - gateway -> component and component -> external edges are TODO.
//   - per-node aggregates are TODO.
func (h *MetricsHandler) QueryRuntimeTopology(
	ctx context.Context,
	request gen.QueryRuntimeTopologyRequestObject,
) (gen.QueryRuntimeTopologyResponseObject, error) {
	if h.promClient == nil {
		return badOrServerErrorRuntimeTopology500("Prometheus client not configured"), nil
	}
	if request.Body == nil {
		return badOrServerErrorRuntimeTopology400("request body is required"), nil
	}

	scope := request.Body.SearchScope
	if scope.Namespace == "" {
		return badOrServerErrorRuntimeTopology400("searchScope.namespace is required"), nil
	}
	projectUID := derefString(scope.ProjectUid)
	environmentUID := derefString(scope.EnvironmentUid)
	if projectUID == "" {
		return badOrServerErrorRuntimeTopology400("searchScope.projectUid is required"), nil
	}
	if environmentUID == "" {
		return badOrServerErrorRuntimeTopology400("searchScope.environmentUid is required"), nil
	}
	componentUIDFilter := derefString(scope.ComponentUid)

	startTime := request.Body.StartTime
	endTime := request.Body.EndTime
	durationSec := endTime.Sub(startTime).Seconds()
	if durationSec <= 0 {
		return badOrServerErrorRuntimeTopology400("endTime must be after startTime"), nil
	}

	durationStr := fmt.Sprintf("%.0fs", durationSec)

	// Filter by project + environment UIDs. Component UID filter is applied in
	// buildEdgeMetricMap so edges touching the component on either side are included.
	labelFilter := prometheus.BuildRuntimeTopologyLabelFilter(scope.Namespace, "", projectUID, environmentUID)

	type runtimeTopologyQuerySpec struct {
		name    string
		queryFn func(string) string
		dest    *map[edgeKey]float64
	}

	var (
		mu              sync.Mutex
		firstErr        error
		requestCountMap = map[edgeKey]float64{}
		errorCountMap   = map[edgeKey]float64{}
		avgLatencyMap   = map[edgeKey]float64{}
		p50LatencyMap   = map[edgeKey]float64{}
		p90LatencyMap   = map[edgeKey]float64{}
		p99LatencyMap   = map[edgeKey]float64{}
	)

	queries := []runtimeTopologyQuerySpec{
		{"requestCount", func(f string) string {
			return prometheus.BuildRuntimeTopologyComponentEdgeRequestCountQuery(durationStr, f)
		}, &requestCountMap},
		{"errorCount", func(f string) string {
			return prometheus.BuildRuntimeTopologyComponentEdgeErrorCountQuery(durationStr, f)
		}, &errorCountMap},
		{"avgLatency", func(f string) string {
			return prometheus.BuildRuntimeTopologyComponentEdgeMeanLatencyQuery(durationStr, f)
		}, &avgLatencyMap},
		{"p50Latency", func(f string) string {
			return prometheus.BuildRuntimeTopologyComponentEdgeLatencyPercentileQuery("0.5", durationStr, f)
		}, &p50LatencyMap},
		{"p90Latency", func(f string) string {
			return prometheus.BuildRuntimeTopologyComponentEdgeLatencyPercentileQuery("0.9", durationStr, f)
		}, &p90LatencyMap},
		{"p99Latency", func(f string) string {
			return prometheus.BuildRuntimeTopologyComponentEdgeLatencyPercentileQuery("0.99", durationStr, f)
		}, &p99LatencyMap},
	}

	var wg sync.WaitGroup
	for _, q := range queries {
		wg.Add(1)
		go func(q runtimeTopologyQuerySpec) {
			defer wg.Done()
			query := q.queryFn(labelFilter)
			h.logger.Debug("Runtime topology query", "name", q.name, "query", query)
			resp, err := h.promClient.QueryInstant(ctx, query, endTime)
			if err != nil {
				h.logger.Error("Runtime topology sub-query failed", "metric", q.name, "error", err)
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("sub-query %q failed: %w", q.name, err)
				}
				mu.Unlock()
				return
			}
			m := buildEdgeMetricMap(resp.Data.Result, componentUIDFilter)
			mu.Lock()
			*q.dest = m
			mu.Unlock()
		}(q)
	}
	wg.Wait()

	if firstErr != nil {
		h.logger.Error("Runtime topology query failed", "error", firstErr)
		return badOrServerErrorRuntimeTopology500(fmt.Sprintf("query failed: %v", firstErr)), nil
	}

	edges := buildRuntimeTopologyEdges(
		scope.Namespace, projectUID,
		requestCountMap, errorCountMap, avgLatencyMap, p50LatencyMap, p90LatencyMap, p99LatencyMap,
	)

	response := gen.RuntimeTopologyResponse{
		Edges: &edges,
		// TODO: populate Nodes once per-node aggregate queries are implemented.
		Nodes: nil,
		Summary: gen.RuntimeTopologySummary{
			StartTime:   startTime,
			EndTime:     endTime,
			GeneratedAt: time.Now().UTC(),
		},
	}
	return runtimeTopologyOKResponse(response), nil
}

// edgeKey identifies a directed HTTP edge between two components. UIDs are the
// stable identity; names come from the same pod labels and are carried for the response.
type edgeKey struct {
	srcUID  string
	dstUID  string
	srcName string
	dstName string
}

// buildEdgeMetricMap converts Prometheus instant query results into a map from
// edge key to metric value. UIDs are required; names are included when present.
func buildEdgeMetricMap(
	series []prometheus.TimeSeries,
	componentUIDFilter string,
) map[edgeKey]float64 {
	m := make(map[edgeKey]float64, len(series))
	for _, ts := range series {
		srcUID := ts.Metric[prometheus.RuntimeTopologySrcComponentUIDLabel]
		dstUID := ts.Metric[prometheus.RuntimeTopologyDstComponentUIDLabel]
		if srcUID == "" || dstUID == "" {
			continue
		}
		if componentUIDFilter != "" && srcUID != componentUIDFilter && dstUID != componentUIDFilter {
			continue
		}
		srcName := ts.Metric[prometheus.RuntimeTopologySrcComponentNameLabel]
		dstName := ts.Metric[prometheus.RuntimeTopologyDstComponentNameLabel]
		points := prometheus.ConvertTimeSeriesToTimeValuePoints(ts)
		if len(points) == 0 || points[0].Value <= 0 {
			continue
		}
		m[edgeKey{srcUID, dstUID, srcName, dstName}] += points[0].Value
	}
	return m
}

// buildRuntimeTopologyEdges assembles RuntimeTopologyEdge values from per-metric
// maps. Request count drives which edges exist; other maps default to zero when absent.
func buildRuntimeTopologyEdges(
	namespace string,
	projectUID string,
	requestCountMap, errorCountMap, avgLatencyMap, p50LatencyMap, p90LatencyMap, p99LatencyMap map[edgeKey]float64,
) []gen.RuntimeTopologyEdge {
	edges := make([]gen.RuntimeTopologyEdge, 0, len(requestCountMap))
	for key, requestCount := range requestCountMap {
		if requestCount <= 0 {
			continue
		}

		ns := namespace
		proj := projectUID
		errorCount := errorCountMap[key]
		avgLatency := avgLatencyMap[key]
		p50Latency := p50LatencyMap[key]
		p90Latency := p90LatencyMap[key]
		p99Latency := p99LatencyMap[key]

		var source, target gen.RuntimeTopologyNodeRef
		_ = source.FromRuntimeTopologyNodeRefComponent(gen.RuntimeTopologyNodeRefComponent{
			Kind:         gen.RuntimeTopologyNodeRefComponentKindComponent,
			Component:    key.srcName,
			ComponentUid: key.srcUID,
			ProjectUid:   &proj,
			Namespace:    &ns,
		})
		_ = target.FromRuntimeTopologyNodeRefComponent(gen.RuntimeTopologyNodeRefComponent{
			Kind:         gen.RuntimeTopologyNodeRefComponentKindComponent,
			Component:    key.dstName,
			ComponentUid: key.dstUID,
			ProjectUid:   &proj,
			Namespace:    &ns,
		})
		edges = append(edges, gen.RuntimeTopologyEdge{
			Id:       fmt.Sprintf("%s->%s", key.srcUID, key.dstUID),
			Source:   source,
			Target:   target,
			Protocol: gen.RuntimeTopologyEdgeProtocolHttp,
			Metrics: &gen.RuntimeTopologyMetrics{
				RequestCount:             &requestCount,
				UnsuccessfulRequestCount: &errorCount,
				MeanLatency:              &avgLatency,
				LatencyP50:               &p50Latency,
				LatencyP90:               &p90Latency,
				LatencyP99:               &p99Latency,
			},
		})
	}
	return edges
}

// runtimeTopologyOKResponse wraps the 200 response so the strict-server
// codegen accepts the value. Unlike MetricsQueryResponse (which is a oneOf
// union), RuntimeTopologyResponse is a plain object — but we keep a thin
// helper for symmetry.
func runtimeTopologyOKResponse(r gen.RuntimeTopologyResponse) gen.QueryRuntimeTopology200JSONResponse {
	return gen.QueryRuntimeTopology200JSONResponse(r)
}

func badOrServerErrorRuntimeTopology400(detail string) gen.QueryRuntimeTopology400JSONResponse {
	return gen.QueryRuntimeTopology400JSONResponse{
		Title:  errorTitle(gen.BadRequest),
		Detail: strPtr(detail),
	}
}

func badOrServerErrorRuntimeTopology500(detail string) gen.QueryRuntimeTopology500JSONResponse {
	return gen.QueryRuntimeTopology500JSONResponse{
		Title:  errorTitle(gen.InternalServerError),
		Detail: strPtr(detail),
	}
}
