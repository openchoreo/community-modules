// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ParsedAlertEvent is the normalised form of every supported webhook payload.
type ParsedAlertEvent struct {
	RuleName       string
	RuleNamespace  string
	AlertValue     float64
	AlertTimestamp time.Time
	State          string // ALARM | OK | INSUFFICIENT_DATA
	AlarmName      string
}

// SNSEnvelopeResult captures whether the incoming SNS request was a subscription
// confirmation or an alarm notification.
type SNSEnvelopeResult struct {
	Event                 *ParsedAlertEvent
	IsSubscriptionConfirm bool
	EnvelopeType          string
	SubscribeURL          string
	SigningCertURL        string
	Signature             string
	SignatureVersion      string
	MessageID             string
	TopicARN              string
	RawMessage            string
	Subject               string
	Timestamp             string
	Token                 string
}

// ParseSNSEnvelope parses an SNS envelope. Callers are expected to perform the
// signature verification themselves before following SubscribeURL.
func ParseSNSEnvelope(body []byte) (*SNSEnvelopeResult, error) {
	var env struct {
		Type             string `json:"Type"`
		MessageID        string `json:"MessageId"`
		TopicArn         string `json:"TopicArn"`
		Message          string `json:"Message"`
		Timestamp        string `json:"Timestamp"`
		Subject          string `json:"Subject"`
		Token            string `json:"Token"`
		SignatureVersion string `json:"SignatureVersion"`
		Signature        string `json:"Signature"`
		SigningCertURL   string `json:"SigningCertURL"`
		SubscribeURL     string `json:"SubscribeURL"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parse_sns_envelope: %w", err)
	}
	res := &SNSEnvelopeResult{
		EnvelopeType:     env.Type,
		MessageID:        env.MessageID,
		TopicARN:         env.TopicArn,
		SignatureVersion: env.SignatureVersion,
		Signature:        env.Signature,
		SigningCertURL:   env.SigningCertURL,
		SubscribeURL:     env.SubscribeURL,
		RawMessage:       env.Message,
		Subject:          env.Subject,
		Timestamp:        env.Timestamp,
		Token:            env.Token,
	}
	switch env.Type {
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		res.IsSubscriptionConfirm = true
		return res, nil
	case "Notification":
		evt, err := parseSNSAlarmMessage(env.Message, env.Timestamp)
		if err != nil {
			return res, err
		}
		res.Event = evt
		return res, nil
	default:
		return nil, fmt.Errorf("unsupported SNS envelope Type %q", env.Type)
	}
}

// parseSNSAlarmMessage handles the JSON body of a CloudWatch-alarm SNS notification.
func parseSNSAlarmMessage(message, envTimestamp string) (*ParsedAlertEvent, error) {
	var msg struct {
		AlarmName       string `json:"AlarmName"`
		AlarmDescription string `json:"AlarmDescription"`
		NewStateValue   string `json:"NewStateValue"`
		NewStateReason  string `json:"NewStateReason"`
		StateChangeTime string `json:"StateChangeTime"`
		Trigger         struct {
			Threshold float64 `json:"Threshold"`
		} `json:"Trigger"`
	}
	if err := json.Unmarshal([]byte(message), &msg); err != nil {
		return nil, fmt.Errorf("parse_sns_alarm_message: %w", err)
	}
	ts := parseTimestampOr(msg.StateChangeTime, envTimestamp)
	value := extractDatapointFromReason(msg.NewStateReason)
	evt := &ParsedAlertEvent{
		AlarmName:      msg.AlarmName,
		State:          msg.NewStateValue,
		AlertValue:     value,
		AlertTimestamp: ts,
	}
	if namespace, name, err := ParseAlertIdentityFromAlarmName(msg.AlarmName); err == nil {
		evt.RuleNamespace = namespace
		evt.RuleName = name
	}
	return evt, nil
}

// ParseEventBridgeEvent handles the payload shape delivered by an EventBridge
// rule that matches the `aws.cloudwatch` / `CloudWatch Alarm State Change` event.
func ParseEventBridgeEvent(body []byte) (*ParsedAlertEvent, error) {
	var evt struct {
		Time   string `json:"time"`
		Source string `json:"source"`
		Detail struct {
			AlarmName string `json:"alarmName"`
			State     struct {
				Value         string `json:"value"`
				Reason        string `json:"reason"`
				ReasonData    string `json:"reasonData"`
				Timestamp     string `json:"timestamp"`
			} `json:"state"`
			Configuration struct {
				Metrics []struct {
					MetricStat struct {
						Metric struct {
							Namespace string `json:"namespace"`
							Name      string `json:"name"`
						} `json:"metric"`
					} `json:"metricStat"`
				} `json:"metrics"`
			} `json:"configuration"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, fmt.Errorf("parse_eventbridge: %w", err)
	}
	if evt.Source != "" && evt.Source != "aws.cloudwatch" {
		return nil, fmt.Errorf("unexpected EventBridge source %q", evt.Source)
	}

	value := extractDatapointFromReasonData(evt.Detail.State.ReasonData)
	if value == 0 {
		value = extractDatapointFromReason(evt.Detail.State.Reason)
	}

	ts := parseTimestampOr(evt.Detail.State.Timestamp, evt.Time)
	parsed := &ParsedAlertEvent{
		AlarmName:      evt.Detail.AlarmName,
		State:          evt.Detail.State.Value,
		AlertValue:     value,
		AlertTimestamp: ts,
	}
	if namespace, name, err := ParseAlertIdentityFromAlarmName(evt.Detail.AlarmName); err == nil {
		parsed.RuleNamespace = namespace
		parsed.RuleName = name
	}
	return parsed, nil
}

