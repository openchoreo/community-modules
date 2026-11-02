// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import (
	"strings"
	"testing"
	"time"
)

const (
	testComponentUID   = "3f9c2b1e-8a4d-4c6e-9b2f-1d7e5a0c4b8f"
	testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
)

func TestBuildResourceMetricsKQL_ReleaseLine(t *testing.T) {
	component := `tostring(_labels["openchoreo.dev/component-uid"]) == "` + testComponentUID + `"`
	line := `tostring(_labels["openchoreo.dev/release-line-uid"]) == "` + testReleaseLineUID + `"`
	cases := map[string]struct {
		lineUID  string
		allLines bool
		want     string
		notWant  []string
	}{
		"main line": {
			want:    component + ` and isempty(tostring(_labels["openchoreo.dev/release-line-uid"]))`,
			notWant: []string{LabelReleaseLineComponentUID},
		},
		"every line": {
			allLines: true,
			want:     "(" + component + ` or tostring(_labels["openchoreo.dev/release-line-component-uid"]) == "` + testComponentUID + `")`,
			notWant:  []string{"isempty"},
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
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			kql := BuildResourceMetricsKQL(MetricsQueryParams{
				Namespace: "dp-acme-dev", ComponentUID: testComponentUID,
				ReleaseLineUID: tc.lineUID, AllReleaseLines: tc.allLines, Step: time.Minute,
			})
			mustContain(t, kql, "\n    | where "+tc.want+"\n")
			for _, s := range tc.notWant {
				if strings.Contains(kql, s) {
					t.Errorf("KQL filters on %q:\n%s", s, kql)
				}
			}
		})
	}
}

// allReleaseLines alone names no component, so it filters nothing.
func TestBuildResourceMetricsKQL_AllReleaseLinesWithoutComponent(t *testing.T) {
	kql := BuildResourceMetricsKQL(MetricsQueryParams{Namespace: "dp-acme-dev", AllReleaseLines: true})
	if strings.Contains(kql, "component-uid") || strings.Contains(kql, "release-line") {
		t.Errorf("KQL scopes by release line without a component:\n%s", kql)
	}
}
