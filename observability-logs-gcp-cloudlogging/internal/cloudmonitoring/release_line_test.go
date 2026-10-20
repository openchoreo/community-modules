// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudmonitoring

import (
	"strings"
	"testing"
)

func TestBuildAlertFilter_ReleaseLine(t *testing.T) {
	const lineUID = "93f65294-926f-475b-98f1-dc4f152f38da"
	want := `labels."k8s-pod/openchoreo_dev/release-line-uid"="` + lineUID + `"`

	tests := []struct {
		name       string
		in         RuleInput
		wantClause bool
		wantScoped bool
	}{
		{
			name:       "phrase with a line",
			in:         RuleInput{Namespace: "default", ComponentUID: "846bb581-d6ba-446d-a160-8cec468d2219", ReleaseLineUID: lineUID, Query: "panic"},
			wantClause: true,
			wantScoped: true,
		},
		{
			name: "phrase without a line",
			in:   RuleInput{Namespace: "default", ComponentUID: "846bb581-d6ba-446d-a160-8cec468d2219", Query: "panic"},
		},
		{
			name: "nil UUID line",
			in:   RuleInput{Namespace: "default", ReleaseLineUID: "00000000-0000-0000-0000-000000000000", Query: "panic"},
		},
		{
			name: "raw filter with a line is used verbatim",
			in:   RuleInput{Namespace: "default", ReleaseLineUID: lineUID, Query: `severity>=ERROR`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := BuildAlertFilter(tt.in)
			if got := strings.Contains(f, want); got != tt.wantClause {
				t.Errorf("release line clause present = %v, want %v:\n%s", got, tt.wantClause, f)
			}
			if got := ReleaseLineScoped(tt.in); got != tt.wantScoped {
				t.Errorf("ReleaseLineScoped = %v, want %v", got, tt.wantScoped)
			}
		})
	}
}
