// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/cloudmonitoring"
	"github.com/openchoreo/community-modules/observability-metrics-gcp-cloudmonitoring/internal/webhook"
)

// alertsNotImplementedDetail is returned by the alert-rule endpoints when
// alerting is not wired (no Observer URL / notification channel configured).
const alertsNotImplementedDetail = "alert rule management is not enabled on this adapter (set OBSERVER_URL and NOTIFICATION_CHANNEL_ID)"

func (h *MetricsHandler) alertingEnabled() bool { return h.alertClient != nil }

func notImplementedErr() gen.ErrorResponse {
	return makeError(gen.InternalServerError, errCodeNotImplemented, alertsNotImplementedDetail)
}

// updateAlertRuleNotFoundResponse returns 404 for an update of a missing rule.
// The generated spec defines no 404 response for PUT (unlike GET/DELETE), so
// this custom visitor supplies it.
type updateAlertRuleNotFoundResponse struct {
	gen.ErrorResponse
}

func (r updateAlertRuleNotFoundResponse) VisitUpdateAlertRuleResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	return json.NewEncoder(w).Encode(r.ErrorResponse)
}

// --- Alert endpoints ---

// CreateAlertRule handles POST /api/v1alpha1/alerts/rules.
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
		if errors.Is(err, cloudmonitoring.ErrRuleAlreadyExists) {
			return gen.CreateAlertRule409JSONResponse(makeError(gen.BadRequest, errCodeConflict, "alert rule already exists")), nil
		}
		if errors.Is(err, cloudmonitoring.ErrValidation) {
			return gen.CreateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
		}
		h.logger.Error("create alert rule failed",
			slog.String("ruleName", in.RuleName), slog.String("namespace", in.Namespace), slog.Any("error", err))
		return gen.CreateAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to create alert rule")), nil
	}
	return gen.CreateAlertRule201JSONResponse(syncResponse(res, gen.Created, gen.Synced)), nil
}

// GetAlertRule handles GET /api/v1alpha1/alerts/rules/{ruleName}.
func (h *MetricsHandler) GetAlertRule(ctx context.Context, request gen.GetAlertRuleRequestObject) (gen.GetAlertRuleResponseObject, error) {
	if !h.alertingEnabled() {
		return gen.GetAlertRule500JSONResponse(notImplementedErr()), nil
	}
	if strings.TrimSpace(request.RuleName) == "" {
		return gen.GetAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "ruleName is required")), nil
	}
	_, namespace, err := h.alertClient.FindRuleByName(ctx, request.RuleName)
	if err != nil {
		if errors.Is(err, cloudmonitoring.ErrRuleNotFound) {
			return gen.GetAlertRule404JSONResponse(makeError(gen.BadRequest, errCodeNotFound, "alert rule not found")), nil
		}
		h.logger.Error("get alert rule failed", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.GetAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to get alert rule")), nil
	}
	name := request.RuleName
	resp := gen.AlertRuleResponse{Metadata: &struct {
		ComponentUid   *openapi_types.UUID `json:"componentUid,omitempty"`
		EnvironmentUid *openapi_types.UUID `json:"environmentUid,omitempty"`
		Name           *string             `json:"name,omitempty"`
		Namespace      *string             `json:"namespace,omitempty"`
		ProjectUid     *openapi_types.UUID `json:"projectUid,omitempty"`
	}{Name: &name}}
	if namespace != "" {
		resp.Metadata.Namespace = &namespace
	}
	return gen.GetAlertRule200JSONResponse(resp), nil
}

// UpdateAlertRule handles PUT /api/v1alpha1/alerts/rules/{ruleName}.
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
	if strings.TrimSpace(request.RuleName) != "" {
		in.RuleName = request.RuleName
	}

	res, err := h.alertClient.UpdateRule(ctx, in)
	if err != nil {
		if errors.Is(err, cloudmonitoring.ErrRuleNotFound) {
			return updateAlertRuleNotFoundResponse{makeError(gen.BadRequest, errCodeNotFound, "alert rule not found")}, nil
		}
		if errors.Is(err, cloudmonitoring.ErrValidation) {
			return gen.UpdateAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
		}
		h.logger.Error("update alert rule failed",
			slog.String("ruleName", in.RuleName), slog.String("namespace", in.Namespace), slog.Any("error", err))
		return gen.UpdateAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to update alert rule")), nil
	}
	return gen.UpdateAlertRule200JSONResponse(syncResponse(res, gen.Updated, gen.Synced)), nil
}

