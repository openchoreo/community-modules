// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"strings"
	"testing"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

// releaseLineCases are the three component scopes, with what the filter must
// and must not hold for each, given how a label reads.
func releaseLineCases(label func(string) string) map[string]struct {
	lineUID  string
	allLines bool
	want     string
	notWant  []string
} {
	component := label(LabelComponentUID) + ` == "` + testComponentUID + `"`
	return map[string]struct {
		lineUID  string
		allLines bool
		want     string
		notWant  []string
	}{
		"main line": {
			want:    component + " and isempty(" + label(LabelReleaseLineUID) + ")",
			notWant: []string{LabelReleaseLineComponentUID},
		},
		"every line": {
			allLines: true,
			want:     "(" + component + " or " + label(LabelReleaseLineComponentUID) + ` == "` + testComponentUID + `")`,
			notWant:  []string{"isempty"},
		},
		"one line": {
			lineUID: testReleaseLineUID,
			want:    label(LabelReleaseLineUID) + ` == "` + testReleaseLineUID + `"`,
			notWant: []string{component, LabelReleaseLineComponentUID},
		},
		"one line takes precedence": {
			lineUID:  testReleaseLineUID,
			allLines: true,
			want:     label(LabelReleaseLineUID) + ` == "` + testReleaseLineUID + `"`,
			notWant:  []string{component, LabelReleaseLineComponentUID},
		},
	}
}

func TestBuildComponentLogsKQL_ReleaseLine(t *testing.T) {
	for name, tc := range releaseLineCases(podLabel) {
		t.Run(name, func(t *testing.T) {
			got := BuildComponentLogsKQL(ComponentLogsParams{
				Namespace: "default", ComponentUID: testComponentUID,
				ReleaseLineUID: tc.lineUID, AllReleaseLines: tc.allLines, Limit: 10,
			})
			where := got[:strings.Index(got, "| extend _labels")]
			if !strings.Contains(where, "| where "+tc.want+"\n") {
				t.Errorf("KQL missing %q\nKQL:\n%s", tc.want, got)
			}
			for _, s := range tc.notWant {
				if strings.Contains(where, s) {
					t.Errorf("KQL filters on %q\nKQL:\n%s", s, got)
				}
			}
		})
	}
}

// allReleaseLines alone names no component, so it filters nothing.
func TestBuildComponentLogsKQL_AllReleaseLinesWithoutComponent(t *testing.T) {
	got := BuildComponentLogsKQL(ComponentLogsParams{Namespace: "default", AllReleaseLines: true, Limit: 10})
	if where := got[:strings.Index(got, "| extend _labels")]; strings.Contains(where, "openchoreo.dev/release-line") ||
		strings.Contains(where, LabelComponentUID) {
		t.Errorf("KQL scopes by release line without a component:\n%s", got)
	}
}

// A release line's records report the component UID they carry under the
// release line's own label.
func TestBuildComponentLogsKQL_ComponentUIDFallsBackToReleaseLineLabel(t *testing.T) {
	got := BuildComponentLogsKQL(ComponentLogsParams{Namespace: "default", Limit: 10})
	want := `ComponentUID        = coalesce(tostring(_labels["openchoreo.dev/component-uid"]), ` +
		`tostring(_labels["openchoreo.dev/release-line-component-uid"]))`
	if !strings.Contains(got, want) {
		t.Errorf("KQL missing %q\nKQL:\n%s", want, got)
	}
}

func TestBuildEventsKQL_ReleaseLine(t *testing.T) {
	for name, tc := range releaseLineCases(eventLabel) {
		t.Run(name, func(t *testing.T) {
			p := eventParams()
			p.Component = &ComponentEventScope{
				Namespace: "acme", ComponentUID: testComponentUID,
				ReleaseLineUID: tc.lineUID, AllReleaseLines: tc.allLines,
			}
			base := baseStatement(t, buildEvents(t, p))

			// The has pre-filter is on the UID every case matches as a label value.
			matched := testComponentUID
			if tc.lineUID != "" {
				matched = tc.lineUID
			}
			assertContains(t, base, `| where ResourceAttributes has "`+matched+`"`+"\n| where "+tc.want)
			assertNotContains(t, base, tc.notWant...)
		})
	}
}

func TestBuildEventsKQL_ComponentUIDFallsBackToReleaseLineLabel(t *testing.T) {
	p := eventParams()
	p.Component = &ComponentEventScope{Namespace: "acme"}
	kql := buildEvents(t, p)
	assertContains(t, kql, `ComponentUID    = coalesce(`+eventLabel(LabelComponentUID)+`, `+
		eventLabel(LabelReleaseLineComponentUID)+`)`)
	assertNotContains(t, baseStatement(t, kql), "openchoreo.dev/release-line")
}

func TestBuildEventsKQL_EscapesReleaseLineUID(t *testing.T) {
	p := eventParams()
	p.Component = &ComponentEventScope{Namespace: "acme", ComponentUID: testComponentUID, ReleaseLineUID: `l" or 1==1 //`}
	base := baseStatement(t, buildEvents(t, p))

	assertContains(t, base, `openchoreo.dev/release-line-uid"]) == "l\" or 1==1 //"`)
	assertNotContains(t, base, `has "l`)
}
