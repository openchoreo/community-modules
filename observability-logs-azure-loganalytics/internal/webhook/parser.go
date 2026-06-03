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

// CommonAlertSchema mirrors only the fields the adapter needs. Anything
// the adapter does not consume is intentionally elided.
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

// AlertContext covers both shapes Azure Monitor emits for log alerts:
//   - V2 (scheduledQueryRules API 2021-08-01 and later) puts the fired
//     conditions under condition.allOf[].
//   - V1 (legacy scheduledQueryRules API 2018-04-16) puts SearchQuery and
//     SearchResults at the top of alertContext.
type AlertContext struct {
	Condition struct {
		AllOf []AlertCondition `json:"allOf,omitempty"`
	} `json:"condition,omitempty"`

	SearchQuery   string `json:"SearchQuery,omitempty"`
	SearchResults struct {
		RowCount int `json:"rowCount,omitempty"`
	} `json:"SearchResults,omitempty"`
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

// Parse turns a webhook body into AlertDetails. Returns an error if the
// payload is unparseable, the schemaId is wrong, or the OpenChoreo identity
// cannot be recovered from either customProperties or essentials.alertRuleId.
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

// extractValueAndQuery prefers the V2 condition.allOf[] shape emitted by the
// v2 scheduledQueryRules API and falls back to the legacy V1
// SearchQuery / SearchResults.rowCount fields when V2 is absent.
func extractValueAndQuery(ctx AlertContext) (float64, string) {
	for _, c := range ctx.Condition.AllOf {
		if c.SearchQuery != "" || c.MetricValue != 0 {
			return c.MetricValue, c.SearchQuery
		}
	}
	return float64(ctx.SearchResults.RowCount), ctx.SearchQuery
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
