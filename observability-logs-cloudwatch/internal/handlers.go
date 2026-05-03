// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-cloudwatch/internal/cloudwatch"
)

type logsClient interface {
	Ping(context.Context) error
	GetComponentLogs(context.Context, cloudwatch.ComponentLogsParams) (*cloudwatch.ComponentLogsResult, error)
	GetWorkflowLogs(context.Context, cloudwatch.WorkflowLogsParams) (*cloudwatch.WorkflowLogsResult, error)
	CreateAlert(context.Context, cloudwatch.LogAlertParams) (string, error)
	GetAlert(context.Context, string, string) (*cloudwatch.AlertDetail, error)
	UpdateAlert(context.Context, string, string, cloudwatch.LogAlertParams) (string, error)
	DeleteAlert(context.Context, string, string) (string, error)
	GetAlarmTagsByName(context.Context, string) (map[string]string, error)
}

type observerForwarder interface {
	ForwardAlert(context.Context, string, string, float64, time.Time) error
}

// LogsHandler implements the generated StrictServerInterface backed by CloudWatch Logs Insights.
type LogsHandler struct {
	client                   logsClient
	observerClient           observerForwarder
	snsAllowSubscribeConfirm bool
	forwardRecovery          bool
	logger                   *slog.Logger
}

// HandlerOptions bundles the non-client dependencies of LogsHandler.
type HandlerOptions struct {
	ObserverClient           observerForwarder
	SNSAllowSubscribeConfirm bool
	ForwardRecovery          bool
}

func NewLogsHandler(client logsClient, logger *slog.Logger) *LogsHandler {
	return &LogsHandler{
		client: client,
		logger: logger,
	}
}

