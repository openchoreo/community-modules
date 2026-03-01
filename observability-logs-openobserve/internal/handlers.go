// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-openobserve/internal/openobserve"
)

// LogsHandler implements the generated StrictServerInterface.
type LogsHandler struct {
	client *openobserve.Client
	logger *slog.Logger
}

func NewLogsHandler(client *openobserve.Client, logger *slog.Logger) *LogsHandler {
	return &LogsHandler{
		client: client,
		logger: logger,
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
	if err == nil && workflowScope.WorkflowRunName != nil && strings.TrimSpace(*workflowScope.WorkflowRunName) != "" {
		if strings.TrimSpace(workflowScope.Namespace) == "" {
			return gen.QueryLogs400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("searchScope with a valid namespace is required"),
			}, nil
		}

		params := toWorkflowLogsParams(request.Body, &workflowScope)
		result, err := h.client.GetWorkflowLogs(ctx, params)
		if err != nil {
			h.logger.Error("Failed to query workflow logs", slog.Any("error", err))
			return gen.QueryLogs500JSONResponse{
				Title:   ptr(gen.InternalServerError),
				Message: ptr(err.Error()),
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
		h.logger.Error("Failed to query logs", slog.Any("error", err))
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr(err.Error()),
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
		h.logger.Error("Failed to create alert", slog.Any("error", err))
		return gen.CreateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr(err.Error()),
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return gen.CreateAlertRule200JSONResponse{
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
		h.logger.Error("Failed to delete alert", slog.Any("error", err))
		return gen.DeleteAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr(err.Error()),
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
			Metadata:  &l.Metadata,
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
		uid := parseUUID(l.ComponentUID)
		entry.Metadata.ComponentUid = &uid
	}
	if l.ProjectUID != "" {
		uid := parseUUID(l.ProjectUID)
		entry.Metadata.ProjectUid = &uid
	}
	if l.EnvironmentUID != "" {
		uid := parseUUID(l.EnvironmentUID)
		entry.Metadata.EnvironmentUid = &uid
	}

	return entry
}

func parseUUID(s string) openapi_types.UUID {
	parsed, _ := uuid.Parse(s)
	return openapi_types.UUID(parsed)
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
