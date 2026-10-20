// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opencost

import "testing"

func TestBuildFilterWithReleaseLine(t *testing.T) {
	got := BuildFilter("default", "env-1", "proj-1", "comp-1", "line-1")
	want := `label[openchoreo_dev_namespace]:"default" + label[openchoreo_dev_environment_uid]:"env-1" + ` +
		`label[openchoreo_dev_project_uid]:"proj-1" + label[openchoreo_dev_component_uid]:"comp-1" + ` +
		`label[openchoreo_dev_release_line_uid]:"line-1"`
	if got != want {
		t.Errorf("BuildFilter() = %q, want %q", got, want)
	}
}
