// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"strings"
	"testing"
)

const (
	testComponentUID   = "3f9c2b1e-8a4d-4c6e-9b2f-1d7e5a0c4b8f"
	testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

	podLabelComponent = `tostring(parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/component-uid"])`
	podLabelLine      = `tostring(parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/release-line-uid"])`
)

func lineRuleInput(lineUID string) RuleInput {
	in := validInput()
	in.Query = "connection refused"
	in.ComponentUID = testComponentUID
	in.ReleaseLineUID = lineUID
	return in
}

// A rule without a line covers the main line only.
func TestBuildAlertKQL_MainLine(t *testing.T) {
	in := lineRuleInput("")
	want := "\n| where " + podLabelComponent + ` == "` + testComponentUID + `" and isempty(` + podLabelLine + ")\n"
	if got := BuildAlertKQL(in); !strings.Contains(got, want) {
		t.Errorf("alert KQL missing %q\nKQL:\n%s", want, got)
	}
	if !ReleaseLineScoped(in) {
		t.Error("a main line rule must report it is scoped")
	}
}

// A rule with a line covers that line, matched on its UID alone.
func TestBuildAlertKQL_ReleaseLine(t *testing.T) {
	in := lineRuleInput(testReleaseLineUID)
	got := BuildAlertKQL(in)
	if want := "\n| where " + podLabelLine + ` == "` + testReleaseLineUID + `"` + "\n"; !strings.Contains(got, want) {
		t.Errorf("alert KQL missing %q\nKQL:\n%s", want, got)
	}
	if strings.Contains(got, labelComponentUID) {
		t.Errorf("a release line's pods carry no component-uid label, so the rule must not filter on it:\n%s", got)
	}
	if !ReleaseLineScoped(in) {
		t.Error("a release line rule must report it is scoped")
	}
}

// A full KQL statement is sent as is, so the rule is not scoped.
func TestReleaseLineScoped_FullKQLStatement(t *testing.T) {
	in := validInput()
	in.ComponentUID = testComponentUID
	in.ReleaseLineUID = testReleaseLineUID
	if ReleaseLineScoped(in) {
		t.Error("a full KQL statement passes through unfiltered, so it is not scoped")
	}
	if got := BuildAlertKQL(in); got != in.Query {
		t.Errorf("full KQL statement was rewritten: %s", got)
	}
}

func TestUnscopableLineRule(t *testing.T) {
	const fullKQL = `ContainerLogV2 | where LogLevel == "ERROR"`
	for name, tc := range map[string]struct {
		line, query string
		want        bool
	}{
		"line with a full KQL statement":      {line: testReleaseLineUID, query: fullKQL, want: true},
		"line with a search phrase":           {line: testReleaseLineUID, query: "connection refused"},
		"main line with a full KQL statement": {query: fullKQL},
		"zero UUID line is the main line":     {line: "00000000-0000-0000-0000-000000000000", query: fullKQL},
	} {
		t.Run(name, func(t *testing.T) {
			in := lineRuleInput(tc.line)
			in.Query = tc.query
			if got := UnscopableLineRule(in); got != tc.want {
				t.Errorf("UnscopableLineRule = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReleaseLineScoped_ZeroUUIDsAreUnset(t *testing.T) {
	in := lineRuleInput("00000000-0000-0000-0000-000000000000")
	in.ComponentUID = "00000000-0000-0000-0000-000000000000"
	if ReleaseLineScoped(in) {
		t.Error("zero UUIDs must read as no component and no release line")
	}
	if got := BuildAlertKQL(in); strings.Contains(got, "openchoreo.dev/release-line-uid") {
		t.Errorf("alert KQL scoped by release line without a component or a line:\n%s", got)
	}
}

func TestToScheduledQueryRule_ReleaseLineCustomProperty(t *testing.T) {
	res, err := ToScheduledQueryRule(lineRuleInput(testReleaseLineUID), validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	props := res.Properties.Actions.CustomProperties
	if got := props["openchoreo-release-line-uid"]; got == nil || *got != testReleaseLineUID {
		t.Errorf("openchoreo-release-line-uid custom property = %v, want %s", got, testReleaseLineUID)
	}
	if got := props["openchoreo-component-uid"]; got == nil || *got != testComponentUID {
		t.Errorf("openchoreo-component-uid custom property = %v, want %s", got, testComponentUID)
	}

	res, err = ToScheduledQueryRule(lineRuleInput(""), validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := res.Properties.Actions.CustomProperties["openchoreo-release-line-uid"]; ok {
		t.Error("release line custom property set without a release line")
	}
}
