// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-dynatrace/internal/dynatrace"
)

// LogsBackend is the part of the Dynatrace client the handlers use, so handlers can be
// tested against a fake.
type LogsBackend interface {
	GetComponentLogs(ctx context.Context, p dynatrace.ComponentLogsParams) (*dynatrace.ComponentLogsResult, error)
	GetWorkflowLogs(ctx context.Context, p dynatrace.WorkflowLogsParams) (*dynatrace.WorkflowLogsResult, error)
	GetEvents(ctx context.Context, p dynatrace.EventsQueryParams) (*dynatrace.EventsResult, error)
	GetPlatformLogs(ctx context.Context, p dynatrace.PlatformLogsParams) (*dynatrace.PlatformLogsResult, error)
	GetPlatformLogFilterValues(ctx context.Context, p dynatrace.PlatformLogsParams, valueField, valueSearch string, maxValues int) (*dynatrace.FilterValues, error)
	GetAuditLogs(ctx context.Context, p dynatrace.AuditLogsParams, timelineInterval string) (*dynatrace.AuditLogsResult, error)
	GetAuditLogFilterValues(ctx context.Context, p dynatrace.AuditLogsParams, filter, valueSearch string, maxValues int) (*dynatrace.FilterValues, error)
}

// LogsHandler implements the generated StrictServerInterface.
type LogsHandler struct {
	backend LogsBackend
	logger  *slog.Logger
}

// NewLogsHandler returns a handler serving from the given backend.
func NewLogsHandler(backend LogsBackend, logger *slog.Logger) *LogsHandler {
	return &LogsHandler{backend: backend, logger: logger}
}

// Ensure LogsHandler implements the interface at compile time.
var _ gen.StrictServerInterface = (*LogsHandler)(nil)

// Health implements the health check endpoint.
func (h *LogsHandler) Health(_ context.Context, _ gen.HealthRequestObject) (gen.HealthResponseObject, error) {
	status := "healthy"
	return gen.Health200JSONResponse{Status: &status}, nil
}

// QueryLogs implements POST /api/v1/logs/query.
func (h *LogsHandler) QueryLogs(ctx context.Context, request gen.QueryLogsRequestObject) (gen.QueryLogsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryLogs400JSONResponse{Title: ptr(gen.BadRequest), Message: ptr("request body is required")}, nil
	}
	body := request.Body
	if !body.EndTime.After(body.StartTime) {
		return gen.QueryLogs400JSONResponse{Title: ptr(gen.BadRequest), Message: ptr("endTime must be after startTime")}, nil
	}

	// A WorkflowSearchScope is identified by having a workflowRunName field.
	workflowScope, err := body.SearchScope.AsWorkflowSearchScope()
	if err == nil && workflowScope.WorkflowRunName != nil {
		if strings.TrimSpace(workflowScope.Namespace) == "" || strings.TrimSpace(*workflowScope.WorkflowRunName) == "" {
			return gen.QueryLogs400JSONResponse{Title: ptr(gen.BadRequest), Message: ptr("searchScope with a valid namespace and workflowRunName is required")}, nil
		}

		params := dynatrace.WorkflowLogsParams{
			Namespace:       workflowScope.Namespace,
			WorkflowRunName: *workflowScope.WorkflowRunName,
			StartTime:       body.StartTime,
			EndTime:         body.EndTime,
			SearchPhrase:    deref(body.SearchPhrase),
			LogLevels:       logLevels(body.LogLevels),
			Limit:           deref(body.Limit),
			SortOrder:       string(deref(body.SortOrder)),
		}
		result, err := h.backend.GetWorkflowLogs(ctx, params)
		if err != nil {
			h.logger.Error("Failed to query workflow logs",
				slog.String("namespace", workflowScope.Namespace), slog.Any("error", err))
			return gen.QueryLogs500JSONResponse{Title: ptr(gen.InternalServerError), Message: ptr("internal server error")}, nil
		}
		return gen.QueryLogs200JSONResponse(toWorkflowLogsResponse(result)), nil
	}

	scope, err := body.SearchScope.AsComponentSearchScope()
	if err != nil || strings.TrimSpace(scope.Namespace) == "" {
		return gen.QueryLogs400JSONResponse{Title: ptr(gen.BadRequest), Message: ptr("searchScope with a valid namespace is required")}, nil
	}

	params := dynatrace.ComponentLogsParams{
		Namespace:     scope.Namespace,
		ProjectID:     deref(scope.ProjectUid),
		EnvironmentID: deref(scope.EnvironmentUid),
		ComponentID:   deref(scope.ComponentUid),
		StartTime:     body.StartTime,
		EndTime:       body.EndTime,
		SearchPhrase:  deref(body.SearchPhrase),
		LogLevels:     logLevels(body.LogLevels),
		Limit:         deref(body.Limit),
		SortOrder:     string(deref(body.SortOrder)),
	}
	result, err := h.backend.GetComponentLogs(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query component logs",
			slog.String("namespace", scope.Namespace), slog.Any("error", err))
		return gen.QueryLogs500JSONResponse{Title: ptr(gen.InternalServerError), Message: ptr("internal server error")}, nil
	}
	return gen.QueryLogs200JSONResponse(toComponentLogsResponse(result)), nil
}

