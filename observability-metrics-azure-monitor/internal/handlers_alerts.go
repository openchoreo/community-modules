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

	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/azuremonitor"
	"github.com/openchoreo/community-modules/observability-metrics-azure-monitor/internal/webhook"
)

// Error codes surfaced in ErrorResponse.errorCode.
const (
	errCodeBadRequest     = "OBS-V1-M-AZURE-MON-400"
	errCodeNotFound       = "OBS-V1-M-AZURE-MON-404"
	errCodeInternal       = "OBS-V1-M-AZURE-MON-500"
	errCodeNotImplemented = "OBS-V1-M-AZURE-MON-501"
	notImplementedDetail  = "alert management is not wired on this adapter"
)

func (h *MetricsHandler) alertingEnabled() bool {
	return h.alertClient != nil
}

func makeError(title gen.ErrorResponseTitle, code, detail string) gen.ErrorResponse {
	return gen.ErrorResponse{Title: &title, ErrorCode: &code, Detail: &detail}
}

func notImplementedErr() gen.ErrorResponse {
	return makeError(gen.InternalServerError, errCodeNotImplemented, notImplementedDetail)
}

func (h *MetricsHandler) CreateAlertRule(ctx context.Context, request gen.CreateAlertRuleRequestObject) (gen.CreateAlertRuleResponseObject, error) {
	if !h.alertingEnabled() {
		return gen.CreateAlertRule500JSONResponse(notImplementedErr()), nil
	}
	if request.Body == nil {
		return gen.CreateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}
	in, err := ruleInputFromRequest(*request.Body)
	if err != nil {
		return gen.CreateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
	}
	res, err := h.alertClient.CreateRule(ctx, in)
	if err != nil {
		if isValidationErr(err) {
			return gen.CreateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
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

func (h *MetricsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	if !h.alertingEnabled() {
		return gen.GetAlertRule500JSONResponse(notImplementedErr()), nil
	}
	if request.RuleName == "" {
		return gen.GetAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "ruleName is required")), nil
	}
	_, namespace, err := h.alertClient.FindRuleByName(ctx, request.RuleName)
	if err != nil {
		if errors.Is(err, azuremonitor.ErrNotFound) {
			return gen.GetAlertRule404JSONResponse(makeError(gen.BadRequest, errCodeNotFound, "alert rule not found")), nil
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

func (h *MetricsHandler) UpdateAlertRule(ctx context.Context, request gen.UpdateAlertRuleRequestObject) (gen.UpdateAlertRuleResponseObject, error) {
	if !h.alertingEnabled() {
		return gen.UpdateAlertRule500JSONResponse(notImplementedErr()), nil
	}
	if request.Body == nil {
		return gen.UpdateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}
	in, err := ruleInputFromRequest(gen.AlertRuleRequest(*request.Body))
	if err != nil {
		return gen.UpdateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
	}
	// The Observer supplies the rule name in the path; trust it over the body.
	if request.RuleName != "" {
		in.RuleName = request.RuleName
	}
	res, err := h.alertClient.UpdateRule(ctx, in)
	if err != nil {
		if isValidationErr(err) {
			return gen.UpdateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
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

func (h *MetricsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	if !h.alertingEnabled() {
		return gen.DeleteAlertRule500JSONResponse(notImplementedErr()), nil
	}
	if request.RuleName == "" {
		return gen.DeleteAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "ruleName is required")), nil
	}
	res, _, err := h.alertClient.FindRuleByName(ctx, request.RuleName)
	if err != nil {
		if errors.Is(err, azuremonitor.ErrNotFound) {
			return gen.DeleteAlertRule404JSONResponse(makeError(gen.BadRequest, errCodeNotFound, "alert rule not found")), nil
		}
		h.logger.Error("find alert rule failed", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.DeleteAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to find alert rule")), nil
	}
	if err := h.alertClient.DeleteRuleByAzureName(ctx, res.LogicalID); err != nil {
		if errors.Is(err, azuremonitor.ErrNotFound) {
			return gen.DeleteAlertRule404JSONResponse(makeError(gen.BadRequest, errCodeNotFound, "alert rule not found")), nil
		}
		h.logger.Error("delete alert rule failed", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.DeleteAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to delete alert rule")), nil
	}
	res.LastSynced = azuremonitor.NowRFC3339()
	return gen.DeleteAlertRule200JSONResponse(syncResponse(res, gen.Deleted, gen.Synced)), nil
}

func (h *MetricsHandler) HandleAlertWebhook(ctx context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	if !h.alertingEnabled() || h.observerClient == nil {
		return gen.HandleAlertWebhook500JSONResponse(notImplementedErr()), nil
	}
	if request.Body == nil {
		return gen.HandleAlertWebhook400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}
	raw, err := json.Marshal(request.Body)
	if err != nil {
		return gen.HandleAlertWebhook400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "failed to re-encode webhook body")), nil
	}
	details, err := webhook.Parse(raw)
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

func ruleInputFromRequest(req gen.AlertRuleRequest) (azuremonitor.RuleInput, error) {
	in := azuremonitor.RuleInput{
		Namespace:      req.Metadata.Namespace,
		RuleName:       req.Metadata.Name,
		ComponentUID:   req.Metadata.ComponentUid.String(),
		ProjectUID:     req.Metadata.ProjectUid.String(),
		EnvironmentUID: req.Metadata.EnvironmentUid.String(),
		Metric:         string(req.Source.Metric),
		Operator:       string(req.Condition.Operator),
		Threshold:      float64(req.Condition.Threshold),
		Interval:       req.Condition.Interval,
		Window:         req.Condition.Window,
		Enabled:        req.Condition.Enabled,
	}
	if strings.TrimSpace(in.RuleName) == "" {
		return in, errors.New("metadata.name is required")
	}
	if strings.TrimSpace(in.Namespace) == "" {
		return in, errors.New("metadata.namespace is required")
	}
	if strings.TrimSpace(in.Metric) == "" {
		return in, errors.New("source.metric is required")
	}
	if strings.TrimSpace(in.Operator) == "" {
		return in, errors.New("condition.operator is required")
	}
	// Validate the metric early so a bad value returns 400, not 500.
	if _, err := azuremonitor.MetricNameForSource(in.Metric); err != nil {
		return in, err
	}
	return in, nil
}

func syncResponse(r *azuremonitor.RuleResult, action gen.AlertingRuleSyncResponseAction, status gen.AlertingRuleSyncResponseStatus) gen.AlertingRuleSyncResponse {
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

func isValidationErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "is required") ||
		strings.Contains(msg, "unsupported operator") ||
		strings.Contains(msg, "parse duration")
}
