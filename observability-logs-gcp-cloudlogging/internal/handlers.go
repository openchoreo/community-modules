// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/cloudlogging"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/cloudmonitoring"
	"github.com/openchoreo/community-modules/observability-logs-gcp-cloudlogging/internal/observer"
)

const (
	errCodePrefix         = "OBS-V1-L-GCP"
	errCodeBadRequest     = errCodePrefix + "-400"
	errCodeNotFound       = errCodePrefix + "-404"
	errCodeConflict       = errCodePrefix + "-409"
	errCodeInternal       = errCodePrefix + "-500"
	errCodeNotImplemented = errCodePrefix + "-501"
)

// LogsHandler implements the generated StrictServerInterface, backed by the
// Cloud Logging client (log queries), the Cloud Monitoring client (alert rule
// CRUD), and the Observer client (webhook forwarding).
type LogsHandler struct {
	client         *cloudlogging.Client
	alertClient    *cloudmonitoring.Client
	observerClient *observer.Client
	logger         *slog.Logger
}

// NewLogsHandler constructs a handler with all dependencies wired in.
func NewLogsHandler(
	client *cloudlogging.Client,
	alertClient *cloudmonitoring.Client,
	observerClient *observer.Client,
	logger *slog.Logger,
) *LogsHandler {
	return &LogsHandler{
		client:         client,
		alertClient:    alertClient,
		observerClient: observerClient,
		logger:         logger,
	}
}

// Compile-time check that LogsHandler satisfies the generated interface.
var _ gen.StrictServerInterface = (*LogsHandler)(nil)

// Health returns a static healthy response; reachability alone means the
// adapter is up (Cloud Logging is verified once at boot, not per-request).
func (h *LogsHandler) Health(_ context.Context, _ gen.HealthRequestObject) (gen.HealthResponseObject, error) {
	status := "healthy"
	return gen.Health200JSONResponse{Status: &status}, nil
}

// QueryLogs handles POST /api/v1/logs/query. Discriminates between
// component-scope and workflow-scope queries based on the searchScope union
// and dispatches accordingly.
func (h *LogsHandler) QueryLogs(ctx context.Context, request gen.QueryLogsRequestObject) (gen.QueryLogsResponseObject, error) {
	if request.Body == nil {
		return badRequest("request body is required"), nil
	}
	if request.Body.EndTime.Before(request.Body.StartTime) {
		return badRequest("endTime must be greater than or equal to startTime"), nil
	}

	limit := 100
	if request.Body.Limit != nil {
		limit = *request.Body.Limit
	}
	if limit < 1 || limit > 1000 {
		return badRequest("limit must be between 1 and 1000"), nil
	}

	sortOrder := cloudlogging.SortDesc
	if request.Body.SortOrder != nil {
		switch *request.Body.SortOrder {
		case gen.LogsQueryRequestSortOrder("asc"):
			sortOrder = cloudlogging.SortAsc
		case gen.LogsQueryRequestSortOrder("desc"):
			sortOrder = cloudlogging.SortDesc
		}
	}

	logLevels := []string{}
	if request.Body.LogLevels != nil {
		for _, l := range *request.Body.LogLevels {
			logLevels = append(logLevels, string(l))
		}
	}

	searchPhrase := ""
	if request.Body.SearchPhrase != nil {
		searchPhrase = *request.Body.SearchPhrase
	}

	// Workflow scope discriminator: presence of workflowRunName.
	if workflow, err := request.Body.SearchScope.AsWorkflowSearchScope(); err == nil && workflow.WorkflowRunName != nil {
		if strings.TrimSpace(workflow.Namespace) == "" {
			return badRequest("searchScope.namespace is required"), nil
		}
		params := cloudlogging.WorkflowLogsParams{
			Namespace:       workflow.Namespace,
			WorkflowRunName: *workflow.WorkflowRunName,
			StartTime:       request.Body.StartTime,
			EndTime:         request.Body.EndTime,
			Limit:           limit,
			SortOrder:       sortOrder,
			SearchPhrase:    searchPhrase,
			LogLevels:       logLevels,
		}
		result, err := h.client.GetWorkflowLogs(ctx, params)
		if err != nil {
			h.logger.Error("workflow log query failed",
				slog.String("namespace", workflow.Namespace),
				slog.Any("error", err),
			)
			return internalError("failed to query workflow logs"), nil
		}
		return gen.QueryLogs200JSONResponse(buildWorkflowResponse(result)), nil
	}

	scope, err := request.Body.SearchScope.AsComponentSearchScope()
	if err != nil {
		return badRequest("searchScope is required"), nil
	}
	if strings.TrimSpace(scope.Namespace) == "" {
		return badRequest("searchScope.namespace is required"), nil
	}

	params := cloudlogging.ComponentLogsParams{
		Namespace:      scope.Namespace,
		ComponentUID:   derefString(scope.ComponentUid),
		ProjectUID:     derefString(scope.ProjectUid),
		EnvironmentUID: derefString(scope.EnvironmentUid),
		StartTime:      request.Body.StartTime,
		EndTime:        request.Body.EndTime,
		Limit:          limit,
		SortOrder:      sortOrder,
		SearchPhrase:   searchPhrase,
		LogLevels:      logLevels,
	}
	result, err := h.client.GetComponentLogs(ctx, params)
	if err != nil {
		h.logger.Error("component log query failed",
			slog.String("namespace", scope.Namespace),
			slog.Any("error", err),
		)
		return internalError("failed to query component logs"), nil
	}
	return gen.QueryLogs200JSONResponse(buildComponentResponse(result)), nil
}