// QueryEvents implements POST /api/v1/events/query.
func (h *LogsHandler) QueryEvents(ctx context.Context, request gen.QueryEventsRequestObject) (gen.QueryEventsResponseObject, error) {
	if request.Body == nil {
		return gen.QueryEvents400JSONResponse{Title: ptr(gen.BadRequest), Message: ptr("request body is required")}, nil
	}
	body := request.Body
	if !body.EndTime.After(body.StartTime) {
		return gen.QueryEvents400JSONResponse{Title: ptr(gen.BadRequest), Message: ptr("endTime must be after startTime")}, nil
	}

	params := dynatrace.EventsQueryParams{
		Reasons:   deref(body.Reasons),
		StartTime: body.StartTime,
		EndTime:   body.EndTime,
		Limit:     deref(body.Limit),
		SortOrder: string(deref(body.SortOrder)),
	}

	if body.SearchScope == nil {
		// Unscoped sweeps are legal only when narrowed by reasons; a missing scope on its
		// own must never widen to every namespace.
		if len(params.Reasons) == 0 {
			return gen.QueryEvents400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("searchScope is required unless reasons is set for an unscoped sweep"),
			}, nil
		}
	} else if workflowScope, err := body.SearchScope.AsWorkflowSearchScope(); err == nil && workflowScope.WorkflowRunName != nil {
		if strings.TrimSpace(workflowScope.Namespace) == "" || strings.TrimSpace(*workflowScope.WorkflowRunName) == "" {
			return gen.QueryEvents400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("searchScope with a valid namespace and workflowRunName is required"),
			}, nil
		}
		params.Workflow = &dynatrace.WorkflowEventsScope{
			Namespace:       workflowScope.Namespace,
			WorkflowRunName: *workflowScope.WorkflowRunName,
			TaskName:        deref(workflowScope.TaskName),
		}
	} else {
		scope, err := body.SearchScope.AsComponentSearchScope()
		if err != nil || strings.TrimSpace(scope.Namespace) == "" {
			return gen.QueryEvents400JSONResponse{Title: ptr(gen.BadRequest), Message: ptr("searchScope with a valid namespace is required")}, nil
		}
		params.Component = &dynatrace.ComponentEventsScope{
			Namespace:     scope.Namespace,
			ProjectID:     deref(scope.ProjectUid),
			ComponentID:   deref(scope.ComponentUid),
			EnvironmentID: deref(scope.EnvironmentUid),
		}
	}

	result, err := h.backend.GetEvents(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query events", slog.Any("error", err))
		return gen.QueryEvents500JSONResponse{Title: ptr(gen.InternalServerError), Message: ptr("internal server error")}, nil
	}
	return gen.QueryEvents200JSONResponse(toEventsResponse(result)), nil
}

// Alerting is not implemented by this adapter. Dynatrace alerting is configured with
// anomaly detectors and workflows in the tenant rather than per-rule over this API, so
// the alert endpoints answer 501 instead of pretending to have synced a rule.
const alertingNotImplemented = "alert rules are not supported by the Dynatrace logs adapter; configure alerting with Dynatrace anomaly detectors"

// notImplemented writes a 501 for operations whose generated response set has no 501.
type notImplemented struct{}

func (notImplemented) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	return json.NewEncoder(w).Encode(gen.ErrorResponse{
		Title:   ptr(gen.NotImplemented),
		Message: ptr(alertingNotImplemented),
	})
}

func (n notImplemented) VisitCreateAlertRuleResponse(w http.ResponseWriter) error { return n.write(w) }
func (n notImplemented) VisitGetAlertRuleResponse(w http.ResponseWriter) error    { return n.write(w) }
func (n notImplemented) VisitUpdateAlertRuleResponse(w http.ResponseWriter) error { return n.write(w) }
func (n notImplemented) VisitDeleteAlertRuleResponse(w http.ResponseWriter) error { return n.write(w) }
func (n notImplemented) VisitHandleAlertWebhookResponse(w http.ResponseWriter) error {
	return n.write(w)
}

// CreateAlertRule implements POST /api/v1alpha1/alerts/rules.
func (h *LogsHandler) CreateAlertRule(context.Context, gen.CreateAlertRuleRequestObject) (gen.CreateAlertRuleResponseObject, error) {
	return notImplemented{}, nil
}

// GetAlertRule implements GET /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) GetAlertRule(context.Context, gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	return notImplemented{}, nil
}

// UpdateAlertRule implements PUT /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) UpdateAlertRule(context.Context, gen.UpdateAlertRuleRequestObject) (gen.UpdateAlertRuleResponseObject, error) {
	return notImplemented{}, nil
}

// DeleteAlertRule implements DELETE /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) DeleteAlertRule(context.Context, gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	return notImplemented{}, nil
}

// HandleAlertWebhook implements POST /api/v1alpha1/alerts/webhook.
func (h *LogsHandler) HandleAlertWebhook(context.Context, gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	return notImplemented{}, nil
}

