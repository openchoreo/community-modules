// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-metrics-cloudwatch/internal/cloudwatchmetrics"
)

type stubObserver struct {
	forwarded chan forwardCall
	err       error
}

type forwardCall struct {
	ruleName      string
	ruleNamespace string
	alertValue    float64
	alertTime     time.Time
}

func (s *stubObserver) ForwardAlert(_ context.Context, ruleName, ruleNamespace string, alertValue float64, alertTimestamp time.Time) error {
	if s.forwarded != nil {
		s.forwarded <- forwardCall{
			ruleName:      ruleName,
			ruleNamespace: ruleNamespace,
			alertValue:    alertValue,
			alertTime:     alertTimestamp,
		}
	}
	return s.err
}

func TestHandleAlertWebhookReturnsOKOnNilBody(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	resp, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

func TestHandleAlertWebhookForwardsLambdaPayload(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	h := newTestHandler(&stubMetricsClient{}, observer)
	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-metrics-alert-x",
		"ruleName":       "high-cpu",
		"ruleNamespace":  "payments",
		"state":          "ALARM",
		"alertValue":     7.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}

	resp, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200 webhook response, got %T", resp)
	}

	select {
	case call := <-observer.forwarded:
		if call.ruleName != "high-cpu" || call.ruleNamespace != "payments" || call.alertValue != 7 {
			t.Fatalf("unexpected forwarded call: %#v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded alert")
	}
}

func TestHandleAlertWebhookIgnoresRecoveryWhenDisabled(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	h := NewMetricsHandler(&stubMetricsClient{}, HandlerOptions{ObserverClient: observer, ForwardRecovery: false}, discardLogger())
	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-metrics-alert-x",
		"ruleName":       "high-cpu",
		"ruleNamespace":  "payments",
		"state":          "OK",
		"alertValue":     0.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}

	if _, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		t.Fatalf("unexpected recovery forwarded: %#v", call)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHandleAlertWebhookForwardsRecoveryWhenEnabled(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	h := NewMetricsHandler(&stubMetricsClient{}, HandlerOptions{ObserverClient: observer, ForwardRecovery: true}, discardLogger())
	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-metrics-alert-x",
		"ruleName":       "rule-x",
		"ruleNamespace":  "ns-x",
		"state":          "OK",
		"alertValue":     0.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}
	if _, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		if call.ruleName != "rule-x" {
			t.Fatalf("unexpected forwarded call: %#v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected recovery to be forwarded")
	}
}

func TestHandleAlertWebhookEventBridgePayload(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	h := newTestHandler(&stubMetricsClient{}, observer)
	alarmName := cloudwatchmetrics.BuildAlarmName("payments", "high-cpu")
	body := gen.HandleAlertWebhookJSONRequestBody{
		"source": "aws.cloudwatch",
		"time":   "2026-04-23T10:00:00Z",
		"detail": map[string]any{
			"alarmName": alarmName,
			"state": map[string]any{
				"value":      "ALARM",
				"reason":     "Threshold Crossed",
				"reasonData": `{"recentDatapoints":[7]}`,
				"timestamp":  "2026-04-23T10:00:05Z",
			},
		},
	}
	resp, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}

	select {
	case call := <-observer.forwarded:
		// The alarm name parses to (payments, high-cpu) — used to recover identity.
		if call.ruleName != "high-cpu" || call.ruleNamespace != "payments" || call.alertValue != 7 {
			t.Fatalf("unexpected forwarded call: %#v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected EventBridge payload to be forwarded")
	}
}