// QueryEvents is not implemented: this adapter is a Cloud Logging *logs*
// backend and does not serve Kubernetes events. The generated contract offers
// no 501 response for this endpoint, so it returns a 500 carrying the
// not-implemented error code.
func (h *LogsHandler) QueryEvents(_ context.Context, _ gen.QueryEventsRequestObject) (gen.QueryEventsResponseObject, error) {
	return gen.QueryEvents500JSONResponse(makeError(gen.InternalServerError, errCodeNotImplemented,
		"events query is not implemented by the GCP Cloud Logging adapter")), nil
}

// --- Alert endpoints ---

// CreateAlertRule provisions a log-based metric + Cloud Monitoring alert policy.
func (h *LogsHandler) CreateAlertRule(ctx context.Context, request gen.CreateAlertRuleRequestObject) (gen.CreateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.CreateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}
	in, err := ruleInputFromRequest(*request.Body)
	if err != nil {
		return gen.CreateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
	}
	res, err := h.alertClient.CreateRule(ctx, in)
	if err != nil {
		if errors.Is(err, cloudmonitoring.ErrAlreadyExists) {
			return gen.CreateAlertRule409JSONResponse(makeError(gen.Conflict, errCodeConflict, "alert rule already exists")), nil
		}
		h.logger.Error("create alert rule failed",
			slog.String("ruleName", in.RuleName),
			slog.String("namespace", in.Namespace),
			slog.Any("error", err),
		)
		return gen.CreateAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to create alert rule")), nil
	}
	return gen.CreateAlertRule201JSONResponse(syncResponse(res, gen.Created, gen.Synced)), nil
}

// GetAlertRule returns the rule matching the Observer-supplied ruleName.
func (h *LogsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	if request.RuleName == "" {
		return gen.GetAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "ruleName is required")), nil
	}
	_, namespace, err := h.alertClient.FindRuleByName(ctx, request.RuleName)
	if err != nil {
		if errors.Is(err, cloudmonitoring.ErrNotFound) {
			return gen.GetAlertRule404JSONResponse(makeError(gen.NotFound, errCodeNotFound, "alert rule not found")), nil
		}
		h.logger.Error("get alert rule failed", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.GetAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to get alert rule")), nil
	}
	resp := gen.AlertRuleResponse{}
	metadata := &struct {
		ComponentUid   *openapi_types.UUID `json:"componentUid,omitempty"`
		EnvironmentUid *openapi_types.UUID `json:"environmentUid,omitempty"`
		Name           *string             `json:"name,omitempty"`
		Namespace      *string             `json:"namespace,omitempty"`
		ProjectUid     *openapi_types.UUID `json:"projectUid,omitempty"`
	}{Name: &request.RuleName}
	if namespace != "" {
		metadata.Namespace = &namespace
	}
	resp.Metadata = metadata
	return gen.GetAlertRule200JSONResponse(resp), nil
}

