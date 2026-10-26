// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opencost

import "testing"

func TestBuildFilterWithReleaseLine(t *testing.T) {
	const base = `label[openchoreo_dev_namespace]:"default" + label[openchoreo_dev_environment_uid]:"env-1" + ` +
		`label[openchoreo_dev_project_uid]:"proj-1"`
	tests := []struct {
		name     string
		line     string
		allLines bool
		want     string
	}{
		{name: "one line matches on its UID alone", line: "line-1",
			want: base + ` + label[openchoreo_dev_release_line_uid]:"line-1"`},
		{name: "default line",
			want: base + ` + label[openchoreo_dev_component_uid]:"comp-1" + label[openchoreo_dev_release_line_uid]:""`},
		{name: "every line", allLines: true,
			want: base + ` + (label[openchoreo_dev_component_uid]:"comp-1" | label[openchoreo_dev_release_line_component_uid]:"comp-1")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildFilter("default", "env-1", "proj-1", "comp-1", tt.line, tt.allLines); got != tt.want {
				t.Errorf("BuildFilter() =\n %s\nwant\n %s", got, tt.want)
			}
		})
	}
}
