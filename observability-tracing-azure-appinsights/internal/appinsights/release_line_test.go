// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

import (
	"strings"
	"testing"
)

const (
	testComponentUID   = "9b2e5f1a-3c4d-4e6f-8a1b-2c3d4e5f6a7b"
	testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
)

func TestTraceQueries_ReleaseLine(t *testing.T) {
	component := `tostring(Properties["openchoreo.dev/component-uid"]) == "` + testComponentUID + `"`
	line := `tostring(Properties["openchoreo.dev/release-line-uid"]) == "` + testReleaseLineUID + `"`
	cases := map[string]struct {
		lineUID  string
		allLines bool
		want     string
		notWant  []string
	}{
		"main line": {
			want:    component + ` and isempty(tostring(Properties["openchoreo.dev/release-line-uid"]))`,
			notWant: []string{LabelReleaseLineComponentUID},
		},
		"every line": {
			allLines: true,
			want:     "(" + component + ` or tostring(Properties["openchoreo.dev/release-line-component-uid"]) == "` + testComponentUID + `")`,
			notWant:  []string{"isempty(tostring(Properties"},
		},
		"one line": {
			lineUID: testReleaseLineUID,
			want:    line,
			notWant: []string{component, LabelReleaseLineComponentUID},
		},
		"one line takes precedence": {
			lineUID:  testReleaseLineUID,
			allLines: true,
			want:     line,
			notWant:  []string{component, LabelReleaseLineComponentUID},
		},
	}
	builders := map[string]func(TracesParams) string{
		"traces": BuildTracesListKQL,
		"spans": func(p TracesParams) string {
			p.TraceID = "4372fc01295900a9"
			return BuildSpansKQL(p)
		},
	}
	for builderName, build := range builders {
		for name, tc := range cases {
			t.Run(builderName+"/"+name, func(t *testing.T) {
				p := baseParams()
				p.ComponentUID = testComponentUID
				p.ReleaseLineUID = tc.lineUID
				p.AllReleaseLines = tc.allLines
				kql := build(p)
				if want := "\n| where " + tc.want + "\n"; !strings.Contains(kql, want) {
					t.Errorf("missing %q in:\n%s", want, kql)
				}
				for _, s := range tc.notWant {
					if strings.Contains(kql, s) {
						t.Errorf("unexpected %q in:\n%s", s, kql)
					}
				}
			})
		}
	}
}

// allReleaseLines alone names no component, so it filters nothing.
func TestTraceQueries_AllReleaseLinesWithoutComponent(t *testing.T) {
	p := baseParams()
	p.AllReleaseLines = true
	if kql := BuildTracesListKQL(p); strings.Contains(kql, "component-uid") || strings.Contains(kql, "release-line") {
		t.Errorf("KQL scopes by release line without a component:\n%s", kql)
	}
}

func TestTraceQueries_EscapesReleaseLineUID(t *testing.T) {
	p := baseParams()
	p.ReleaseLineUID = `l" or 1==1 //`
	kql := BuildTracesListKQL(p)
	if !strings.Contains(kql, `["openchoreo.dev/release-line-uid"]) == "l\" or 1==1 //"`) {
		t.Errorf("release line UID not escaped:\n%s", kql)
	}
}

// Release line labels are resource attributes, like the other OpenChoreo labels.
func TestSplitAttributes_ReleaseLineLabelsAreResourceAttributes(t *testing.T) {
	attrs, res := splitAttributes(map[string]interface{}{
		LabelReleaseLineUID:          testReleaseLineUID,
		LabelReleaseLineComponentUID: testComponentUID,
		"http.method":                "GET",
	})
	if res[LabelReleaseLineUID] != testReleaseLineUID || res[LabelReleaseLineComponentUID] != testComponentUID {
		t.Errorf("resource attributes = %v", res)
	}
	if _, ok := attrs[LabelReleaseLineUID]; ok {
		t.Errorf("release line UID reported as a span attribute: %v", attrs)
	}
}