// DeleteAlertRule handles DELETE /api/v1alpha1/alerts/rules/{ruleName}.
func (h *MetricsHandler) DeleteAlertRule(ctx context.Context, request gen.DeleteAlertRuleRequestObject) (gen.DeleteAlertRuleResponseObject, error) {
	if !h.alertingEnabled() {
		return gen.DeleteAlertRule500JSONResponse(notImplementedErr()), nil
	}
	if strings.TrimSpace(request.RuleName) == "" {
		return gen.DeleteAlertRule400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "ruleName is required")), nil
	}
	res, err := h.alertClient.DeleteRule(ctx, request.RuleName)
	if err != nil {
		if errors.Is(err, cloudmonitoring.ErrRuleNotFound) {
			return gen.DeleteAlertRule404JSONResponse(makeError(gen.BadRequest, errCodeNotFound, "alert rule not found")), nil
		}
		h.logger.Error("delete alert rule failed", slog.String("ruleName", request.RuleName), slog.Any("error", err))
		return gen.DeleteAlertRule500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to delete alert rule")), nil
	}
	return gen.DeleteAlertRule200JSONResponse(syncResponse(res, gen.Deleted, gen.Synced)), nil
}

// HandleAlertWebhook handles POST /api/v1alpha1/alerts/webhook.
//
// Cloud Monitoring's webhook notification channel POSTs here when an alert
// fires. The alert is forwarded to the Observer synchronously: a forwarding
// failure returns 500 so Cloud Monitoring redelivers the notification, while
// parse failures return 400 (a redelivery cannot fix a malformed payload).
// Resolved (non-firing) incidents are acknowledged and skipped.
func (h *MetricsHandler) HandleAlertWebhook(ctx context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	if h.observerClient == nil {
		return gen.HandleAlertWebhook500JSONResponse(notImplementedErr()), nil
	}
	if request.Body == nil {
		return gen.HandleAlertWebhook400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "request body is required")), nil
	}
	raw, err := json.Marshal(*request.Body)
	if err != nil {
		return gen.HandleAlertWebhook400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, "failed to re-encode webhook body")), nil
	}
	details, err := webhook.Parse(raw)
	if err != nil {
		h.logger.Warn("webhook parse failed", slog.Any("error", err))
		return gen.HandleAlertWebhook400JSONResponse(makeError(gen.BadRequest, errCodeBadRequest, err.Error())), nil
	}
	status := gen.Success
	if !details.IsFiring() {
		h.logger.Debug("ignoring non-firing incident",
			slog.String("ruleName", details.RuleName), slog.String("state", details.State))
		return gen.HandleAlertWebhook200JSONResponse{Status: &status, Message: strPtr("non-firing incident ignored")}, nil
	}

	if err := h.observerClient.ForwardAlert(ctx, details.RuleName, details.RuleNamespace, details.AlertValue, details.AlertTimestamp); err != nil {
		h.logger.Error("forward to observer failed",
			slog.String("ruleName", details.RuleName),
			slog.String("ruleNamespace", details.RuleNamespace),
			slog.Any("error", err),
		)
		return gen.HandleAlertWebhook500JSONResponse(makeError(gen.InternalServerError, errCodeInternal, "failed to forward alert to observer")), nil
	}
	h.logger.Info("forwarded alert to observer",
		slog.String("ruleName", details.RuleName),
		slog.String("ruleNamespace", details.RuleNamespace),
		slog.Float64("alertValue", details.AlertValue))
	return gen.HandleAlertWebhook200JSONResponse{Status: &status, Message: strPtr("alert forwarded to observer")}, nil
}

// ruleInputFromRequest maps the generated AlertRuleRequest to the backend
// RuleInput, validating the required identity fields.
func ruleInputFromRequest(req gen.AlertRuleRequest) (cloudmonitoring.RuleInput, error) {
	in := cloudmonitoring.RuleInput{
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