func TestHandleAlertWebhookSNSSubscriptionConfirmIgnoredByDefault(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	body := gen.HandleAlertWebhookJSONRequestBody{
		"Type":           "SubscriptionConfirmation",
		"MessageId":      "msg-1",
		"Token":          "token-1",
		"TopicArn":       "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp":      "2026-04-23T10:00:00Z",
		"SubscribeURL":   "https://sns.eu-north-1.amazonaws.com/?Action=ConfirmSubscription",
		"Signature":      "abc",
		"SigningCertURL": "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
		"Message":        "You have chosen to subscribe",
	}
	resp, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

func TestHandleAlertWebhookSNSSubscriptionConfirmCanBeAllowed(t *testing.T) {
	// We can't actually verify the signature here (would need fetcher swap),
	// but we can at least drive the gate and confirm the response shape.
	h := NewMetricsHandler(&stubMetricsClient{}, HandlerOptions{
		SNSAllowSubscribeConfirm: true,
	}, discardLogger())

	body := gen.HandleAlertWebhookJSONRequestBody{
		"Type":           "SubscriptionConfirmation",
		"MessageId":      "msg-1",
		"Token":          "token-1",
		"TopicArn":       "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp":      "2026-04-23T10:00:00Z",
		"SubscribeURL":   "https://sns.eu-north-1.amazonaws.com/?Action=ConfirmSubscription",
		"Signature":      "abc",
		"SigningCertURL": "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
		"Message":        "You have chosen to subscribe",
	}
	resp, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

func TestHandleAlertWebhookSNSNotificationFallsBackToTagsForRuleIdentity(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	client := &stubMetricsClient{
		getAlarmTagsByName: func(_ context.Context, _ string) (map[string]string, error) {
			return map[string]string{
				cloudwatchmetrics.TagRuleSource:    cloudwatchmetrics.TagRuleSourceVal,
				cloudwatchmetrics.TagRuleName:      "tagged-rule",
				cloudwatchmetrics.TagRuleNamespace: "tagged-ns",
			}, nil
		},
	}
	h := NewMetricsHandler(client, HandlerOptions{ObserverClient: observer}, discardLogger())

	body := gen.HandleAlertWebhookJSONRequestBody{
		"Type":           "Notification",
		"MessageId":      "msg-1",
		"TopicArn":       "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp":      "2026-04-23T10:00:00Z",
		"Signature":      "abc",
		"SigningCertURL": "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
		"Message":        `{"AlarmName":"unmanaged-name","NewStateValue":"ALARM","StateChangeTime":"2026-04-23T10:00:05Z"}`,
	}
	if _, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		if call.ruleName != "tagged-rule" || call.ruleNamespace != "tagged-ns" {
			t.Fatalf("unexpected forwarded call: %#v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected forwarded call once tags are hydrated")
	}
}

func TestHandleAlertWebhookSkipsLogsModuleAlarms(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	client := &stubMetricsClient{
		getAlarmTagsByName: func(_ context.Context, _ string) (map[string]string, error) {
			return map[string]string{
				cloudwatchmetrics.TagRuleSource:    "logs",
				cloudwatchmetrics.TagRuleName:      "should-not-forward",
				cloudwatchmetrics.TagRuleNamespace: "logs-ns",
			}, nil
		},
	}
	h := NewMetricsHandler(client, HandlerOptions{ObserverClient: observer}, discardLogger())

	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "unmanaged-name",
		"state":          "ALARM",
		"alertValue":     5.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}
	if _, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		t.Fatalf("did not expect logs-source alarm to be forwarded, got %#v", call)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHandleAlertWebhookDropsWhenRuleNameUnknown(t *testing.T) {
	observer := &stubObserver{forwarded: make(chan forwardCall, 1)}
	client := &stubMetricsClient{
		getAlarmTagsByName: func(_ context.Context, _ string) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}
	h := NewMetricsHandler(client, HandlerOptions{ObserverClient: observer}, discardLogger())

	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "unmanaged-name",
		"state":          "ALARM",
		"alertValue":     1.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}
	if _, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body}); err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}

	select {
	case call := <-observer.forwarded:
		t.Fatalf("did not expect any forwarded event, got %#v", call)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHandleAlertWebhookSwallowsObserverError(t *testing.T) {
	observer := &stubObserver{
		forwarded: make(chan forwardCall, 1),
		err:       errors.New("observer down"),
	}
	h := newTestHandler(&stubMetricsClient{}, observer)
	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-metrics-alert-x",
		"ruleName":       "high-cpu",
		"ruleNamespace":  "payments",
		"state":          "ALARM",
		"alertValue":     7.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}
	resp, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	// Drain so the goroutine exits cleanly.
	<-observer.forwarded
}

