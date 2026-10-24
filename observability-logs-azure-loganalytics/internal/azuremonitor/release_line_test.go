// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package azuremonitor

import (
	"strings"
	"testing"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

func TestBuildAlertKQL_ReleaseLine(t *testing.T) {
	in := validInput()
	in.Query = "connection refused"
	in.ComponentUID = "3f9c2b1e-8a4d-4c6e-9b2f-1d7e5a0c4b8f"
	in.ReleaseLineUID = testReleaseLineUID

	want := `| where tostring(parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/release-line-uid"]) == "` +
		testReleaseLineUID + `"`
	if got := BuildAlertKQL(in); !strings.Contains(got, want) {
		t.Errorf("alert KQL missing %q\nKQL:\n%s", want, got)
	}
	if !ReleaseLineScoped(in) {
		t.Error("expected the rule to be scoped to the release line")
	}

	in.ReleaseLineUID = ""
	if got := BuildAlertKQL(in); strings.Contains(got, labelReleaseLineUID) {
		t.Errorf("alert KQL filters on the release line without one:\n%s", got)
	}
	if ReleaseLineScoped(in) {
		t.Error("a rule without a release line must not report it as scoped")
	}
}

func TestReleaseLineScoped_FullKQLStatementIsNotScoped(t *testing.T) {
	in := validInput()
	in.ReleaseLineUID = testReleaseLineUID
	if ReleaseLineScoped(in) {
		t.Error("a full KQL statement passes through unfiltered, so it is not scoped")
	}
	if got := BuildAlertKQL(in); got != in.Query {
		t.Errorf("full KQL statement was rewritten: %s", got)
	}
}

func TestReleaseLineScoped_ZeroUUIDIsUnset(t *testing.T) {
	in := validInput()
	in.Query = "error"
	in.ReleaseLineUID = "00000000-0000-0000-0000-000000000000"
	if ReleaseLineScoped(in) {
		t.Error("a zero UUID must read as no release line")
	}
}

func TestToScheduledQueryRule_ReleaseLineCustomProperty(t *testing.T) {
	in := validInput()
	in.ReleaseLineUID = testReleaseLineUID
	res, err := ToScheduledQueryRule(in, validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := res.Properties.Actions.CustomProperties["openchoreo-release-line-uid"]
	if got == nil || *got != testReleaseLineUID {
		t.Errorf("openchoreo-release-line-uid custom property = %v, want %s", got, testReleaseLineUID)
	}

	in.ReleaseLineUID = ""
	res, err = ToScheduledQueryRule(in, validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := res.Properties.Actions.CustomProperties["openchoreo-release-line-uid"]; ok {
		t.Error("release line custom property set without a release line")
	}
}
