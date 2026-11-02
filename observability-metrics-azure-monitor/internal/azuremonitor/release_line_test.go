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
)

// A rule without a line covers the main line only.
func TestBuildAlertKQL_MainLine(t *testing.T) {
	in := RuleInput{Namespace: "dp-acme-dev", ComponentUID: testComponentUID, Metric: "cpu_usage"}
	kql := BuildAlertKQL(in, counterCPUUsageNanoCores)
	mustContain(t, kql, "\n    | where "+`tostring(_labels["openchoreo.dev/component-uid"]) == "`+testComponentUID+
		`" and isempty(tostring(_labels["openchoreo.dev/release-line-uid"]))`+"\n")
	if !ReleaseLineScoped(in) {
		t.Error("a main line rule must report it is scoped")
	}
}

// A rule with a line covers that line, matched on its UID alone.
func TestBuildAlertKQL_ReleaseLine(t *testing.T) {
	in := RuleInput{Namespace: "dp-acme-dev", ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID, Metric: "cpu_usage"}
	kql := BuildAlertKQL(in, counterCPUUsageNanoCores)
	mustContain(t, kql, "\n    | where "+`tostring(_labels["openchoreo.dev/release-line-uid"]) == "`+testReleaseLineUID+`"`+"\n")
	for _, s := range []string{labelComponentUID, labelNamespace} {
		if strings.Contains(kql, s) {
			t.Errorf("a release line rule must not filter on %s:\n%s", s, kql)
		}
	}
	if !ReleaseLineScoped(in) {
		t.Error("a release line rule must report it is scoped")
	}
}

func TestReleaseLineScoped_ZeroUUIDsAreUnset(t *testing.T) {
	in := RuleInput{
		Namespace:      "default",
		ComponentUID:   "00000000-0000-0000-0000-000000000000",
		ReleaseLineUID: "00000000-0000-0000-0000-000000000000",
		Metric:         "cpu_usage",
	}
	if ReleaseLineScoped(in) {
		t.Error("zero UUIDs must read as no component and no release line")
	}
	kql := BuildAlertKQL(in, counterCPUUsageNanoCores)
	if strings.Contains(kql, "release-line-uid") {
		t.Errorf("alert KQL scoped by release line without a component or a line:\n%s", kql)
	}
	mustContain(t, kql, `tostring(_labels["openchoreo.dev/namespace"]) == "default"`)
}

func TestToScheduledQueryRule_ReleaseLineCustomProperty(t *testing.T) {
	in := RuleInput{
		Namespace: "dp-acme-dev", RuleName: "high-cpu", ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID,
		Metric: "cpu_usage", Operator: "gt", Threshold: 80, Interval: "5m", Window: "10m", Enabled: true,
	}
	res, err := ToScheduledQueryRule(in, testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.Properties.Actions.CustomProperties["openchoreo-release-line-uid"]; got == nil || *got != testReleaseLineUID {
		t.Errorf("openchoreo-release-line-uid custom property = %v, want %s", got, testReleaseLineUID)
	}

	in.ReleaseLineUID = ""
	res, err = ToScheduledQueryRule(in, testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := res.Properties.Actions.CustomProperties["openchoreo-release-line-uid"]; ok {
		t.Error("release line custom property set without a release line")
	}
}