func TestHandleAlertWebhookDropsWhenObserverNotConfigured(t *testing.T) {
	h := newTestHandler(&stubMetricsClient{}, nil)
	body := gen.HandleAlertWebhookJSONRequestBody{
		"alarmName":      "oc-metrics-alert-x",
		"ruleName":       "high-cpu",
		"ruleNamespace":  "payments",
		"state":          "ALARM",
		"alertValue":     7.0,
		"alertTimestamp": "2026-04-23T10:00:05Z",
	}
	resp, err := h.HandleAlertWebhook(context.Background(), gen.HandleAlertWebhookRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("HandleAlertWebhook() error = %v", err)
	}
	if _, ok := resp.(gen.HandleAlertWebhook200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
}

func TestConfirmSNSSubscriptionRejectsBadSignature(t *testing.T) {
	// Hit a server that, if reached, would fail the test — confirming that
	// the verifier short-circuits before any outbound call is made.
	subscribeHit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		subscribeHit <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	h := newTestHandler(&stubMetricsClient{}, nil)
	// Cert URL is rejected by validateSigningCertURL → verifier returns an
	// error → confirmSNSSubscription must NOT call SubscribeURL.
	h.confirmSNSSubscription(&cloudwatchmetrics.SNSEnvelopeResult{
		EnvelopeType:   "SubscriptionConfirmation",
		Signature:      "QUFB",
		SigningCertURL: "http://attacker.example.com/cert.pem",
		SubscribeURL:   srv.URL,
		Timestamp:      "2026-04-23T10:00:00Z",
		Token:          "token-1",
		TopicARN:       "arn:aws:sns:eu-north-1:123:alerts",
	})

	select {
	case <-subscribeHit:
		t.Fatal("verifier should have rejected envelope; SubscribeURL must not be called")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestParseWebhookBodyDispatchesByEnvelope(t *testing.T) {
	t.Run("eventbridge", func(t *testing.T) {
		raw := []byte(`{"source":"aws.cloudwatch","detail":{"alarmName":"x","state":{"value":"ALARM"}}}`)
		evt, confirm, err := parseWebhookBody(raw)
		if err != nil {
			t.Fatalf("parseWebhookBody() error = %v", err)
		}
		if confirm != nil || evt == nil || evt.State != "ALARM" {
			t.Fatalf("unexpected parse: evt=%+v confirm=%+v", evt, confirm)
		}
	})

	t.Run("lambda", func(t *testing.T) {
		raw := []byte(`{"alarmName":"a","ruleName":"r","ruleNamespace":"n","state":"ALARM","alertValue":3.5,"alertTimestamp":"2026-04-23T10:00:05Z"}`)
		evt, confirm, err := parseWebhookBody(raw)
		if err != nil {
			t.Fatalf("parseWebhookBody() error = %v", err)
		}
		if confirm != nil || evt == nil || evt.RuleName != "r" {
			t.Fatalf("unexpected parse: %+v", evt)
		}
	})

	t.Run("sns subscription", func(t *testing.T) {
		raw := []byte(`{"Type":"SubscriptionConfirmation","Token":"t","TopicArn":"arn","SubscribeURL":"https://sns.eu-north-1.amazonaws.com/x"}`)
		_, confirm, err := parseWebhookBody(raw)
		if err != nil {
			t.Fatalf("parseWebhookBody() error = %v", err)
		}
		if confirm == nil || !confirm.IsSubscriptionConfirm {
			t.Fatalf("expected subscription confirm: %+v", confirm)
		}
	})
}