// UpdateAlertRule is idempotent — CreateOrUpdate.
func (h *LogsHandler) UpdateAlertRule(ctx context.Context, request gen.UpdateAlertRuleRequestObject) (gen.UpdateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.UpdateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}
	in, err := ruleInputFromRequest(gen.AlertRuleRequest(*request.Body))
	if err != nil {
		return gen.UpdateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
	}
	res, err := h.alertClient.UpdateRule(ctx, in)
	if err != nil {
		if errors.Is(err, cloudmonitoring.ErrNotFound) {
			return gen.UpdateAlertRule404JSONResponse(makeError(gen.NotFound, errCodeNotFound, "alert rule not found")), nil
		}
		h.logger.Error("update alert rule failed",
			slog.String("ruleName", in.RuleName),
			slog.String("namespace", in.Namespace),
			slog.Any("error", err),
		)
		return gen.UpdateAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to update alert rule")), nil
	}
	return gen.UpdateAlertRule200JSONResponse(syncResponse(res, gen.Updated, gen.Synced)), nil
}

// DeleteAlertRule removes the alert policy and log-based metric for the rule.
func (h *LogsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	if request.RuleName == "" {
		return gen.DeleteAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "ruleName is required")), nil
	}
	res, namespace, err := h.alertClient.FindRuleByName(ctx, request.RuleName)
	if err != nil {
		if errors.Is(err, cloudmonitoring.ErrNotFound) {
			return gen.DeleteAlertRule404JSONResponse(makeError(gen.NotFound, errCodeNotFound, "alert rule not found")), nil
		}
		h.logger.Error("find alert rule failed", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.DeleteAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to find alert rule")), nil
	}
	if err := h.alertClient.DeleteRule(ctx, namespace, request.RuleName); err != nil {
		if errors.Is(err, cloudmonitoring.ErrNotFound) {
			return gen.DeleteAlertRule404JSONResponse(makeError(gen.NotFound, errCodeNotFound, "alert rule not found")), nil
		}
		h.logger.Error("delete alert rule failed", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.DeleteAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to delete alert rule")), nil
	}
	res.LastSynced = cloudmonitoring.NowRFC3339()
	return gen.DeleteAlertRule200JSONResponse(syncResponse(res, gen.Deleted, gen.Synced)), nil
}

// HandleAlertWebhook accepts a Cloud Monitoring incident notification,
// recovers the OpenChoreo identity, and forwards a normalized alert to the
// Observer.
func (h *LogsHandler) HandleAlertWebhook(ctx context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	if request.Body == nil {
		return gen.HandleAlertWebhook400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}
	raw, err := json.Marshal(request.Body)
	if err != nil {
		return gen.HandleAlertWebhook400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "failed to re-encode webhook body")), nil
	}
	details, err := cloudmonitoring.ParseWebhook(raw)
	if err != nil {
		h.logger.Warn("webhook parse failed", slog.Any("error", err))
		return gen.HandleAlertWebhook400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
	}
	if err := h.observerClient.ForwardAlert(ctx, details.RuleName, details.RuleNamespace, details.AlertValue, details.AlertTimestamp); err != nil {
		h.logger.Error("forward to observer failed",
			slog.String("ruleName", details.RuleName),
			slog.String("ruleNamespace", details.RuleNamespace),
			slog.Any("error", err),
		)
		return gen.HandleAlertWebhook500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to forward alert to observer")), nil
	}
	status := gen.Success
	msg := "alert forwarded to observer"
	return gen.HandleAlertWebhook200JSONResponse(gen.AlertWebhookResponse{Status: &status, Message: &msg}), nil
}

func ruleInputFromRequest(req gen.AlertRuleRequest) (cloudmonitoring.RuleInput, error) {
	in := cloudmonitoring.RuleInput{
		Namespace:      req.Metadata.Namespace,
		RuleName:       req.Metadata.Name,
		ComponentUID:   req.Metadata.ComponentUid.String(),
		ProjectUID:     req.Metadata.ProjectUid.String(),
		EnvironmentUID: req.Metadata.EnvironmentUid.String(),
		Query:          req.Source.Query,
		Operator:       string(req.Condition.Operator),
		Threshold:      float64(req.Condition.Threshold),
		Window:         req.Condition.Window,
		Enabled:        req.Condition.Enabled,
	}
	if strings.TrimSpace(in.RuleName) == "" {
		return in, errors.New("metadata.name is required")
	}
	if strings.TrimSpace(in.Namespace) == "" {
		return in, errors.New("metadata.namespace is required")
	}
	if strings.TrimSpace(in.Operator) == "" {
		return in, errors.New("condition.operator is required")
	}
	if strings.TrimSpace(in.Query) == "" {
		return in, errors.New("source.query is required")
	}
	return in, nil
}

