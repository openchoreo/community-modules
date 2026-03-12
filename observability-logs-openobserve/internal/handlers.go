// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/observer"
	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/openobserve"
)

// LogsHandler implements the generated StrictServerInterface.
type LogsHandler struct {
	client         *openobserve.Client
	observerClient *observer.Client
	logger         *slog.Logger
}

func NewLogsHandler(client *openobserve.Client, observerClient *observer.Client, logger *slog.Logger) *LogsHandler {
	return &LogsHandler{
		client:         client,
		observerClient: observerClient,
		logger:         logger,
	}
}

// Ensure LogsHandler implements the interface at compile time.
var _ gen.StrictServerInterface = (*LogsHandler)(nil)

// Health implements the health check endpoint.
func (h *LogsHandler) Health(ctx context.Context, _ gen.HealthRequestObject) (gen.HealthResponseObject, error) {
	status := "healthy"
	return gen.Health200JSONResponse{Status: &status}, nil
}

// QueryLogs implements POST /api/v1/logs/query.
func (h *LogsHandler) QueryLogs(ctx context.Context, request gen.QueryLogsRequestObject) (gen.QueryLogsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	// Try to interpret the search scope as a WorkflowSearchScope first
	// A WorkflowSearchScope is identified by having a workflowRunName field
	workflowScope, err := request.Body.SearchScope.AsWorkflowSearchScope()
	if err == nil && workflowScope.WorkflowRunName != nil {
		if strings.TrimSpace(workflowScope.Namespace) == "" {
			return gen.QueryLogs400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("searchScope with a valid namespace is required"),
			}, nil
		}

		params := toWorkflowLogsParams(request.Body, &workflowScope)
		result, err := h.client.GetWorkflowLogs(ctx, params)
		if err != nil {
			h.logger.Error("Failed to query workflow logs",
				slog.String("function", "QueryLogs"),
				slog.String("namespace", workflowScope.Namespace),
				slog.Any("error", err),
			)
			return gen.QueryLogs500JSONResponse{
				Title:   ptr(gen.InternalServerError),
				Message: ptr("internal server error"),
			}, nil
		}

		return gen.QueryLogs200JSONResponse(toWorkflowLogsQueryResponse(result)), nil
	}

	// Fall back to ComponentSearchScope
	scope, err := request.Body.SearchScope.AsComponentSearchScope()
	if err != nil || strings.TrimSpace(scope.Namespace) == "" {
		return gen.QueryLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("searchScope with a valid namespace is required"),
		}, nil
	}

	params := toComponentLogsParams(request.Body, &scope)

	result, err := h.client.GetComponentLogs(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query component logs",
			slog.String("function", "QueryLogs"),
			slog.String("namespace", scope.Namespace),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	return gen.QueryLogs200JSONResponse(toLogsQueryResponse(result)), nil
}

