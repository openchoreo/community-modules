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

	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/observer"
	"github.com/openchoreo/community-modules/observability-logs-opensearch/internal/opensearch"
)

// LogsHandler implements the generated StrictServerInterface.
type LogsHandler struct {
	osClient       *opensearch.Client
	queryBuilder   *opensearch.QueryBuilder
	observerClient *observer.Client
	logger         *slog.Logger
}

// NewLogsHandler creates a new LogsHandler.
func NewLogsHandler(
	osClient *opensearch.Client,
	queryBuilder *opensearch.QueryBuilder,
	observerClient *observer.Client,
	logger *slog.Logger,
) *LogsHandler {
	return &LogsHandler{
		osClient:       osClient,
		queryBuilder:   queryBuilder,
		observerClient: observerClient,
		logger:         logger,
	}
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
		return gen.QueryLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	// Try to interpret the search scope as a WorkflowSearchScope first
	workflowScope, err := request.Body.SearchScope.AsWorkflowSearchScope()
	if err == nil && workflowScope.WorkflowRunName != nil {
		if strings.TrimSpace(workflowScope.Namespace) == "" {
			return gen.QueryLogs400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("searchScope with a valid namespace is required"),
			}, nil
		}

		return h.queryWorkflowLogs(ctx, request.Body, &workflowScope)
	}

	// Fall back to ComponentSearchScope
	scope, err := request.Body.SearchScope.AsComponentSearchScope()
	if err != nil || strings.TrimSpace(scope.Namespace) == "" {
		return gen.QueryLogs400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("searchScope with a valid namespace is required"),
		}, nil
	}

	return h.queryComponentLogs(ctx, request.Body, &scope)
}

