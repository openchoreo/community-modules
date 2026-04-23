package cloudwatch

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseSNSEnvelopeNotification(t *testing.T) {
	names := BuildAlertResourceNames("payments", "high-error-rate")
	body := []byte(`{
		"Type": "Notification",
		"MessageId": "msg-1",
		"TopicArn": "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp": "2026-04-23T10:00:00Z",
		"Message": "{\"AlarmName\":\"` + names.AlarmName + `\",\"NewStateValue\":\"ALARM\",\"NewStateReason\":\"Threshold Crossed: 1 datapoint [5.0 (23/04/26 10:00:00)] was greater than the threshold (3.0).\",\"StateChangeTime\":\"2026-04-23T10:00:05Z\"}"
	}`)

	res, err := ParseSNSEnvelope(body)
	if err != nil {
		t.Fatalf("ParseSNSEnvelope() error = %v", err)
	}
	if res.IsSubscriptionConfirm {
		t.Fatal("expected notification, not subscription confirmation")
	}
	if res.Event == nil || res.Event.State != "ALARM" || res.Event.AlertValue != 5 {
		t.Fatalf("unexpected parsed event: %#v", res.Event)
	}
	if res.Event.RuleName != "high-error-rate" || res.Event.RuleNamespace != "payments" {
		t.Fatalf("expected SNS parser to recover rule identity from alarm name: %#v", res.Event)
	}
	if !res.Event.AlertTimestamp.Equal(time.Date(2026, 4, 23, 10, 0, 5, 0, time.UTC)) {
		t.Fatalf("unexpected timestamp: %s", res.Event.AlertTimestamp)
	}
}

func TestParseSNSEnvelopeSubscriptionConfirmation(t *testing.T) {
	body := []byte(`{
		"Type": "SubscriptionConfirmation",
		"MessageId": "msg-1",
		"TopicArn": "arn:aws:sns:eu-north-1:123456789012:alerts",
		"Timestamp": "2026-04-23T10:00:00Z",
		"Message": "You have chosen to subscribe",
		"Token": "token-1",
		"SignatureVersion": "1",
		"Signature": "abc",
		"SigningCertURL": "https://sns.eu-north-1.amazonaws.com/SimpleNotificationService.pem",
		"SubscribeURL": "https://sns.eu-north-1.amazonaws.com/?Action=ConfirmSubscription"
	}`)

	res, err := ParseSNSEnvelope(body)
	if err != nil {
		t.Fatalf("ParseSNSEnvelope() error = %v", err)
	}
	if !res.IsSubscriptionConfirm {
		t.Fatal("expected subscription confirmation")
	}
	if res.SubscribeURL == "" || res.Token == "" {
		t.Fatalf("expected subscription fields to be preserved: %#v", res)
	}
}

func TestParseEventBridgeEvent(t *testing.T) {
	names := BuildAlertResourceNames("payments", "high-error-rate")
	body := []byte(`{
		"source": "aws.cloudwatch",
		"time": "2026-04-23T10:00:00Z",
		"detail": {
			"alarmName": "` + names.AlarmName + `",
			"state": {
				"value": "ALARM",
				"reason": "Threshold Crossed",
				"reasonData": "{\"recentDatapoints\":[2.0,4.0,6.0]}",
				"timestamp": "2026-04-23T10:00:05Z"
			}
		}
	}`)

	evt, err := ParseEventBridgeEvent(body)
	if err != nil {
		t.Fatalf("ParseEventBridgeEvent() error = %v", err)
	}
	if evt.State != "ALARM" || evt.AlertValue != 6 {
		t.Fatalf("unexpected event: %#v", evt)
	}
	if evt.RuleName != "high-error-rate" || evt.RuleNamespace != "payments" {
		t.Fatalf("expected EventBridge parser to recover rule identity from alarm name: %#v", evt)
	}
}

func TestParseLambdaForwarderEvent(t *testing.T) {
	body := []byte(`{
		"alarmName": "oc-logs-alert-123",
		"ruleName": "high-error-rate",
		"ruleNamespace": "payments",
		"state": "ALARM",
		"alertValue": 4,
		"alertTimestamp": "2026-04-23T10:00:05Z"
	}`)

	evt, err := ParseLambdaForwarderEvent(body)
	if err != nil {
		t.Fatalf("ParseLambdaForwarderEvent() error = %v", err)
	}
	if evt.RuleName != "high-error-rate" || evt.RuleNamespace != "payments" {
		t.Fatalf("unexpected event identity: %#v", evt)
	}
}

func TestApplyTagsToEvent(t *testing.T) {
	evt := &ParsedAlertEvent{AlarmName: "oc-logs-alert-123"}
	ApplyTagsToEvent(evt, map[string]string{
		TagRuleName:      "high-error-rate",
		TagRuleNamespace: "payments",
	})

	if evt.RuleName != "high-error-rate" || evt.RuleNamespace != "payments" {
		t.Fatalf("unexpected event after tags: %#v", evt)
	}
}

func TestExtractDatapointFromReasonDataHandlesInvalidJSON(t *testing.T) {
	if got := extractDatapointFromReasonData("{"); got != 0 {
		t.Fatalf("extractDatapointFromReasonData() = %v, want 0", got)
	}
}

func TestParseEventBridgeEventRejectsUnexpectedSource(t *testing.T) {
	payload := map[string]any{
		"source": "aws.ec2",
		"detail": map[string]any{},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if _, err := ParseEventBridgeEvent(body); err == nil {
		t.Fatal("expected unexpected source error")
	}
}
