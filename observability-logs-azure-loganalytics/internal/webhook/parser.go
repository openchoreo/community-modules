// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package webhook parses Azure Monitor's Common Alert Schema and recovers
// the OpenChoreo identity from a fired alert payload.
//
// Schema reference:
// https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-common-schema
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
		AlertContext struct {
			SearchQuery   string `json:"SearchQuery,omitempty"`
			SearchResults struct {
				RowCount int `json:"rowCount,omitempty"`
			} `json:"SearchResults,omitempty"`
		} `json:"alertContext"`
		CustomProperties map[string]string `json:"customProperties,omitempty"`
	} `json:"data"`
}

// AlertDetails is the adapter's normalized form, ready to forward.
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
		// Fallback: parse essentials.alertRuleId. The docs warn this field
		// may be absent in some payloads, so it's a fallback and not the
		// primary source.
		if ns, name, ok := parseAlertRuleID(payload.Data.Essentials.AlertRuleID); ok && ns != "" && name != "" {
			namespace, ruleName = ns, name
		}
	}
	if namespace == "" || ruleName == "" {
		return nil, errors.New("OpenChoreo identity missing: neither customProperties nor alertRuleId yielded a (namespace, ruleName)")
	}

	ts := parseTime(payload.Data.Essentials.FiredDateTime)

	return &AlertDetails{
		RuleNamespace:  namespace,
		RuleName:       ruleName,
		Severity:       payload.Data.Essentials.Severity,
		AlertValue:     float64(payload.Data.AlertContext.SearchResults.RowCount),
		AlertTimestamp: ts,
		AlertRuleID:    payload.Data.Essentials.AlertRuleID,
		SearchQuery:    payload.Data.AlertContext.SearchQuery,
	}, nil
}

func identityFromCustomProperties(props map[string]string) (namespace, ruleName string) {
	if props == nil {
		return "", ""
	}
	return strings.TrimSpace(props[customPropNamespace]),
		strings.TrimSpace(props[customPropRuleName])
}

// parseAlertRuleID is a best-effort fallback when customProperties is absent.
// The ARM ID format is /subscriptions/<sub>/resourceGroups/<rg>/providers/microsoft.insights/scheduledQueryRules/<name>
// — the name encodes a hash, so we can't recover namespace/ruleName from it
// without an external map. Returns (rgName, ruleResourceName, true) so the
// caller can use them as a degraded identity rather than failing outright.
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
