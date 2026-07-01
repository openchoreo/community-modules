// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"strings"
	"testing"
	"time"
)

func TestBuildAlertFilter_ScopeAndPhrase(t *testing.T) {
	defer func(prev bool) { SanitizePodLabelDots = prev }(SanitizePodLabelDots)
	SanitizePodLabelDots = true

	f := BuildAlertFilter(RuleInput{
		Namespace:    "default",
		ComponentUID: "c-uid",
		Query:        "panic",
	})
	for _, want := range []string{
		`resource.type="k8s_container"`,
		`labels."k8s-pod/openchoreo_dev/namespace"="default"`,
		`labels."k8s-pod/openchoreo_dev/component-uid"="c-uid"`,
		`(textPayload:"panic" OR jsonPayload.message:"panic")`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("filter missing %q\nfilter:\n%s", want, f)
		}
	}
}

func TestBuildAlertFilter_RawFilterPassthrough(t *testing.T) {
	raw := `resource.type="k8s_container" AND severity>=ERROR`
	if got := BuildAlertFilter(RuleInput{Query: raw}); got != raw {
		t.Errorf("raw filter should pass through verbatim; got %q", got)
	}
}

func TestBuildAlertFilter_DropsZeroUUID(t *testing.T) {
	f := BuildAlertFilter(RuleInput{
		Namespace:    "default",
		ComponentUID: "00000000-0000-0000-0000-000000000000",
	})
	if strings.Contains(f, "component-uid") {
		t.Errorf("zero UUID should be dropped; got:\n%s", f)
	}
}

func TestParseWebhook(t *testing.T) {
	body := []byte(`{
		"version": "1.2",
		"incident": {
			"incident_id": "0.abc",
			"policy_name": "too-many-errors",
			"condition_name": "count",
			"state": "open",
			"started_at": 1782787431,
			"observed_value": "12.0",
			"policy_user_labels": {
				"openchoreo-namespace": "default",
				"openchoreo-rule-name": "too-many-errors"
			}
		}
	}`)
	d, err := ParseWebhook(body)
	if err != nil {
		t.Fatalf("ParseWebhook error: %v", err)
	}
	if d.RuleNamespace != "default" || d.RuleName != "too-many-errors" {
		t.Errorf("identity = %q/%q", d.RuleNamespace, d.RuleName)
	}
	if d.State != "open" {
		t.Errorf("state = %q", d.State)
	}
	if d.AlertValue != 12.0 {
		t.Errorf("alertValue = %v, want 12.0 (parsed from string)", d.AlertValue)
	}
	if !d.AlertTimestamp.Equal(time.Unix(1782787431, 0).UTC()) {
		t.Errorf("timestamp = %v", d.AlertTimestamp)
	}
}

func TestParseWebhook_MissingIdentity(t *testing.T) {
	body := []byte(`{"incident":{"state":"open","policy_user_labels":{}}}`)
	if _, err := ParseWebhook(body); err == nil {
		t.Error("expected error when policy_user_labels lack the openchoreo identity")
	}
}

func TestParseWebhook_BadJSON(t *testing.T) {
	if _, err := ParseWebhook([]byte("not json")); err == nil {
		t.Error("expected error for malformed body")
	}
}