// NewLogsHandlerWithOptions constructs a LogsHandler with alerting wiring.
func NewLogsHandlerWithOptions(client logsClient, opts HandlerOptions, logger *slog.Logger) *LogsHandler {
	observerClient := opts.ObserverClient
	if isNilObserverForwarder(observerClient) {
		observerClient = nil
	}
	return &LogsHandler{
		client:                   client,
		observerClient:           observerClient,
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

// Compile-time check that LogsHandler satisfies the generated interface.
var _ gen.StrictServerInterface = (*LogsHandler)(nil)

// Health implements the health check endpoint. AWS connectivity is verified
// once at startup (main.go calls cwClient.Ping); the pod exits on failure, so
// reaching this handler at request time means the process is up. Matches the
// openobserve adapter's behaviour and avoids per-poll AWS calls (and the
// log noise from transient cluster-DNS hiccups resolving STS).
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

	// WorkflowSearchScope is identified by the presence of workflowRunName.
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

	return gen.QueryLogs200JSONResponse(toComponentLogsQueryResponse(result)), nil
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

	// Detect duplicates before create. AWS PutMetricAlarm is upsert, so we
	// need an explicit GET to preserve 409 semantics.
	if existing, err := h.client.GetAlert(ctx, params.Namespace, params.Name); err == nil && existing != nil {
		return gen.CreateAlertRule409JSONResponse{
			Title:   ptr(gen.Conflict),
			Message: ptr("alert rule already exists"),
		}, nil
	} else if err != nil && !errors.Is(err, cloudwatch.ErrAlertNotFound) {
		h.logger.Warn("Duplicate-check failed for CreateAlertRule",
			slog.String("ruleName", params.Name),
			slog.Any("error", err),
		)
	}

	arn, err := h.client.CreateAlert(ctx, params)
	if err != nil {
		if isValidationError(err) {
			return gen.CreateAlertRule400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr(err.Error()),
			}, nil
		}
		h.logger.Error("Failed to create alert",
			slog.String("function", "CreateAlertRule"),
			slog.String("alertName", params.Name),
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
		RuleLogicalId: &params.Name,
		RuleBackendId: &arn,
		LastSyncedAt:  &now,
	}, nil
}

func (h *LogsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	arn, err := h.client.DeleteAlert(ctx, "", request.RuleName)
	if err != nil {
		if errors.Is(err, cloudwatch.ErrAlertNotFound) {
			return gen.DeleteAlertRule404JSONResponse{
				Title:   ptr(gen.NotFound),
				Message: ptr("alert rule not found"),
			}, nil
		}
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
		RuleBackendId: &arn,
		LastSyncedAt:  &now,
	}, nil
}

func (h *LogsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	alert, err := h.client.GetAlert(ctx, "", request.RuleName)
	if err != nil {
		if errors.Is(err, cloudwatch.ErrAlertNotFound) {
			return gen.GetAlertRule404JSONResponse{
				Title:   ptr(gen.NotFound),
				Message: ptr("alert rule not found"),
			}, nil
		}
		h.logger.Error("Failed to get alert",
			slog.String("function", "GetAlertRule"),
			slog.String("ruleName", request.RuleName),
			slog.Any("error", err),
		)
		return gen.GetAlertRule500JSONResponse{
			Title:   ptr(gen.InternalServerError),
			Message: ptr("internal server error"),
		}, nil
	}

	return gen.GetAlertRule200JSONResponse(toAlertRuleResponse(alert)), nil
}

func (h *LogsHandler) UpdateAlertRule(ctx context.Context, request gen.UpdateAlertRuleRequestObject) (gen.UpdateAlertRuleResponseObject, error) {
	if request.Body == nil {
		return gen.UpdateAlertRule400JSONResponse{
			Title:   ptr(gen.BadRequest),
			Message: ptr("request body is required"),
		}, nil
	}

	params := toLogAlertParams(request.Body)
	params.Name = request.RuleName
	arn, err := h.client.UpdateAlert(ctx, params.Namespace, request.RuleName, params)
	if err != nil {
		if errors.Is(err, cloudwatch.ErrAlertNotFound) {
			return gen.UpdateAlertRule400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr("alert rule not found"),
			}, nil
		}
		if isValidationError(err) {
			return gen.UpdateAlertRule400JSONResponse{
				Title:   ptr(gen.BadRequest),
				Message: ptr(err.Error()),
			}, nil
		}
		h.logger.Error("Failed to update alert",
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
		RuleBackendId: &arn,
		LastSyncedAt:  &now,
	}, nil
}

// HandleAlertWebhook parses SNS / EventBridge / Lambda forwarder payloads and
// forwards `ALARM` state transitions to the Observer.
func (h *LogsHandler) HandleAlertWebhook(ctx context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	if request.Body == nil {
		h.logger.Warn("Alert webhook received with nil body")
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}

	raw, err := json.Marshal(*request.Body)
	if err != nil {
		h.logger.Warn("Failed to re-marshal webhook body", slog.Any("error", err))
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}

	event, confirm, err := parseWebhookBody(raw)
	if err != nil {
		h.logger.Warn("Failed to parse webhook body", slog.Any("error", err))
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}

	if confirm != nil {
		if !h.snsAllowSubscribeConfirm {
			h.logger.Warn("SNS subscription confirmation received but SNS_ALLOW_SUBSCRIBE_CONFIRM=false; ignoring",
				slog.String("topicArn", confirm.TopicARN),
			)
		} else if confirm.SubscribeURL != "" {
			go h.confirmSNSSubscription(confirm)
		}
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("subscription confirmation received"),
			Status:  ptr(gen.Success),
		}, nil
	}

	if event == nil {
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}

	if event.State != "" && event.State != "ALARM" && !h.forwardRecovery {
		h.logger.Debug("Ignoring non-ALARM webhook transition",
			slog.String("state", event.State),
			slog.String("alarmName", event.AlarmName),
		)
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}

	if h.observerClient == nil {
		h.logger.Debug("Observer not configured; dropping alert webhook",
			slog.String("alarmName", event.AlarmName),
		)
		return gen.HandleAlertWebhook200JSONResponse{
			Message: ptr("alert webhook received successfully"),
			Status:  ptr(gen.Success),
		}, nil
	}

	h.logger.Info("Alert webhook received",
		slog.String("path", "/api/v1alpha1/alerts/webhook"),
		slog.String("alarmName", event.AlarmName),
		slog.String("state", event.State),
		slog.String("ruleName", event.RuleName),
		slog.String("ruleNamespace", event.RuleNamespace),
	)
	go h.forwardAlertEvent(event)

	return gen.HandleAlertWebhook200JSONResponse{
		Message: ptr("alert webhook received successfully"),
		Status:  ptr(gen.Success),
	}, nil
}

func (h *LogsHandler) forwardAlertEvent(event *cloudwatch.ParsedAlertEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// If the parsed event lacks the rule name/namespace (SNS path), try to
	// recover them from alarm tags via DescribeAlarms+ListTagsForResource.
	if (event.RuleName == "" || event.RuleNamespace == "") && event.AlarmName != "" {
		if tags, err := h.client.GetAlarmTagsByName(ctx, event.AlarmName); err == nil {
			cloudwatch.ApplyTagsToEvent(event, tags)
		} else {
			h.logger.Warn("Failed to hydrate alarm tags",
				slog.String("alarmName", event.AlarmName),
				slog.Any("error", err),
			)
		}
	}

	if event.RuleName == "" {
		h.logger.Warn("Dropping alert: could not determine rule name",
			slog.String("alarmName", event.AlarmName),
		)
		return
	}

	if err := h.observerClient.ForwardAlert(ctx, event.RuleName, event.RuleNamespace, event.AlertValue, event.AlertTimestamp); err != nil {
		h.logger.Error("Failed to forward alert to Observer",
			slog.String("ruleName", event.RuleName),
			slog.Any("error", err),
		)
		return
	}
	h.logger.Info("Forwarded alert to Observer",
		slog.String("ruleName", event.RuleName),
		slog.String("ruleNamespace", event.RuleNamespace),
		slog.Float64("alertValue", event.AlertValue),
	)
}

