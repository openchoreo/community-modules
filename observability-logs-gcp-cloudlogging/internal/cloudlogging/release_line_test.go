// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import (
	"strings"
	"testing"

	"cloud.google.com/go/logging"
)

const (
	testComponentUID = "846bb581-d6ba-446d-a160-8cec468d2219"
	testLineUID      = "93f65294-926f-475b-98f1-dc4f152f38da"
)

func TestReleaseLineScopeClauses(t *testing.T) {
	tests := []struct {
		name        string
		componentID string
		lineID      string
		allLines    bool
		want        []string
	}{
		{name: "no component or line"},
		{name: "every line without a component", allLines: true},
		{name: "one line matches on its UID alone", componentID: testComponentUID, lineID: testLineUID,
			want: []string{`labels."k8s-pod/openchoreo_dev/release-line-uid"="` + testLineUID + `"`}},
		{name: "one line takes precedence over every line", componentID: testComponentUID, lineID: testLineUID, allLines: true,
			want: []string{`labels."k8s-pod/openchoreo_dev/release-line-uid"="` + testLineUID + `"`}},
		{name: "main line", componentID: testComponentUID,
			want: []string{
				`labels."k8s-pod/openchoreo_dev/component-uid"="` + testComponentUID + `"`,
				`NOT labels."k8s-pod/openchoreo_dev/release-line-uid":*`,
			}},
		{name: "every line", componentID: testComponentUID, allLines: true,
			want: []string{`(labels."k8s-pod/openchoreo_dev/component-uid"="` + testComponentUID +
				`" OR labels."k8s-pod/openchoreo_dev/release-line-component-uid"="` + testComponentUID + `")`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := releaseLineScopeClauses(tt.componentID, tt.lineID, tt.allLines)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildComponentLogsFilter_ReleaseLine(t *testing.T) {
	p := ComponentLogsParams{Namespace: "default", ComponentUID: testComponentUID, ProjectUID: "2f1c9a4e-7b3d-4e5a-9c8f-1a2b3c4d5e6f"}

	if f := BuildComponentLogsFilter(p); !strings.Contains(f, `NOT labels."k8s-pod/openchoreo_dev/release-line-uid":*`) ||
		strings.Contains(f, "release-line-component-uid") {
		t.Errorf("a component query must cover its main line only:\n%s", f)
	}

	p.AllReleaseLines = true
	if f := BuildComponentLogsFilter(p); !strings.Contains(f, `labels."k8s-pod/openchoreo_dev/release-line-component-uid"="`+testComponentUID+`"`) ||
		strings.Contains(f, "NOT ") {
		t.Errorf("an every line query must cover its release lines' pods:\n%s", f)
	}

	p.ReleaseLineUID = testLineUID
	f := BuildComponentLogsFilter(p)
	if !strings.Contains(f, `labels."k8s-pod/openchoreo_dev/release-line-uid"="`+testLineUID+`"`) || strings.Contains(f, "component-uid") {
		t.Errorf("a release line query must match the line UID alone:\n%s", f)
	}
	if !strings.Contains(f, `labels."k8s-pod/openchoreo_dev/project-uid"=`) {
		t.Errorf("a release line query must keep the other scope clauses:\n%s", f)
	}
}

// A release line's entries are attributed to their component like the main line's.
func TestMapComponentEntry_ReleaseLineComponentUID(t *testing.T) {
	got := mapComponentEntry(&logging.Entry{Labels: map[string]string{
		podLabelKey(LabelReleaseLineComponentUID): testComponentUID,
		podLabelKey(LabelReleaseLineUID):          testLineUID,
	}})
	if got.ComponentUID != testComponentUID {
		t.Errorf("ComponentUID = %q, want %q", got.ComponentUID, testComponentUID)
	}
}