// CreateAlertRule implements POST /api/v1alpha1/alerts/rules.
func (h *LogsHandler) CreateAlertRule(ctx context.Context, request gen.CreateAlertRuleRequestObject) (gen.CreateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.CreateAlertRule400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	params := toLogAlertParams(request.Body)

	alertID, err := h.client.CreateAlert(ctx, params)
	if err != nil {
		h.logger.Error("Failed to create alert",
			slog.String("function", "CreateAlertRule"),
			slog.Any("alertName", params.Name),
			slog.Any("error", err),
		)
		return gen.CreateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return gen.CreateAlertRule201JSONResponse{
		Action:        ptr(gen.Created),
		Status:        ptr(gen.Synced),
		RuleLogicalId: params.Name,
		RuleBackendId: &alertID,
		LastSyncedAt:  &now,
	}, nil
}

// DeleteAlertRule implements DELETE /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	alertID, err := h.client.DeleteAlert(ctx, request.RuleName)
	if err != nil {
		h.logger.Error("Failed to delete alert",
			slog.String("function", "DeleteAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.DeleteAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return gen.DeleteAlertRule200JSONResponse{
		Action:        ptr(gen.Deleted),
		Status:        ptr(gen.Synced),
		RuleLogicalId: &request.RuleName,
		RuleBackendId: &alertID,
		LastSyncedAt:  &now,
	}, nil
}

// GetAlertRule implements GET /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	alert, err := h.client.GetAlert(ctx, request.RuleName)
	if err != nil {
		h.logger.Error("Failed to get alert",
			slog.String("function", "GetAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		if strings.Contains(err.Error(), "not found") {
			return gen.GetAlertRule404JSONResponse{
				Title:   ptr(gen.NotFound),
				Message: ptr("alert rule not found"),
			}, nil
		}
		return gen.GetAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	searchPattern := openobserve.ExtractSearchPattern(alert.SQL)
	operator := gen.AlertRuleResponseConditionOperator(openobserve.ReverseMapOperator(alert.Operator))
	threshold := float32(alert.Threshold)
	window := openobserve.ToDurationString(alert.Period, alert.FrequencyType)
	interval := openobserve.ToDurationString(alert.Frequency, alert.FrequencyType)

	metadata := &struct {
		ComponentUid   *openapi_types.UUID `json:"componentUid,omitempty"`
		EnvironmentUid *openapi_types.UUID `json:"environmentUid,omitempty"`
		Name           *string             `json:"name,omitempty"`
		Namespace      *string             `json:"namespace,omitempty"`
		ProjectUid     *openapi_types.UUID `json:"projectUid,omitempty"`
	}{
		Name:      &alert.Name,
		Namespace: strPtr(alert.Namespace),
	}
	if alert.ProjectUID != "" {
		if uid, ok := parseUUID(alert.ProjectUID); ok {
			metadata.ProjectUid = &uid
		}
	}
	if alert.EnvironmentUID != "" {
		if uid, ok := parseUUID(alert.EnvironmentUID); ok {
			metadata.EnvironmentUid = &uid
		}
	}
	if alert.ComponentUID != "" {
		if uid, ok := parseUUID(alert.ComponentUID); ok {
			metadata.ComponentUid = &uid
		}
	}

	response := gen.AlertRuleResponse{
		Metadata: metadata,
		Source: &struct {
			Metric *gen.AlertRuleResponseSourceMetric `json:"metric,omitempty"`
			Query  *string                            `json:"query,omitempty"`
		}{
			Metric: ptr(gen.AlertRuleResponseSourceMetric("log")),
			Query:  &searchPattern,
		},
		Condition: &struct {
			Enabled   *bool                                  `json:"enabled,omitempty"`
			Interval  *string                                `json:"interval,omitempty"`
			Operator  *gen.AlertRuleResponseConditionOperator `json:"operator,omitempty"`
			Threshold *float32                               `json:"threshold,omitempty"`
			Window    *string                                `json:"window,omitempty"`
		}{
			Enabled:   &alert.Enabled,
			Operator:  &operator,
			Threshold: &threshold,
			Window:    &window,
			Interval:  &interval,
		},
	}

	return gen.GetAlertRule200JSONResponse(response), nil
}

// UpdateAlertRule implements PUT /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) UpdateAlertRule(ctx context.Context, request gen.UpdateAlertRuleRequestObject) (gen.UpdateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.UpdateAlertRule400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	params := toLogAlertParams(request.Body)

	alertID, err := h.client.UpdateAlert(ctx, request.RuleName, params)
	if err != nil {
		h.logger.Error("Failed to update alert",
			slog.String("function", "UpdateAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		if strings.Contains(err.Error(), "not found") {
			return gen.UpdateAlertRule500JSONResponse{
				Title:   ptr(gen.InternalServerError),
				Message: ptr("alert rule not found"),
			}, nil
		}
		if strings.Contains(err.Error(), "invalid") {
			return gen.UpdateAlertRule400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr(err.Error()),
			}, nil
		}
		return gen.UpdateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return gen.UpdateAlertRule200JSONResponse{
		Action:        ptr(gen.Updated),
		Status:        ptr(gen.Synced),
		RuleLogicalId: &request.RuleName,
		RuleBackendId: &alertID,
		LastSyncedAt:  &now,
	}, nil
}

// HandleAlertWebhook implements POST /api/v1alpha1/alerts/webhook.
func (h *LogsHandler) HandleAlertWebhook(ctx context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	if request.Body == nil {
		h.logger.Warn("Alert webhook received with nil body")
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}
	body := *request.Body

	alertName, ruleName, alertCount, alertTimestamp, err := parseAlertWebhookBody(body)
	if err != nil {
		h.logger.Error("Failed to parse alert webhook body", slog.Any("error", err))
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}

	go func() {
		forwardCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Retrieve the alert details from OpenObserve to get the namespace. This is because the webhook body does not contain the namespace, but the observer's webhook API requires it.
		alertDetail, err := h.client.GetAlert(forwardCtx, alertName)
		if err != nil {
			h.logger.Error("Failed to get alert details from OpenObserve",
				slog.String("alertName", alertName),
				slog.Any("error", err),
			)
			return
		}

		if err := h.observerClient.ForwardAlert(forwardCtx, ruleName, alertDetail.Namespace, alertCount, alertTimestamp); err != nil {
			h.logger.Error("Failed to forward alert webhook to observer API",
				slog.Any("error", err),
			)
		}
	}()

	return gen.HandleAlertWebhook200JSONResponse{
		Message: ptr("alert webhook received successfully"),
		Status:  ptr(gen.Success),
	}, nil
}

// parseAlertWebhookBody extracts alert fields from the incoming OpenObserve webhook body.
// It returns the alertName, ruleName (same as alertName), alertCount, and alertTimestamp.
func parseAlertWebhookBody(body map[string]interface{}) (alertName string, ruleName string, alertCount float64, alertTimestamp time.Time, err error) {
	nameVal, ok := body["alertName"]
	if !ok {
		return "", "", 0, time.Time{}, fmt.Errorf("missing alertName in webhook body")
	}
	alertName, ok = nameVal.(string)
	if !ok {
		return "", "", 0, time.Time{}, fmt.Errorf("alertName is not a string")
	}
	ruleName = alertName

	if countVal, ok := body["alertCount"]; ok {
		switch v := countVal.(type) {
		case float64:
			alertCount = v
		case string:
			alertCount, err = strconv.ParseFloat(v, 64)
			if err != nil {
				return "", "", 0, time.Time{}, fmt.Errorf("failed to parse alertCount %q: %w", v, err)
			}
		}
	}

	if tsVal, ok := body["alertTriggerTimeMicroSeconds"]; ok {
		switch v := tsVal.(type) {
		case float64:
			alertTimestamp = time.UnixMicro(int64(v))
		case string:
			usec, parseErr := strconv.ParseInt(v, 10, 64)
			if parseErr != nil {
				return "", "", 0, time.Time{}, fmt.Errorf("failed to parse alertTriggerTimeMicroSeconds %q: %w", v, parseErr)
			}
			alertTimestamp = time.UnixMicro(usec)
		default:
			alertTimestamp = time.Now()
		}
	} else {
		alertTimestamp = time.Now()
	}

	return alertName, ruleName, alertCount, alertTimestamp, nil
}

// toWorkflowLogsParams converts the generated request to internal workflow query params.
func toWorkflowLogsParams(req *gen.LogsQueryRequest, scope *gen.WorkflowSearchScope) openobserve.WorkflowLogsParams {
	params := openobserve.WorkflowLogsParams{
		Namespace: scope.Namespace,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
	}
	if scope.WorkflowRunName != nil {
		params.WorkflowRunName = *scope.WorkflowRunName
	}
	if req.Limit != nil {
		params.Limit = *req.Limit
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
	}
	if req.SearchPhrase != nil {
		params.SearchPhrase = *req.SearchPhrase
	}
	if req.LogLevels != nil {
		levels := make([]string, len(*req.LogLevels))
		for i, l := range *req.LogLevels {
			levels[i] = string(l)
		}
		params.LogLevels = levels
	}
	return params
}

// toWorkflowLogsQueryResponse converts the internal workflow result to the generated response model.
func toWorkflowLogsQueryResponse(result *openobserve.WorkflowLogsResult) gen.LogsQueryResponse {
	entries := make([]gen.WorkflowLogEntry, 0, len(result.Logs))
	for _, l := range result.Logs {
		entry := gen.WorkflowLogEntry{
			Timestamp: &l.Timestamp,
			Log:       &l.Log,
		}
		entries = append(entries, entry)
	}

	resp := gen.LogsQueryResponse{
		Total:  &result.TotalCount,
		TookMs: &result.Took,
	}

	logs := gen.LogsQueryResponse_Logs{}
	_ = logs.FromLogsQueryResponseLogs1(entries)
	resp.Logs = &logs

	return resp
}

// toComponentLogsParams converts the generated request to internal query params.
func toComponentLogsParams(req *gen.LogsQueryRequest, scope *gen.ComponentSearchScope) openobserve.ComponentLogsParams {
	params := openobserve.ComponentLogsParams{
		Namespace: scope.Namespace,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
	}
	if scope.ProjectUid != nil {
		params.ProjectID = *scope.ProjectUid
	}
	if scope.EnvironmentUid != nil {
		params.EnvironmentID = *scope.EnvironmentUid
	}
	if scope.ComponentUid != nil {
		params.ComponentIDs = []string{*scope.ComponentUid}
	}
	if req.Limit != nil {
		params.Limit = *req.Limit
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
	}
	if req.SearchPhrase != nil {
		params.SearchPhrase = *req.SearchPhrase
	}
	if req.LogLevels != nil {
		levels := make([]string, len(*req.LogLevels))
		for i, l := range *req.LogLevels {
			levels[i] = string(l)
		}
		params.LogLevels = levels
	}
	return params
}

// toLogAlertParams converts the generated AlertRuleRequest to internal params.
func toLogAlertParams(req *gen.AlertRuleRequest) openobserve.LogAlertParams {
	params := openobserve.LogAlertParams{}
	if req.Metadata != nil {
		params.Name = req.Metadata.Name
		if req.Metadata.Namespace != nil {
			params.Namespace = *req.Metadata.Namespace
		}
		if req.Metadata.ProjectUid != nil {
			params.ProjectUID = req.Metadata.ProjectUid.String()
		}
		if req.Metadata.EnvironmentUid != nil {
			params.EnvironmentUID = req.Metadata.EnvironmentUid.String()
		}
		if req.Metadata.ComponentUid != nil {
			params.ComponentUID = req.Metadata.ComponentUid.String()
		}
	}
	if req.Source != nil {
		if req.Source.Query != nil {
			params.SearchPattern = *req.Source.Query
		}
	}
	if req.Condition != nil {
		if req.Condition.Enabled != nil {
			params.Enabled = req.Condition.Enabled
		}
		if req.Condition.Operator != nil {
			params.Operator = string(*req.Condition.Operator)
		}
		if req.Condition.Threshold != nil {
			params.ThresholdValue = *req.Condition.Threshold
		}
		if req.Condition.Window != nil {
			params.Window = *req.Condition.Window
		}
		if req.Condition.Interval != nil {
			params.Interval = *req.Condition.Interval
		}
	}
	return params
}

// toLogsQueryResponse converts the internal result to the generated response model.
func toLogsQueryResponse(result *openobserve.ComponentLogsResult) gen.LogsQueryResponse {
	entries := make([]gen.ComponentLogEntry, 0, len(result.Logs))
	for _, l := range result.Logs {
		entry := toComponentLogEntry(&l)
		entries = append(entries, entry)
	}

	resp := gen.LogsQueryResponse{
		Total:  &result.TotalCount,
		TookMs: &result.Took,
	}

	logs := gen.LogsQueryResponse_Logs{}
	_ = logs.FromLogsQueryResponseLogs0(entries)
	resp.Logs = &logs

	return resp
}

func toComponentLogEntry(l *openobserve.ComponentLogsEntry) gen.ComponentLogEntry {
	entry := gen.ComponentLogEntry{
		Timestamp: &l.Timestamp,
		Log:       &l.Log,
		Level:     &l.LogLevel,
		Metadata: &struct {
			ComponentName   *string            `json:"componentName,omitempty"`
			ComponentUid    *openapi_types.UUID `json:"componentUid,omitempty"`
			ContainerName   *string            `json:"containerName,omitempty"`
			EnvironmentName *string            `json:"environmentName,omitempty"`
			EnvironmentUid  *openapi_types.UUID `json:"environmentUid,omitempty"`
			NamespaceName   *string            `json:"namespaceName,omitempty"`
			PodName         *string            `json:"podName,omitempty"`
			PodNamespace    *string            `json:"podNamespace,omitempty"`
			ProjectName     *string            `json:"projectName,omitempty"`
			ProjectUid      *openapi_types.UUID `json:"projectUid,omitempty"`
		}{
			NamespaceName:   strPtr(l.Namespace),
			ContainerName:   strPtr(l.ContainerName),
			PodName:         strPtr(l.PodName),
			PodNamespace:    strPtr(l.PodNamespace),
			ComponentName:   strPtr(l.ComponentName),
			ProjectName:     strPtr(l.ProjectName),
			EnvironmentName: strPtr(l.EnvironmentName),
		},
	}

	if l.ComponentUID != "" {
		if uid, ok := parseUUID(l.ComponentUID); ok {
			entry.Metadata.ComponentUid = &uid
		}
	}
	if l.ProjectUID != "" {
		if uid, ok := parseUUID(l.ProjectUID); ok {
			entry.Metadata.ProjectUid = &uid
		}
	}
	if l.EnvironmentUID != "" {
		if uid, ok := parseUUID(l.EnvironmentUID); ok {
			entry.Metadata.EnvironmentUid = &uid
		}
	}

	return entry
}

func parseUUID(s string) (openapi_types.UUID, bool) {
	parsed, err := uuid.Parse(s)
	if err != nil {
		return openapi_types.UUID{}, false
	}
	return openapi_types.UUID(parsed), true
}

func ptr[T any](v T) *T {
	return &v
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
