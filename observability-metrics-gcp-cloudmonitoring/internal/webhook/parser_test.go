// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"testing"
	"time"
)

const firingBody = `{
  "version": "1.2",
  "incident": {
    "incident_id": "abc123",
    "policy_name": "default/high-cpu",
    "state": "open",
    "started_at": 1751961600,
    "observed_value": "0.873",
    "metric": {"type": "kubernetes.io/container/cpu/core_usage_time"},
    "policy_user_labels": {
      "managed_by": "openchoreo",
      "openchoreo_namespace": "default",
      "openchoreo_rule_name": "high-cpu"
    }
  }
}`

func TestParseFiring(t *testing.T) {
	d, err := Parse([]byte(firingBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.RuleNamespace != "default" || d.RuleName != "high-cpu" {
		t.Errorf("identity = %q/%q", d.RuleNamespace, d.RuleName)
	}
	if d.AlertValue != 0.873 {
		t.Errorf("value = %v", d.AlertValue)
	}
	if !d.IsFiring() {
		t.Errorf("expected firing")
	}
	if !d.AlertTimestamp.Equal(time.Unix(1751961600, 0).UTC()) {
		t.Errorf("timestamp = %v", d.AlertTimestamp)
	}
}

func TestParseResolved(t *testing.T) {
	body := `{"incident":{"state":"closed","ended_at":1751965200,"observed_value":"0.1",
	  "policy_user_labels":{"openchoreo_namespace":"ns","openchoreo_rule_name":"r"}}}`
	d, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.IsFiring() {
		t.Errorf("closed incident should not be firing")
	}
	if !d.AlertTimestamp.Equal(time.Unix(1751965200, 0).UTC()) {
		t.Errorf("timestamp = %v, want ended_at", d.AlertTimestamp)
	}
}

func TestParseMissingStateNotFiring(t *testing.T) {
	body := `{"incident":{"observed_value":"0.9",
	  "policy_user_labels":{"openchoreo_namespace":"ns","openchoreo_rule_name":"r"}}}`
	d, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.IsFiring() {
		t.Errorf("incident without a state must not be treated as firing")
	}
}

func TestParsePrefersPolicyNameOverTruncatedLabel(t *testing.T) {
	// The user-label value is truncated at 63 chars, but policy_name carries
	// the full "<namespace>/<rule name>". The parser must use policy_name.
	full := "recommendation-development-70c09f59-recommendation-high-cpu-alert"
	truncated := full[:29] // simulate a 63-char cap chopping the tail
	body := `{"incident":{"state":"open","observed_value":"1.08",
	  "policy_name":"dp-default-gcp-microserv-development-4b8b4fdc/` + full + `",
	  "policy_user_labels":{"openchoreo_namespace":"dp-default-gcp-microserv-development-4b8b4fdc","openchoreo_rule_name":"` + truncated + `"}}}`
	d, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.RuleName != full {
		t.Errorf("ruleName = %q, want full %q", d.RuleName, full)
	}
	if d.RuleNamespace != "dp-default-gcp-microserv-development-4b8b4fdc" {
		t.Errorf("namespace = %q", d.RuleNamespace)
	}
}

func TestParseFallsBackToLabelsWhenNoPolicyName(t *testing.T) {
	body := `{"incident":{"state":"open","observed_value":"1.0",
	  "policy_user_labels":{"openchoreo_namespace":"ns","openchoreo_rule_name":"r"}}}`
	d, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.RuleNamespace != "ns" || d.RuleName != "r" {
		t.Errorf("identity = %q/%q, want ns/r", d.RuleNamespace, d.RuleName)
	}
}

func TestParseMissingIdentity(t *testing.T) {
	body := `{"incident":{"state":"open","policy_user_labels":{"foo":"bar"}}}`
	if _, err := Parse([]byte(body)); err == nil {
		t.Errorf("expected error when identity labels absent")
	}
}

func TestParseNoIncident(t *testing.T) {
	if _, err := Parse([]byte(`{"version":"1.2"}`)); err == nil {
		t.Errorf("expected error when incident missing")
	}
}

func TestParseMalformedJSON(t *testing.T) {
	if _, err := Parse([]byte(`{not json`)); err == nil {
		t.Errorf("expected decode error")
	}
}

func TestParseBlankValueDefaultsZero(t *testing.T) {
	body := `{"incident":{"state":"open","observed_value":"",
	  "policy_user_labels":{"openchoreo_namespace":"ns","openchoreo_rule_name":"r"}}}`
	d, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.AlertValue != 0 {
		t.Errorf("value = %v, want 0", d.AlertValue)
	}
}