func toComponentLogsResponse(result *dynatrace.ComponentLogsResult) gen.LogsQueryResponse {
	entries := make([]gen.ComponentLogEntry, 0, len(result.Logs))
	for i := range result.Logs {
		l := &result.Logs[i]
		entry := gen.ComponentLogEntry{
			Timestamp: &l.Timestamp,
			Log:       &l.Log,
			Level:     strPtr(l.LogLevel),
			Metadata: &struct {
				ComponentName   *string             `json:"componentName,omitempty"`
				ComponentUid    *openapi_types.UUID `json:"componentUid,omitempty"`
				ContainerName   *string             `json:"containerName,omitempty"`
				EnvironmentName *string             `json:"environmentName,omitempty"`
				EnvironmentUid  *openapi_types.UUID `json:"environmentUid,omitempty"`
				NamespaceName   *string             `json:"namespaceName,omitempty"`
				PodName         *string             `json:"podName,omitempty"`
				PodNamespace    *string             `json:"podNamespace,omitempty"`
				ProjectName     *string             `json:"projectName,omitempty"`
				ProjectUid      *openapi_types.UUID `json:"projectUid,omitempty"`
			}{
				ComponentName:   strPtr(l.ComponentName),
				ComponentUid:    uuidPtr(l.ComponentUID),
				ContainerName:   strPtr(l.ContainerName),
				EnvironmentName: strPtr(l.EnvironmentName),
				EnvironmentUid:  uuidPtr(l.EnvironmentUID),
				NamespaceName:   strPtr(l.Namespace),
				PodName:         strPtr(l.PodName),
				PodNamespace:    strPtr(l.PodNamespace),
				ProjectName:     strPtr(l.ProjectName),
				ProjectUid:      uuidPtr(l.ProjectUID),
			},
		}
		entries = append(entries, entry)
	}

	logs := gen.LogsQueryResponse_Logs{}
	_ = logs.FromLogsQueryResponseLogs0(entries)
	return gen.LogsQueryResponse{Logs: &logs, Total: &result.TotalCount, TookMs: &result.Took}
}

func toWorkflowLogsResponse(result *dynatrace.WorkflowLogsResult) gen.LogsQueryResponse {
	entries := make([]gen.WorkflowLogEntry, 0, len(result.Logs))
	for i := range result.Logs {
		l := &result.Logs[i]
		entries = append(entries, gen.WorkflowLogEntry{Timestamp: &l.Timestamp, Log: &l.Log})
	}

	logs := gen.LogsQueryResponse_Logs{}
	_ = logs.FromLogsQueryResponseLogs1(entries)
	return gen.LogsQueryResponse{Logs: &logs, Total: &result.TotalCount, TookMs: &result.Took}
}

func toEventsResponse(result *dynatrace.EventsResult) gen.EventsQueryResponse {
	entries := make([]gen.EventEntry, 0, len(result.Events))
	for i := range result.Events {
		e := &result.Events[i]
		entries = append(entries, gen.EventEntry{
			Timestamp: &e.Timestamp,
			Message:   strPtr(e.Message),
			Type:      strPtr(e.Type),
			Reason:    strPtr(e.Reason),
			Metadata: &struct {
				ComponentName   *string             `json:"componentName,omitempty"`
				ComponentUid    *openapi_types.UUID `json:"componentUid,omitempty"`
				EnvironmentName *string             `json:"environmentName,omitempty"`
				EnvironmentUid  *openapi_types.UUID `json:"environmentUid,omitempty"`
				NamespaceName   *string             `json:"namespaceName,omitempty"`
				ObjectKind      *string             `json:"objectKind,omitempty"`
				ObjectName      *string             `json:"objectName,omitempty"`
				ObjectNamespace *string             `json:"objectNamespace,omitempty"`
				ProjectName     *string             `json:"projectName,omitempty"`
				ProjectUid      *openapi_types.UUID `json:"projectUid,omitempty"`
			}{
				ComponentName:   strPtr(e.ComponentName),
				ComponentUid:    uuidPtr(e.ComponentID),
				EnvironmentName: strPtr(e.EnvironmentName),
				EnvironmentUid:  uuidPtr(e.EnvironmentID),
				NamespaceName:   strPtr(e.NamespaceName),
				ObjectKind:      strPtr(e.ObjectKind),
				ObjectName:      strPtr(e.ObjectName),
				ObjectNamespace: strPtr(e.ObjectNamespace),
				ProjectName:     strPtr(e.ProjectName),
				ProjectUid:      uuidPtr(e.ProjectID),
			},
		})
	}
	return gen.EventsQueryResponse{Events: &entries, Total: result.TotalCount, TookMs: &result.Took}
}

// logLevels converts the generated level enum slice to strings.
func logLevels[T ~string](levels *[]T) []string {
	if levels == nil {
		return nil
	}
	out := make([]string, len(*levels))
	for i, l := range *levels {
		out[i] = string(l)
	}
	return out
}

func uuidPtr(s string) *openapi_types.UUID {
	if s == "" {
		return nil
	}
	parsed, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	u := openapi_types.UUID(parsed)
	return &u
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
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