func syncResponse(r *cloudmonitoring.RuleResult, action gen.AlertingRuleSyncResponseAction, status gen.AlertingRuleSyncResponseStatus) gen.AlertingRuleSyncResponse {
	backendID := r.BackendID
	logicalID := r.LogicalID
	lastSynced := r.LastSynced
	return gen.AlertingRuleSyncResponse{
		Action:        &action,
		LastSyncedAt:  &lastSynced,
		RuleBackendId: &backendID,
		RuleLogicalId: &logicalID,
		Status:        &status,
	}
}

func makeError(title gen.ErrorResponseTitle, code, message string) gen.ErrorResponse {
	return gen.ErrorResponse{
		Title:     &title,
		ErrorCode: &code,
		Message:   &message,
	}
}

// --- response builders & helpers ---

func badRequest(message string) gen.QueryLogs400JSONResponse {
	t := gen.BadRequest
	c := errCodeBadRequest
	return gen.QueryLogs400JSONResponse{
		Title:     &t,
		ErrorCode: &c,
		Message:   &message,
	}
}

func internalError(message string) gen.QueryLogs500JSONResponse {
	t := gen.InternalServerError
	c := errCodeInternal
	return gen.QueryLogs500JSONResponse{
		Title:     &t,
		ErrorCode: &c,
		Message:   &message,
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func buildComponentResponse(result *cloudlogging.ComponentLogsResult) gen.LogsQueryResponse {
	entries := make([]gen.ComponentLogEntry, 0, len(result.Logs))
	for i := range result.Logs {
		entries = append(entries, mapComponentEntry(&result.Logs[i]))
	}

	logs := gen.LogsQueryResponse_Logs{}
	_ = logs.FromLogsQueryResponseLogs0(entries)

	total := capTotal(result.TotalCount)
	took := result.TookMs
	return gen.LogsQueryResponse{
		Logs:   &logs,
		Total:  &total,
		TookMs: &took,
	}
}

func buildWorkflowResponse(result *cloudlogging.WorkflowLogsResult) gen.LogsQueryResponse {
	entries := make([]gen.WorkflowLogEntry, 0, len(result.Logs))
	for i := range result.Logs {
		e := &result.Logs[i]
		ts := e.Timestamp
		log := e.LogMessage
		entries = append(entries, gen.WorkflowLogEntry{
			Timestamp: &ts,
			Log:       &log,
		})
	}

	logs := gen.LogsQueryResponse_Logs{}
	_ = logs.FromLogsQueryResponseLogs1(entries)

	total := capTotal(result.TotalCount)
	took := result.TookMs
	return gen.LogsQueryResponse{
		Logs:   &logs,
		Total:  &total,
		TookMs: &took,
	}
}

func mapComponentEntry(e *cloudlogging.ComponentLogEntry) gen.ComponentLogEntry {
	ts := e.Timestamp
	level := e.LogLevel
	log := e.LogMessage

	entry := gen.ComponentLogEntry{
		Timestamp: &ts,
		Log:       &log,
		Level:     &level,
	}

	metadata := &struct {
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
		ContainerName: ptrStringNonEmpty(e.ContainerName),
		PodName:       ptrStringNonEmpty(e.PodName),
		PodNamespace:  ptrStringNonEmpty(e.PodNamespace),
		NamespaceName: ptrStringNonEmpty(e.OpenChoreoNamespace),
	}

	if uid, ok := parseUUID(e.ComponentUID); ok {
		metadata.ComponentUid = &uid
	}
	if uid, ok := parseUUID(e.ProjectUID); ok {
		metadata.ProjectUid = &uid
	}
	if uid, ok := parseUUID(e.EnvironmentUID); ok {
		metadata.EnvironmentUid = &uid
	}

	entry.Metadata = metadata
	return entry
}

func parseUUID(s string) (openapi_types.UUID, bool) {
	if s == "" {
		return openapi_types.UUID{}, false
	}
	var u openapi_types.UUID
	if err := u.Scan(s); err != nil {
		return openapi_types.UUID{}, false
	}
	return u, true
}

func ptrStringNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func capTotal(n int) int {
	if n > 1000 {
		return 1000
	}
	return n
}
