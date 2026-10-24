// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"strings"
	"testing"
	"time"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

func TestBuildComponentLogsKQL_ReleaseLine(t *testing.T) {
	p := ComponentLogsParams{Namespace: "default", ComponentUID: "comp-uid", ReleaseLineUID: testReleaseLineUID}
	want := `| where tostring(parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/release-line-uid"]) == "` +
		testReleaseLineUID + `"`
	if got := BuildComponentLogsKQL(p); !strings.Contains(got, want) {
		t.Errorf("KQL missing release line filter %q\nKQL:\n%s", want, got)
	}

	p.ReleaseLineUID = ""
	if got := BuildComponentLogsKQL(p); strings.Contains(got, LabelReleaseLineUID) {
		t.Errorf("KQL filters on the release line without one:\n%s", got)
	}
}

func TestBuildEventsKQL_ReleaseLine(t *testing.T) {
	p := EventsParams{
		Component: &ComponentEventScope{Namespace: "acme", ComponentUID: testComponentUID, ReleaseLineUID: testReleaseLineUID},
		StartTime: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	}
	got, err := BuildEventsKQL(p, DefaultEventsTable, DefaultEventsScopeName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		`| where ResourceAttributes has "` + testReleaseLineUID + `"`,
		`| where tostring(ResourceAttributes["k8s.object.label.openchoreo.dev/release-line-uid"]) == "` + testReleaseLineUID + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("KQL missing %q\nKQL:\n%s", want, got)
		}
	}

	p.Component.ReleaseLineUID = ""
	got, err = BuildEventsKQL(p, DefaultEventsTable, DefaultEventsScopeName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, LabelReleaseLineUID) {
		t.Errorf("KQL filters on the release line without one:\n%s", got)
	}
}
