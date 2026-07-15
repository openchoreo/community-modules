// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	labelNamespace = "openchoreo_namespace"
	labelRuleName  = "openchoreo_rule_name"
)

type CloudMonitoringWebhook struct {
	Incident *Incident `json:"incident"`
	Version  string    `json:"version"`
}

type Incident struct {
	IncidentID       string            `json:"incident_id"`
	PolicyName       string            `json:"policy_name"`
	State            string            `json:"state"` // "open" | "closed"
	StartedAt        int64             `json:"started_at"`
	EndedAt          int64             `json:"ended_at"`
	PolicyUserLabels map[string]string `json:"policy_user_labels"`
	ObservedValue    string            `json:"observed_value"`
	Metric           struct {
		Type string `json:"type"`
	} `json:"metric"`
}

// AlertDetails is the parsed, backend-neutral view of a fired alert.
type AlertDetails struct {
	RuleNamespace  string
	RuleName       string
	AlertValue     float64
	AlertTimestamp time.Time
	State          string
}

// IsFiring reports whether the incident represents a firing (open) alert as
// opposed to a resolution. A missing state is treated as not firing so a
// malformed payload cannot produce a false alert.
func (d *AlertDetails) IsFiring() bool {
	return strings.EqualFold(d.State, "open")
}

// Parse decodes a Cloud Monitoring webhook body and extracts the OpenChoreo
// identity and fired value. It fails only when the payload is unparseable or
// the OpenChoreo identity labels are absent (i.e. not one of our policies).
func Parse(raw []byte) (*AlertDetails, error) {
	var payload CloudMonitoringWebhook
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode cloud monitoring webhook: %w", err)
	}
	if payload.Incident == nil {
		return nil, errors.New("webhook body has no incident")
	}
	inc := payload.Incident

	// Recover the OpenChoreo identity. The user-label values are the primary
	// source, BUT Cloud Monitoring caps user-label values at 63 chars, so a
	// long "<namespace>/<name>" pair can be truncated in
	// openchoreo_rule_name. The policy display name (incident.policy_name) is
	// set to the full, untruncated "<namespace>/<rule name>" and has no such
	// cap, so it is preferred when it parses cleanly.
	namespace, ruleName := identityFromPolicyName(inc.PolicyName)
	if namespace == "" {
		namespace = strings.TrimSpace(inc.PolicyUserLabels[labelNamespace])
	}
	if ruleName == "" {
		ruleName = strings.TrimSpace(inc.PolicyUserLabels[labelRuleName])
	}
	if namespace == "" || ruleName == "" {
		return nil, errors.New("OpenChoreo identity missing: incident lacks a parseable policy_name and policy_user_labels (openchoreo_namespace / openchoreo_rule_name)")
	}

	return &AlertDetails{
		RuleNamespace:  namespace,
		RuleName:       ruleName,
		AlertValue:     parseValue(inc.ObservedValue),
		AlertTimestamp: incidentTime(inc),
		State:          inc.State,
	}, nil
}

// identityFromPolicyName splits the alert policy display name — set by this
// adapter to "<namespace>/<rule name>" — into its two parts. The rule name may
// itself be empty and the namespace may contain no slash; in either malformed
// case it returns empty strings so the caller can fall back to user labels.
// Splitting on the FIRST slash is safe because OpenChoreo namespaces do not
// contain "/".
func identityFromPolicyName(policyName string) (namespace, ruleName string) {
	policyName = strings.TrimSpace(policyName)
	i := strings.Index(policyName, "/")
	if i <= 0 || i == len(policyName)-1 {
		return "", ""
	}
	return policyName[:i], policyName[i+1:]
}

// parseValue converts Cloud Monitoring's stringified observed value to a
// float. A blank or unparseable value yields 0.
func parseValue(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// incidentTime prefers started_at (Unix seconds) for a firing incident and
// ended_at for a resolution, falling back to now.
func incidentTime(inc *Incident) time.Time {
	switch {
	case strings.EqualFold(inc.State, "closed") && inc.EndedAt > 0:
		return time.Unix(inc.EndedAt, 0).UTC()
	case inc.StartedAt > 0:
		return time.Unix(inc.StartedAt, 0).UTC()
	default:
		return time.Now().UTC()
	}
}