// ParseLambdaForwarderEvent handles the reference Lambda forwarder payload.
// The Lambda flattens alarm state + OpenChoreo metadata into a single JSON doc
// so the webhook handler doesn't need a second AWS API call.
func ParseLambdaForwarderEvent(body []byte) (*ParsedAlertEvent, error) {
	var evt struct {
		AlarmName      string  `json:"alarmName"`
		RuleName       string  `json:"ruleName"`
		RuleNamespace  string  `json:"ruleNamespace"`
		State          string  `json:"state"`
		AlertValue     float64 `json:"alertValue"`
		AlertTimestamp string  `json:"alertTimestamp"`
	}
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, fmt.Errorf("parse_lambda_forwarder: %w", err)
	}
	if evt.AlarmName == "" && evt.RuleName == "" {
		return nil, errors.New("parse_lambda_forwarder: missing alarmName and ruleName")
	}
	ts := time.Now().UTC()
	if evt.AlertTimestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, evt.AlertTimestamp); err == nil {
			ts = parsed
		}
	}
	return &ParsedAlertEvent{
		RuleName:       evt.RuleName,
		RuleNamespace:  evt.RuleNamespace,
		AlarmName:      evt.AlarmName,
		State:          evt.State,
		AlertValue:     evt.AlertValue,
		AlertTimestamp: ts,
	}, nil
}

// ApplyTagsToEvent fills in the ruleName / ruleNamespace fields from the alarm
// tag map (as fetched by the caller via DescribeAlarms / ListTagsForResource).
func ApplyTagsToEvent(evt *ParsedAlertEvent, tags map[string]string) {
	if evt == nil {
		return
	}
	if v, ok := tags[TagRuleName]; ok && v != "" {
		evt.RuleName = v
	}
	if v, ok := tags[TagRuleNamespace]; ok && v != "" {
		evt.RuleNamespace = v
	}
}

// extractDatapointFromReason scrapes the textual `recent datapoints` block of
// a CloudWatch alarm reason string. Best-effort.
func extractDatapointFromReason(reason string) float64 {
	// Example: "Threshold Crossed: 1 datapoint [5.0 (22/04/26 14:00:00)] was greater than the threshold (3.0)."
	start := strings.Index(reason, "[")
	end := strings.Index(reason, "]")
	if start == -1 || end == -1 || end <= start+1 {
		return 0
	}
	body := reason[start+1 : end]
	var v float64
	if _, err := fmt.Sscanf(body, "%f", &v); err == nil {
		return v
	}
	return 0
}

// extractDatapointFromReasonData parses the JSON `reasonData` emitted by
// CloudWatch Alarms State Change events.
func extractDatapointFromReasonData(reasonData string) float64 {
	if strings.TrimSpace(reasonData) == "" {
		return 0
	}
	var rd struct {
		RecentDatapoints []float64 `json:"recentDatapoints"`
	}
	if err := json.Unmarshal([]byte(reasonData), &rd); err != nil {
		return 0
	}
	if len(rd.RecentDatapoints) == 0 {
		return 0
	}
	return rd.RecentDatapoints[len(rd.RecentDatapoints)-1]
}

func parseTimestampOr(primary, fallback string) time.Time {
	for _, s := range []string{primary, fallback} {
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02T15:04:05.000-0700", s); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}
