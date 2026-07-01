// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// IncidentPayload mirrors the fields the adapter needs from a Cloud Monitoring
// webhook notification (schema version 1.2). Only stable structured fields are
// read; the free-text summary/url fields are intentionally ignored because GCP
// documents that their format can change without notice.
//
// observed_value and threshold_value arrive as STRINGS in the payload, not
// numbers — hence the string typing and explicit parse.
type IncidentPayload struct {
	Incident struct {
		IncidentID    string `json:"incident_id"`
		PolicyName    string `json:"policy_name"`
		ConditionName string `json:"condition_name"`
		State         string `json:"state"`      // "open" | "closed"
		StartedAt     int64  `json:"started_at"` // Unix epoch seconds
		EndedAt       int64  `json:"ended_at"`
		ObservedValue string `json:"observed_value"`
		// PolicyUserLabels carries the alert policy's user_labels — this is
		// where openchoreo-namespace / openchoreo-rule-name land.
		PolicyUserLabels map[string]string `json:"policy_user_labels"`
	} `json:"incident"`
	Version string `json:"version"`
}

// AlertDetails is the normalized view forwarded to the Observer.
type AlertDetails struct {
	RuleNamespace  string
	RuleName       string
	State          string
	AlertValue     float64
	AlertTimestamp time.Time
}

// ParseWebhook decodes a Cloud Monitoring incident notification and recovers
// the OpenChoreo identity from incident.policy_user_labels. Returns an error
// if the body is unparseable or the identity labels are absent.
func ParseWebhook(raw []byte) (*AlertDetails, error) {
	var p IncidentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decode incident payload: %w", err)
	}

	labels := p.Incident.PolicyUserLabels
	namespace := strings.TrimSpace(labels[UserLabelNamespace])
	ruleName := strings.TrimSpace(labels[UserLabelRuleName])
	if namespace == "" || ruleName == "" {
		return nil, errors.New("OpenChoreo identity missing: incident.policy_user_labels lacked openchoreo-namespace/openchoreo-rule-name")
	}

	return &AlertDetails{
		RuleNamespace:  namespace,
		RuleName:       ruleName,
		State:          p.Incident.State,
		AlertValue:     parseFloat(p.Incident.ObservedValue),
		AlertTimestamp: parseEpoch(p.Incident.StartedAt),
	}, nil
}

// parseFloat parses the string-typed observed_value; returns 0 on any failure.
func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

// parseEpoch converts Unix epoch seconds to a UTC time; falls back to now.
func parseEpoch(sec int64) time.Time {
	if sec <= 0 {
		return time.Now().UTC()
	}
	return time.Unix(sec, 0).UTC()
}
