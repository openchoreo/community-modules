// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"strings"
	"testing"
)

func TestBuildAlertFilter_ReleaseLine(t *testing.T) {
	const (
		componentUID = "846bb581-d6ba-446d-a160-8cec468d2219"
		lineUID      = "93f65294-926f-475b-98f1-dc4f152f38da"
	)
	lineClause := `labels."k8s-pod/openchoreo_dev/release-line-uid"="` + lineUID + `"`
	mainClauses := []string{
		`labels."k8s-pod/openchoreo_dev/component-uid"="` + componentUID + `"`,
		`NOT labels."k8s-pod/openchoreo_dev/release-line-uid":*`,
	}

	tests := []struct {
		name        string
		in          RuleInput
		want        []string
		notWant     []string
		wantApplied bool
	}{
		{
			name:        "main line rule excludes release line entries",
			in:          RuleInput{Namespace: "default", ComponentUID: componentUID, Query: "panic"},
			want:        mainClauses,
			wantApplied: true,
		},
		{
			name:        "release line rule matches the line UID alone",
			in:          RuleInput{Namespace: "default", ComponentUID: componentUID, ReleaseLineUID: lineUID, Query: "panic"},
			want:        []string{lineClause},
			notWant:     []string{"component-uid"},
			wantApplied: true,
		},
		{
			name:    "nil UUID line is the main line",
			in:      RuleInput{Namespace: "default", ComponentUID: componentUID, ReleaseLineUID: "00000000-0000-0000-0000-000000000000", Query: "panic"},
			want:    mainClauses,
			notWant: []string{lineClause},
			// The nil UUID line is dropped, leaving the component's main line.
			wantApplied: true,
		},
		{
			name:    "no component or line",
			in:      RuleInput{Namespace: "default", Query: "panic"},
			notWant: []string{"component-uid", "release-line-uid"},
		},
		{
			name:    "raw filter is used verbatim",
			in:      RuleInput{Namespace: "default", ComponentUID: componentUID, ReleaseLineUID: lineUID, Query: `severity>=ERROR`},
			notWant: []string{"release-line-uid", "component-uid"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := BuildAlertFilter(tt.in)
			for _, w := range tt.want {
				if !strings.Contains(f, w) {
					t.Errorf("filter missing %q:\n%s", w, f)
				}
			}
			for _, nw := range tt.notWant {
				if strings.Contains(f, nw) {
					t.Errorf("filter must not contain %q:\n%s", nw, f)
				}
			}
			if got := ReleaseLineApplied(tt.in); got != tt.wantApplied {
				t.Errorf("ReleaseLineApplied = %v, want %v", got, tt.wantApplied)
			}
		})
	}
}

func TestUnscopableLineRule(t *testing.T) {
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	for name, tc := range map[string]struct {
		in   RuleInput
		want bool
	}{
		"line with a raw filter":      {in: RuleInput{ReleaseLineUID: lineUID, Query: `severity>=ERROR`}, want: true},
		"line with a search phrase":   {in: RuleInput{ReleaseLineUID: lineUID, Query: "panic"}},
		"main line with a raw filter": {in: RuleInput{Query: `severity>=ERROR`}},
		"nil UUID line is the main line": {
			in: RuleInput{ReleaseLineUID: "00000000-0000-0000-0000-000000000000", Query: `severity>=ERROR`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := UnscopableLineRule(tc.in); got != tc.want {
				t.Errorf("UnscopableLineRule = %v, want %v", got, tc.want)
			}
		})
	}
}