func (h *LogsHandler) confirmSNSSubscription(env *cloudwatch.SNSEnvelopeResult) {
	if err := cloudwatch.VerifySNSMessageSignature(env); err != nil {
		h.logger.Warn("Rejecting SNS subscription confirmation: signature verification failed",
			slog.Any("error", err),
		)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.SubscribeURL, nil)
	if err != nil {
		h.logger.Warn("Failed to build SNS SubscribeURL request", slog.Any("error", err))
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.logger.Warn("Failed to call SNS SubscribeURL", slog.Any("error", err))
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		h.logger.Warn("SNS SubscribeURL returned non-2xx",
			slog.Int("statusCode", resp.StatusCode),
		)
		return
	}
	h.logger.Info("Confirmed SNS subscription",
		slog.String("topicArn", env.TopicARN),
	)
}

// parseWebhookBody picks the correct parser based on envelope shape. The raw
// JSON has already been produced by re-marshalling `map[string]interface{}`.
func parseWebhookBody(raw []byte) (*cloudwatch.ParsedAlertEvent, *cloudwatch.SNSEnvelopeResult, error) {
	var envShape struct {
		Type    string          `json:"Type"`
		Source  string          `json:"source"`
		Detail  json.RawMessage `json:"detail"`
		Message json.RawMessage `json:"Message"`
	}
	_ = json.Unmarshal(raw, &envShape)

	switch {
	case envShape.Type != "":
		res, err := cloudwatch.ParseSNSEnvelope(raw)
		if err != nil {
			return nil, nil, err
		}
		if res.IsSubscriptionConfirm {
			return nil, res, nil
		}
		return res.Event, nil, nil
	case envShape.Source == "aws.cloudwatch" || len(envShape.Detail) > 0:
		evt, err := cloudwatch.ParseEventBridgeEvent(raw)
		return evt, nil, err
	default:
		evt, err := cloudwatch.ParseLambdaForwarderEvent(raw)
		return evt, nil, err
	}
}

func isValidationError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "invalid:")
}

// toLogAlertParams converts the generated AlertRuleRequest to internal params.
func toLogAlertParams(req *gen.AlertRuleRequest) cloudwatch.LogAlertParams {
	params := cloudwatch.LogAlertParams{
		Name:           req.Metadata.Name,
		Namespace:      req.Metadata.Namespace,
		ProjectUID:     req.Metadata.ProjectUid.String(),
		EnvironmentUID: req.Metadata.EnvironmentUid.String(),
		ComponentUID:   req.Metadata.ComponentUid.String(),
		SearchPattern:  req.Source.Query,
		Operator:       string(req.Condition.Operator),
		Threshold:      float64(req.Condition.Threshold),
		Enabled:        req.Condition.Enabled,
	}
	if d, err := cloudwatch.ParseDurationStrict(req.Condition.Window); err == nil {
		params.Window = d
	}
	if d, err := cloudwatch.ParseDurationStrict(req.Condition.Interval); err == nil {
		params.Interval = d
	}
	return params
}

func toAlertRuleResponse(alert *cloudwatch.AlertDetail) gen.AlertRuleResponse {
	operator := gen.AlertRuleResponseConditionOperator(alert.Operator)
	threshold := float32(alert.Threshold)
	window := cloudwatch.FormatDuration(alert.Window)
	interval := cloudwatch.FormatDuration(alert.Interval)
	enabled := alert.Enabled
	query := alert.SearchPattern

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

	return gen.AlertRuleResponse{
		Metadata: metadata,
		Source: &struct {
			Metric *gen.AlertRuleResponseSourceMetric `json:"metric,omitempty"`
			Query  *string                            `json:"query,omitempty"`
		}{
			Query: &query,
		},
		Condition: &struct {
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
		},
	}
}

// --- request/response mapping --------------------------------------------------

func toComponentLogsParams(req *gen.LogsQueryRequest, scope *gen.ComponentSearchScope) cloudwatch.ComponentLogsParams {
	params := cloudwatch.ComponentLogsParams{
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

func toWorkflowLogsParams(req *gen.LogsQueryRequest, scope *gen.WorkflowSearchScope) cloudwatch.WorkflowLogsParams {
	params := cloudwatch.WorkflowLogsParams{
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

func toComponentLogsQueryResponse(result *cloudwatch.ComponentLogsResult) gen.LogsQueryResponse {
	entries := make([]gen.ComponentLogEntry, 0, len(result.Logs))
	for i := range result.Logs {
		entries = append(entries, toComponentLogEntry(&result.Logs[i]))
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

func toWorkflowLogsQueryResponse(result *cloudwatch.WorkflowLogsResult) gen.LogsQueryResponse {
	entries := make([]gen.WorkflowLogEntry, 0, len(result.Logs))
	for _, l := range result.Logs {
		entries = append(entries, gen.WorkflowLogEntry{
			Timestamp: &l.Timestamp,
			Log:       &l.Log,
		})
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

func toComponentLogEntry(l *cloudwatch.ComponentLogsEntry) gen.ComponentLogEntry {
	entry := gen.ComponentLogEntry{
		Timestamp: &l.Timestamp,
		Log:       &l.Log,
		Level:     &l.LogLevel,
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
