// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestIsRawFilter(t *testing.T) {
	raw := []string{
		`resource.type="k8s_container"`,
		`labels."k8s-pod/x"="y"`,
		`severity>=ERROR`,
		`severity="ERROR"`,
		`severity >= ERROR`, // leading space before operator
		`logName:"projects/p/logs/stdout"`,
		`jsonPayload.message:"boom"`,
	}
	for _, q := range raw {
		if !isRawFilter(q) {
			t.Errorf("isRawFilter(%q) = false, want true", q)
		}
	}
	// Free-text phrases that merely START with a field word must NOT be treated
	// as raw filters — the whole point of the fix.
	phrases := []string{
		"severity degraded",
		"severity",
		"logName rotated",
		"textPayload truncated",
		"panic",
		"connection refused",
	}
	for _, q := range phrases {
		if isRawFilter(q) {
			t.Errorf("isRawFilter(%q) = true, want false (plain phrase)", q)
		}
	}
}

func TestBuildAlertFilter_PhraseWithFieldWordIsScoped(t *testing.T) {
	// "severity degraded" is a phrase, not a filter: it must be scoped by
	// namespace and wrapped as a text search, not emitted unscoped.
	f := BuildAlertFilter(RuleInput{Namespace: "default", Query: "severity degraded"})
	if !strings.Contains(f, `resource.type="k8s_container"`) ||
		!strings.Contains(f, `labels."k8s-pod/openchoreo_dev/namespace"="default"`) ||
		!strings.Contains(f, `textPayload:"severity degraded"`) {
		t.Errorf("phrase should be scoped and text-searched; got:\n%s", f)
	}
}

func TestEscapeFilterValue(t *testing.T) {
	// A ruleName crafted to break out of the quoted literal must be neutralised:
	// every embedded double-quote must be backslash-escaped so it can't close
	// the literal and inject a boolean operator.
	in := `x" OR user_labels.managed-by="openchoreo`
	got := escapeFilterValue(in)
	// No bare (unescaped) double-quote may remain: scan and require a preceding
	// backslash on every quote.
	for i := 0; i < len(got); i++ {
		if got[i] == '"' && (i == 0 || got[i-1] != '\\') {
			t.Fatalf("escapeFilterValue left an unescaped quote at %d: %q", i, got)
		}
	}
	if !strings.Contains(got, `\"`) {
		t.Errorf("expected escaped quotes in %q", got)
	}
}

func TestSanitizeLabelValue_RuneSafe(t *testing.T) {
	// 30 three-byte runes = 90 bytes; truncation must land on a rune boundary
	// (never split a multi-byte rune) and stay <=63 bytes.
	v := strings.Repeat("あ", 30)
	got := sanitizeLabelValue(v)
	if len(got) > 63 {
		t.Errorf("len=%d, want <=63", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation split a rune: %q", got)
	}
}

func TestBuildAlertPolicy_StampsRuleIDAnchor(t *testing.T) {
	in := RuleInput{Namespace: "default", RuleName: "r", Operator: "gt", Threshold: 1, Window: "PT5M", Query: "x"}
	p, err := buildAlertPolicy(in, Config{ProjectID: "p"}, "oc-metric")
	if err != nil {
		t.Fatalf("buildAlertPolicy: %v", err)
	}
	want := deriveResourceName("default", "r")
	if got := p.GetUserLabels()[UserLabelRuleID]; got != want {
		t.Errorf("rule-id anchor = %q, want %q", got, want)
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