func (h *LogsHandler) queryComponentLogs(ctx context.Context, req *gen.LogsQueryRequest, scope *gen.ComponentSearchScope) (gen.QueryLogsResponseObject, error) {
	startTime := req.StartTime.Format(time.RFC3339)
	endTime := req.EndTime.Format(time.RFC3339)

	params := opensearch.ComponentLogsQueryParamsV1{
		StartTime:     startTime,
		EndTime:       endTime,
		NamespaceName: scope.Namespace,
	}
	if scope.ProjectUid != nil {
		params.ProjectID = *scope.ProjectUid
	}
	if scope.EnvironmentUid != nil {
		params.EnvironmentID = *scope.EnvironmentUid
	}
	if scope.ComponentUid != nil {
		params.ComponentID = *scope.ComponentUid
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

	query, err := h.queryBuilder.BuildComponentLogsQueryV1(params)
	if err != nil {
		h.logger.Error("Failed to build component logs query",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	indices, err := h.queryBuilder.GenerateIndices(startTime, endTime)
	if err != nil {
		h.logger.Error("Failed to generate indices",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	result, err := h.osClient.Search(ctx, indices, query)
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

	entries := make([]gen.ComponentLogEntry, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		logEntry := opensearch.ParseLogEntry(hit)
		entry := toComponentLogEntry(&logEntry)
		entries = append(entries, entry)
	}

	total := result.Hits.Total.Value
	took := result.Took
	resp := gen.LogsQueryResponse{
		Total:  &total,
		TookMs: &took,
	}
	logs := gen.LogsQueryResponse_Logs{}
	if err := logs.FromLogsQueryResponseLogs0(entries); err != nil {
		h.logger.Error("Failed to serialize component log entries",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	resp.Logs = &logs

	return gen.QueryLogs200JSONResponse(resp), nil
}

func (h *LogsHandler) queryWorkflowLogs(ctx context.Context, req *gen.LogsQueryRequest, scope *gen.WorkflowSearchScope) (gen.QueryLogsResponseObject, error) {
	startTime := req.StartTime.Format(time.RFC3339)
	endTime := req.EndTime.Format(time.RFC3339)

	queryParams := opensearch.QueryParams{
		StartTime:     startTime,
		EndTime:       endTime,
		NamespaceName: scope.Namespace,
		Limit:         100,
		SortOrder:     "desc",
	}
	if req.Limit != nil {
		queryParams.Limit = *req.Limit
	}
	if req.SortOrder != nil {
		queryParams.SortOrder = string(*req.SortOrder)
	}
	if req.SearchPhrase != nil {
		queryParams.SearchPhrase = *req.SearchPhrase
	}
	if req.LogLevels != nil {
		levels := make([]string, len(*req.LogLevels))
		for i, l := range *req.LogLevels {
			levels[i] = string(l)
		}
		queryParams.LogLevels = levels
	}

	workflowRunName := ""
	if scope.WorkflowRunName != nil {
		workflowRunName = *scope.WorkflowRunName
	}

	params := opensearch.WorkflowRunQueryParams{
		QueryParams:   queryParams,
		WorkflowRunID: workflowRunName,
	}

	query := h.queryBuilder.BuildWorkflowRunLogsQuery(params)

	indices, err := h.queryBuilder.GenerateIndices(startTime, endTime)
	if err != nil {
		h.logger.Error("Failed to generate indices",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	result, err := h.osClient.Search(ctx, indices, query)
	if err != nil {
		h.logger.Error("Failed to query workflow logs",
			slog.String("function", "QueryLogs"),
			slog.String("namespace", scope.Namespace),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	entries := make([]gen.WorkflowLogEntry, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		var ts *time.Time
		if tsVal, ok := hit.Source["@timestamp"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, tsVal); err == nil {
				ts = &parsed
			}
		}
		log := ""
		if logVal, ok := hit.Source["log"].(string); ok {
			log = logVal
		}
		entry := gen.WorkflowLogEntry{
			Timestamp: ts,
			Log:       &log,
		}
		entries = append(entries, entry)
	}

	total := result.Hits.Total.Value
	took := result.Took
	resp := gen.LogsQueryResponse{
		Total:  &total,
		TookMs: &took,
	}
	logs := gen.LogsQueryResponse_Logs{}
	if err := logs.FromLogsQueryResponseLogs1(entries); err != nil {
		h.logger.Error("Failed to serialize workflow log entries",
			slog.String("function", "QueryLogs"),
			slog.Any("error", err),
		)
		return gen.QueryLogs500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	resp.Logs = &logs

	return gen.QueryLogs200JSONResponse(resp), nil
}

// CreateAlertRule implements POST /api/v1alpha1/alerts/rules.
func (h *LogsHandler) CreateAlertRule(ctx context.Context, request gen.CreateAlertRuleRequestObject) (gen.CreateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.CreateAlertRule400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	params := toAlertingRuleRequest(request.Body)

	monitorBody, err := h.queryBuilder.BuildLogAlertingRuleMonitorBody(params)
	if err != nil {
		h.logger.Error("Failed to build monitor body",
			slog.String("function", "CreateAlertRule"),
			slog.Any("error", err),
		)
		return gen.CreateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	alertID, _, err := h.osClient.CreateMonitor(ctx, monitorBody)
	if err != nil {
		h.logger.Error("Failed to create monitor",
			slog.String("function", "CreateAlertRule"),
			slog.Any("alertName", params.Metadata.Name),
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
		RuleLogicalId: &params.Metadata.Name,
		RuleBackendId: &alertID,
		LastSyncedAt:  &now,
	}, nil
}

// DeleteAlertRule implements DELETE /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	monitorID, found, err := h.osClient.SearchMonitorByName(ctx, request.RuleName)
	if err != nil {
		h.logger.Error("Failed to search for monitor",
			slog.String("function", "DeleteAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.DeleteAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	if !found {
		return gen.DeleteAlertRule404JSONResponse{
			Title:   ptr(gen.NotFound),
			Message: ptr("alert rule not found"),
		}, nil
	}

	if err := h.osClient.DeleteMonitor(ctx, monitorID); err != nil {
		h.logger.Error("Failed to delete monitor",
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
		RuleBackendId: &monitorID,
		LastSyncedAt:  &now,
	}, nil
}

// GetAlertRule implements GET /api/v1alpha1/alerts/rules/{ruleName}.
func (h *LogsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	monitorID, found, err := h.osClient.SearchMonitorByName(ctx, request.RuleName)
	if err != nil {
		h.logger.Error("Failed to search for monitor",
			slog.String("function", "GetAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.GetAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	if !found {
		return gen.GetAlertRule404JSONResponse{
			Title:   ptr(gen.NotFound),
			Message: ptr("alert rule not found"),
		}, nil
	}

	monitor, err := h.osClient.GetMonitorByID(ctx, monitorID)
	if err != nil {
		h.logger.Error("Failed to get monitor",
			slog.String("function", "GetAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.GetAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	response, err := parseMonitorToAlertRuleResponse(monitor)
	if err != nil {
		h.logger.Error("Failed to parse monitor to alert rule response",
			slog.String("function", "GetAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.GetAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
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

	monitorID, found, err := h.osClient.SearchMonitorByName(ctx, request.RuleName)
	if err != nil {
		h.logger.Error("Failed to search for monitor",
			slog.String("function", "UpdateAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.UpdateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}
	if !found {
		return gen.UpdateAlertRule404JSONResponse{
			Title:   ptr(gen.NotFound),
			Message: ptr("alert rule not found"),
		}, nil
	}

	params := toAlertingRuleRequest(request.Body)

	monitorBody, err := h.queryBuilder.BuildLogAlertingRuleMonitorBody(params)
	if err != nil {
		h.logger.Error("Failed to build monitor body",
			slog.String("function", "UpdateAlertRule"),
			slog.Any("error", err),
		)
		return gen.UpdateAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	if _, err := h.osClient.UpdateMonitor(ctx, monitorID, monitorBody); err != nil {
		h.logger.Error("Failed to update monitor",
			slog.String("function", "UpdateAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
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
		RuleBackendId: &monitorID,
		LastSyncedAt:  &now,
	}, nil
}

// HandleAlertWebhook implements POST /api/v1alpha1/alerts/webhook.
func (h *LogsHandler) HandleAlertWebhook(_ context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	if request.Body == nil {
		h.logger.Warn("Alert webhook received with nil body")
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}
	body := *request.Body

	ruleName, ruleNamespace, alertValue, alertTimestamp, err := parseAlertWebhookBody(body)
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

		if err := h.observerClient.ForwardAlert(forwardCtx, ruleName, ruleNamespace, alertValue, alertTimestamp); err != nil {
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

// parseAlertWebhookBody extracts alert fields from the incoming OpenSearch alerting webhook body.
func parseAlertWebhookBody(body map[string]interface{}) (ruleName string, ruleNamespace string, alertValue float64, alertTimestamp time.Time, err error) {
	ruleNameVal, ok := body["ruleName"]
	if !ok {
		return "", "", 0, time.Time{}, fmt.Errorf("missing ruleName in webhook body")
	}
	ruleName, ok = ruleNameVal.(string)
	if !ok {
		return "", "", 0, time.Time{}, fmt.Errorf("ruleName is not a string")
	}

	if nsVal, ok := body["ruleNamespace"]; ok {
		if ns, ok := nsVal.(string); ok {
			ruleNamespace = ns
		}
	}

	if countVal, ok := body["alertValue"]; ok {
		switch v := countVal.(type) {
		case float64:
			alertValue = v
		case string:
			alertValue, err = strconv.ParseFloat(v, 64)
			if err != nil {
				return "", "", 0, time.Time{}, fmt.Errorf("failed to parse alertValue %q: %w", v, err)
			}
		}
	}

	if tsVal, ok := body["alertTimestamp"]; ok {
		switch v := tsVal.(type) {
		case string:
			parsed, parseErr := time.Parse(time.RFC3339, v)
			if parseErr != nil {
				alertTimestamp = time.Now()
			} else {
				alertTimestamp = parsed
			}
		default:
			alertTimestamp = time.Now()
		}
	} else {
		alertTimestamp = time.Now()
	}

	return ruleName, ruleNamespace, alertValue, alertTimestamp, nil
}

// toAlertingRuleRequest converts the generated AlertRuleRequest to the internal type.
func toAlertingRuleRequest(req *gen.AlertRuleRequest) opensearch.AlertingRuleRequest {
	return opensearch.AlertingRuleRequest{
		Metadata: opensearch.AlertingRuleMetadata{
			Name:           req.Metadata.Name,
			Namespace:      req.Metadata.Namespace,
			ProjectUID:     req.Metadata.ProjectUid.String(),
			EnvironmentUID: req.Metadata.EnvironmentUid.String(),
			ComponentUID:   req.Metadata.ComponentUid.String(),
		},
		Source: opensearch.AlertingRuleSource{
			Query: req.Source.Query,
		},
		Condition: opensearch.AlertingRuleCondition{
			Enabled:   req.Condition.Enabled,
			Window:    req.Condition.Window,
			Interval:  req.Condition.Interval,
			Operator:  string(req.Condition.Operator),
			Threshold: float64(req.Condition.Threshold),
		},
	}
}

// parseMonitorToAlertRuleResponse parses an OpenSearch monitor to the API AlertRuleResponse.
func parseMonitorToAlertRuleResponse(monitor map[string]interface{}) (gen.AlertRuleResponse, error) {
	name := getStringFromMap(monitor, "name")

	// Extract metadata from the monitor's trigger action message template
	var namespace, projectUID, environmentUID, componentUID, searchQuery string
	var enabled bool
	var threshold float32
	var operator, window, interval string

	if enabledVal, ok := monitor["enabled"].(bool); ok {
		enabled = enabledVal
	}

	// Extract query and metadata from inputs
	if inputs, ok := monitor["inputs"].([]interface{}); ok && len(inputs) > 0 {
		if input, ok := inputs[0].(map[string]interface{}); ok {
			if search, ok := input["search"].(map[string]interface{}); ok {
				if queryMap, ok := search["query"].(map[string]interface{}); ok {
					searchQuery, namespace, projectUID, environmentUID, componentUID = extractQueryMetadata(queryMap)
				}
			}
		}
	}

	// Extract trigger condition
	if triggers, ok := monitor["triggers"].([]interface{}); ok && len(triggers) > 0 {
		if trigger, ok := triggers[0].(map[string]interface{}); ok {
			if qlt, ok := trigger["query_level_trigger"].(map[string]interface{}); ok {
				if condition, ok := qlt["condition"].(map[string]interface{}); ok {
					if script, ok := condition["script"].(map[string]interface{}); ok {
						if source, ok := script["source"].(string); ok {
							operator, threshold = parseConditionScript(source)
						}
					}
				}
			}
		}
	}

	// Extract schedule for interval
	if schedule, ok := monitor["schedule"].(map[string]interface{}); ok {
		if period, ok := schedule["period"].(map[string]interface{}); ok {
			intervalVal := 0.0
			if v, ok := period["interval"].(float64); ok {
				intervalVal = v
			}
			unit := getStringFromMap(period, "unit")
			interval = formatScheduleToInterval(intervalVal, unit)
		}
	}

	// Extract window from query time range
	window = extractWindowFromQuery(monitor)

	operatorEnum := gen.AlertRuleResponseConditionOperator(opensearch.ReverseMapOperator(operator))

	metadata := &struct {
		ComponentUid   *openapi_types.UUID `json:"componentUid,omitempty"`
		EnvironmentUid *openapi_types.UUID `json:"environmentUid,omitempty"`
		Name           *string             `json:"name,omitempty"`
		Namespace      *string             `json:"namespace,omitempty"`
		ProjectUid     *openapi_types.UUID `json:"projectUid,omitempty"`
	}{
		Name:      &name,
		Namespace: strPtr(namespace),
	}
	if projectUID != "" {
		if uid, ok := parseUUID(projectUID); ok {
			metadata.ProjectUid = &uid
		}
	}
	if environmentUID != "" {
		if uid, ok := parseUUID(environmentUID); ok {
			metadata.EnvironmentUid = &uid
		}
	}
	if componentUID != "" {
		if uid, ok := parseUUID(componentUID); ok {
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
			Query:  &searchQuery,
		},
		Condition: &struct {
			Enabled   *bool                                  `json:"enabled,omitempty"`
			Interval  *string                                `json:"interval,omitempty"`
			Operator  *gen.AlertRuleResponseConditionOperator `json:"operator,omitempty"`
			Threshold *float32                               `json:"threshold,omitempty"`
			Window    *string                                `json:"window,omitempty"`
		}{
			Enabled:   &enabled,
			Operator:  &operatorEnum,
			Threshold: &threshold,
			Window:    &window,
			Interval:  &interval,
		},
	}

	return response, nil
}

// extractQueryMetadata extracts metadata from the monitor's search query.
func extractQueryMetadata(queryMap map[string]interface{}) (searchQuery, namespace, projectUID, environmentUID, componentUID string) {
	boolQuery, ok := queryMap["query"].(map[string]interface{})
	if !ok {
		return
	}
	boolMap, ok := boolQuery["bool"].(map[string]interface{})
	if !ok {
		return
	}
	filters, ok := boolMap["filter"].([]interface{})
	if !ok {
		return
	}

	for _, f := range filters {
		filter, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		if termMap, ok := filter["term"].(map[string]interface{}); ok {
			for key, val := range termMap {
				valMap, ok := val.(map[string]interface{})
				if !ok {
					continue
				}
				value := getStringFromMap(valMap, "value")
				switch key {
				case opensearch.OSComponentID:
					componentUID = value
				case opensearch.OSEnvironmentID:
					environmentUID = value
				case opensearch.OSProjectID:
					projectUID = value
				}
			}
		}
		if wildcardMap, ok := filter["wildcard"].(map[string]interface{}); ok {
			if logMap, ok := wildcardMap["log"].(map[string]interface{}); ok {
				if pattern, ok := logMap["wildcard"].(string); ok {
					// Remove leading/trailing * from pattern
					searchQuery = strings.TrimPrefix(strings.TrimSuffix(pattern, "*"), "*")
				}
			}
		}
	}

	return searchQuery, namespace, projectUID, environmentUID, componentUID
}

// parseConditionScript parses the trigger condition script to extract operator and threshold.
func parseConditionScript(source string) (string, float32) {
	// Format: "ctx.results[0].hits.total.value > 10"
	parts := strings.Fields(source)
	if len(parts) < 3 {
		return "", 0
	}
	operator := parts[len(parts)-2]
	thresholdStr := parts[len(parts)-1]
	threshold, _ := strconv.ParseFloat(thresholdStr, 32)
	return operator, float32(threshold)
}

// formatScheduleToInterval converts schedule period to a duration string.
func formatScheduleToInterval(interval float64, unit string) string {
	switch strings.ToUpper(unit) {
	case "MINUTES":
		return fmt.Sprintf("%dm", int(interval))
	case "HOURS":
		return fmt.Sprintf("%dh", int(interval))
	}
	return fmt.Sprintf("%dm", int(interval))
}

// extractWindowFromQuery extracts the window duration from the monitor's query time range.
func extractWindowFromQuery(monitor map[string]interface{}) string {
	if inputs, ok := monitor["inputs"].([]interface{}); ok && len(inputs) > 0 {
		if input, ok := inputs[0].(map[string]interface{}); ok {
			if search, ok := input["search"].(map[string]interface{}); ok {
				if queryMap, ok := search["query"].(map[string]interface{}); ok {
					if boolQuery, ok := queryMap["query"].(map[string]interface{}); ok {
						if boolMap, ok := boolQuery["bool"].(map[string]interface{}); ok {
							if filters, ok := boolMap["filter"].([]interface{}); ok {
								for _, f := range filters {
									filter, ok := f.(map[string]interface{})
									if !ok {
										continue
									}
									if rangeMap, ok := filter["range"].(map[string]interface{}); ok {
										if tsMap, ok := rangeMap["@timestamp"].(map[string]interface{}); ok {
											if from, ok := tsMap["from"].(string); ok {
												// Format: "{{period_end}}||-1h"
												if idx := strings.Index(from, "||-"); idx != -1 {
													return from[idx+3:]
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return ""
}

func toComponentLogEntry(l *opensearch.LogEntry) gen.ComponentLogEntry {
	ts := l.Timestamp
	entry := gen.ComponentLogEntry{
		Timestamp: &ts,
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
			NamespaceName:   strPtr(l.NamespaceName),
			ContainerName:   strPtr(l.ContainerName),
			PodName:         strPtr(l.PodName),
			PodNamespace:    strPtr(l.PodNamespace),
			ComponentName:   strPtr(l.ComponentName),
			ProjectName:     strPtr(l.ProjectName),
			EnvironmentName: strPtr(l.EnvironmentName),
		},
	}

	if l.ComponentID != "" {
		if uid, ok := parseUUID(l.ComponentID); ok {
			entry.Metadata.ComponentUid = &uid
		}
	}
	if l.ProjectID != "" {
		if uid, ok := parseUUID(l.ProjectID); ok {
			entry.Metadata.ProjectUid = &uid
		}
	}
	if l.EnvironmentID != "" {
		if uid, ok := parseUUID(l.EnvironmentID); ok {
			entry.Metadata.EnvironmentUid = &uid
		}
	}

	return entry
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
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
