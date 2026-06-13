// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	expectedSchemaID = "azureMonitorCommonAlertSchema"

	customPropNamespace = "openchoreo-namespace"
	customPropRuleName  = "openchoreo-rule-name"
)

type CommonAlertSchema struct {
	SchemaID string `json:"schemaId"`
	Data     struct {
		Essentials struct {
			AlertID          string `json:"alertId"`
			AlertRule        string `json:"alertRule"`
			AlertRuleID      string `json:"alertRuleId,omitempty"`
			Severity         string `json:"severity"`
			SignalType       string `json:"signalType"`
			MonitorCondition string `json:"monitorCondition"`
			FiredDateTime    string `json:"firedDateTime"`
			ResolvedDateTime string `json:"resolvedDateTime,omitempty"`
		} `json:"essentials"`
		AlertContext     AlertContext      `json:"alertContext"`
		CustomProperties map[string]string `json:"customProperties,omitempty"`
	} `json:"data"`
}

type AlertContext struct {
	Condition struct {
		AllOf []AlertCondition `json:"allOf,omitempty"`
	} `json:"condition,omitempty"`
}
type AlertCondition struct {
	SearchQuery string  `json:"searchQuery,omitempty"`
	MetricValue float64 `json:"metricValue,omitempty"`
}
type AlertDetails struct {
	RuleNamespace  string
	RuleName       string
	Severity       string
	AlertValue     float64
	AlertTimestamp time.Time
	AlertRuleID    string
	SearchQuery    string
}

func Parse(raw []byte) (*AlertDetails, error) {
	var payload CommonAlertSchema
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode common alert schema: %w", err)
	}
	if payload.SchemaID != expectedSchemaID {
		return nil, fmt.Errorf("unexpected schemaId %q (want %q)", payload.SchemaID, expectedSchemaID)
	}

	namespace, ruleName := identityFromCustomProperties(payload.Data.CustomProperties)
	if namespace == "" || ruleName == "" {
		if ns, name, ok := parseAlertRuleID(payload.Data.Essentials.AlertRuleID); ok && ns != "" && name != "" {
			namespace, ruleName = ns, name
		}
	}
	if namespace == "" || ruleName == "" {
		return nil, errors.New("OpenChoreo identity missing: neither customProperties nor alertRuleId yielded a (namespace, ruleName)")
	}

	ts := parseTime(payload.Data.Essentials.FiredDateTime)
	alertValue, searchQuery := extractValueAndQuery(payload.Data.AlertContext)

	return &AlertDetails{
		RuleNamespace:  namespace,
		RuleName:       ruleName,
		Severity:       payload.Data.Essentials.Severity,
		AlertValue:     alertValue,
		AlertTimestamp: ts,
		AlertRuleID:    payload.Data.Essentials.AlertRuleID,
		SearchQuery:    searchQuery,
	}, nil
}

func extractValueAndQuery(ctx AlertContext) (float64, string) {
	for _, c := range ctx.Condition.AllOf {
		if c.SearchQuery != "" || c.MetricValue != 0 {
			return c.MetricValue, c.SearchQuery
		}
	}
	return 0, ""
}

func identityFromCustomProperties(props map[string]string) (namespace, ruleName string) {
	if props == nil {
		return "", ""
	}
	return strings.TrimSpace(props[customPropNamespace]),
		strings.TrimSpace(props[customPropRuleName])
}

func parseAlertRuleID(armID string) (rg, name string, ok bool) {
	if armID == "" {
		return "", "", false
	}
	parts := strings.Split(armID, "/")
	for i := 0; i+1 < len(parts); i++ {
		switch strings.ToLower(parts[i]) {
		case "resourcegroups":
			rg = parts[i+1]
		case "scheduledqueryrules":
			name = parts[i+1]
		}
	}
	return rg, name, rg != "" && name != ""
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	// Azure typically emits RFC3339 / ISO 8601 with sub-second precision.
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}
