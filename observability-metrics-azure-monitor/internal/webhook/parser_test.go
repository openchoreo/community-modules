// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"testing"
)

const samplePayload = `{
  "schemaId": "azureMonitorCommonAlertSchema",
  "data": {
    "essentials": {
      "alertId": "/subscriptions/sub/providers/microsoft.alertsmanagement/alerts/abcd",
      "alertRule": "oc-9f86d081884c7d65",
      "alertRuleId": "/subscriptions/sub/resourceGroups/rg/providers/microsoft.insights/scheduledQueryRules/oc-9f86d081884c7d65",
      "severity": "Sev3",
      "signalType": "Log",
      "monitorCondition": "Fired",
      "firedDateTime": "2026-05-28T05:30:00.0000000Z"
    },
    "alertContext": {
      "condition": {
        "allOf": [
          {
            "searchQuery": "Perf | take 1",
            "metricValue": 42
          }
        ]
      }
    },
    "customProperties": {
      "openchoreo-namespace": "default",
      "openchoreo-rule-name": "high-error-rate"
    }
  }
}`

func TestParse_HappyPath(t *testing.T) {
	got, err := Parse([]byte(samplePayload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RuleNamespace != "default" {
		t.Errorf("namespace: want default, got %q", got.RuleNamespace)
	}
	if got.RuleName != "high-error-rate" {
		t.Errorf("ruleName: want high-error-rate, got %q", got.RuleName)
	}
	if got.AlertValue != 42 {
		t.Errorf("alertValue: want 42, got %v", got.AlertValue)
	}
	if got.Severity != "Sev3" {
		t.Errorf("severity: want Sev3, got %q", got.Severity)
	}
	if got.AlertTimestamp.IsZero() {
		t.Errorf("alertTimestamp not parsed")
	}
}

func TestParse_V2ConditionAllOf(t *testing.T) {
	const v2Payload = `{
  "schemaId": "azureMonitorCommonAlertSchema",
  "data": {
    "essentials": {
      "alertRuleId": "/subscriptions/sub/resourceGroups/rg/providers/microsoft.insights/scheduledQueryRules/oc-9f86d081884c7d65",
      "severity": "Sev3",
      "signalType": "Log",
      "monitorCondition": "Fired",
      "firedDateTime": "2026-05-28T05:30:00Z"
    },
    "alertContext": {
      "condition": {
        "allOf": [
          {
            "searchQuery": "ContainerLogV2 | where LogMessage contains \"rpc error\" | count",
            "metricValue": 552
          }
        ]
      }
    },
    "customProperties": {
      "openchoreo-namespace": "default",
      "openchoreo-rule-name": "high-error-rate"
    }
  }
}`
	got, err := Parse([]byte(v2Payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AlertValue != 552 {
		t.Errorf("alertValue: want 552 (from condition.allOf[0].metricValue), got %v", got.AlertValue)
	}
	if got.SearchQuery == "" {
		t.Errorf("searchQuery should be populated from condition.allOf[0].searchQuery, got empty")
	}
}

func TestParse_RejectsWrongSchemaID(t *testing.T) {
	const bad = `{"schemaId":"somethingElse","data":{}}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected schemaId error")
	}
}

func TestParse_RejectsInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte(`not json`)); err == nil {
		t.Fatal("expected json error")
	}
}

func TestParse_DoesNotFabricateIdentityFromAlertRuleID(t *testing.T) {
	// customProperties absent but alertRuleId present. The parser must NOT
	// derive the identity from the ARM ID (resource group + hashed name are not
	// the OpenChoreo namespace/ruleName) — it must fail instead.
	const noCustomProps = `{
		"schemaId": "azureMonitorCommonAlertSchema",
		"data": {
			"essentials": {
				"alertRuleId": "/subscriptions/sub/resourceGroups/rg-x/providers/microsoft.insights/scheduledQueryRules/oc-abc123",
				"firedDateTime": "2026-05-28T05:30:00Z"
			},
			"alertContext": {}
		}
	}`
	if _, err := Parse([]byte(noCustomProps)); err == nil {
		t.Fatal("expected error when customProperties are missing (must not fabricate identity from alertRuleId)")
	}
}

func TestParse_FailsWhenIdentityUnrecoverable(t *testing.T) {
	const noIdentity = `{
		"schemaId": "azureMonitorCommonAlertSchema",
		"data": { "essentials": {}, "alertContext": {} }
	}`
	if _, err := Parse([]byte(noIdentity)); err == nil {
		t.Fatal("expected identity error")
	}
}
