// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/cloudwatchmetrics"
)

// HandleAlertWebhook receives an SNS / EventBridge / Lambda forwarder payload
// and forwards firing alarms to the OpenChoreo Observer.
func (h *MetricsHandler) HandleAlertWebhook(ctx context.Context, request gen.HandleAlertWebhookRequestObject) (gen.HandleAlertWebhookResponseObject, error) {
	successStatus := gen.Success
	ackResponse := gen.HandleAlertWebhook200JSONResponse{
		Message: strPtr("alert webhook received successfully"),
		Status:  &successStatus,
	}

	if request.Body == nil {
		h.logger.Warn("Alert webhook received with nil body")
		return ackResponse, nil
	}
	raw, err := json.Marshal(*request.Body)
	if err != nil {
		h.logger.Warn("Failed to re-marshal webhook body", slog.Any("error", err))
		return ackResponse, nil
	}

	event, confirm, err := parseWebhookBody(raw)
	if err != nil {
		h.logger.Warn("Failed to parse webhook body", slog.Any("error", err))
		return ackResponse, nil
	}

	if confirm != nil {
		if !h.snsAllowSubscribeConfirm {
			h.logger.Warn("SNS subscription confirmation received but SNS_ALLOW_SUBSCRIBE_CONFIRM=false; ignoring",
				slog.String("topicArn", confirm.TopicARN),
			)
			return gen.HandleAlertWebhook200JSONResponse{
				Message: strPtr("subscription confirmation ignored"),
				Status:  &successStatus,
			}, nil
		}
		if confirm.SubscribeURL != "" {
			go h.confirmSNSSubscription(confirm)
		}
		return gen.HandleAlertWebhook200JSONResponse{
			Message: strPtr("subscription confirmation received"),
			Status:  &successStatus,
		}, nil
	}

	if event == nil {
		return ackResponse, nil
	}
	if event.State != "" && event.State != "ALARM" && !h.forwardRecovery {
		h.logger.Debug("Ignoring non-ALARM webhook transition",
			slog.String("state", event.State),
			slog.String("alarmName", event.AlarmName),
		)
		return ackResponse, nil
	}
	if h.observerClient == nil {
		h.logger.Debug("Observer not configured; dropping alert webhook",
			slog.String("alarmName", event.AlarmName),
		)
		return ackResponse, nil
	}

	go h.forwardAlertEvent(event)
	return ackResponse, nil
}

func (h *MetricsHandler) forwardAlertEvent(event *cloudwatchmetrics.ParsedAlertEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Recover ruleName / namespace from the deterministic alarm name. If the
	// parse fails (alarm wasn't created by this module, or uses an older
	// scheme), fall back to a tag lookup so we can still source-filter and
	// recover identity for non-metrics alarms.
	if event.AlarmName != "" {
		if ns, name, perr := cloudwatchmetrics.ParseAlertIdentityFromAlarmName(event.AlarmName); perr == nil {
			if event.RuleName == "" {
				event.RuleName = name
			}
			if event.RuleNamespace == "" {
				event.RuleNamespace = ns
			}
		} else if tags, err := h.client.GetAlarmTagsByName(ctx, event.AlarmName); err == nil {
			if src := tags[cloudwatchmetrics.TagRuleSource]; src != "" && src != cloudwatchmetrics.TagRuleSourceVal {
				h.logger.Debug("Skipping non-metrics alarm",
					slog.String("alarmName", event.AlarmName),
					slog.String("source", src),
				)
				return
			}
			cloudwatchmetrics.ApplyTagsToEvent(event, tags)
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

func (h *MetricsHandler) confirmSNSSubscription(env *cloudwatchmetrics.SNSEnvelopeResult) {
	if err := cloudwatchmetrics.VerifySNSMessageSignature(env); err != nil {
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
		h.logger.Warn("SNS SubscribeURL returned non-2xx", slog.Int("statusCode", resp.StatusCode))
		return
	}
	h.logger.Info("Confirmed SNS subscription", slog.String("topicArn", env.TopicARN))
}

// parseWebhookBody picks the right parser based on envelope shape.
func parseWebhookBody(raw []byte) (*cloudwatchmetrics.ParsedAlertEvent, *cloudwatchmetrics.SNSEnvelopeResult, error) {
	var envShape struct {
		Type    string          `json:"Type"`
		Source  string          `json:"source"`
		Detail  json.RawMessage `json:"detail"`
		Message json.RawMessage `json:"Message"`
	}
	_ = json.Unmarshal(raw, &envShape)

	switch {
	case envShape.Type != "":
		res, err := cloudwatchmetrics.ParseSNSEnvelope(raw)
		if err != nil {
			return nil, nil, err
		}
		if res.IsSubscriptionConfirm {
			return nil, res, nil
		}
		return res.Event, nil, nil
	case envShape.Source == "aws.cloudwatch" || len(envShape.Detail) > 0:
		evt, err := cloudwatchmetrics.ParseEventBridgeEvent(raw)
		return evt, nil, err
	default:
		evt, err := cloudwatchmetrics.ParseLambdaForwarderEvent(raw)
		return evt, nil, err
	}
}
