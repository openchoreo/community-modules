// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"strings"
	"testing"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

var releaseLineMatcher = `label_openchoreo_dev_release_line_uid="` + testReleaseLineUID + `"`

func TestPodLabels_ReleaseLine(t *testing.T) {
	selectors := map[string]func(line string) string{
		"query": func(line string) string { return BuildPodLabels("ns", "comp", "proj", "env", line, line == "") },
		"alert": func(line string) string { return BuildComponentPodLabels("comp", "proj", "env", line) },
	}
	for name, build := range selectors {
		t.Run(name, func(t *testing.T) {
			if got := build(""); !strings.Contains(got, `label_openchoreo_dev_release_line_uid=""`) {
				t.Errorf("the main line's selector must exclude release line pods: %s", got)
			}
			got := build(testReleaseLineUID)
			if !strings.Contains(got, releaseLineMatcher) || strings.Contains(got, "component_uid") {
				t.Errorf("a release line's selector must match %s alone: %s", releaseLineMatcher, got)
			}
		})
	}
}

func TestBuildAlertExpression_ReleaseLine(t *testing.T) {
	for _, metric := range []string{MetricTypeCPUUsage, MetricTypeMemoryUsage, MetricTypeBudget} {
		t.Run(metric, func(t *testing.T) {
			expr, err := buildAlertExpression(AlertRuleParams{
				ComponentUID: "comp", ProjectUID: "proj", EnvironmentUID: "env", ReleaseLineUID: testReleaseLineUID,
				Metric: metric, Operator: "gt", Threshold: 80, Window: "5m",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(expr, releaseLineMatcher) || strings.Contains(expr, `component_uid="comp"`) {
				t.Errorf("expression must match %s alone: %s", releaseLineMatcher, expr)
			}
		})
	}
}

func TestRuntimeTopologyMapping_ReleaseLineComponentUID(t *testing.T) {
	expr := buildHubblePodMappingExprWithSide("source", "src_component", "src_component_uid", `label_openchoreo_dev_namespace="ns"`)
	want := `"src_component_uid", "$1", "label_openchoreo_dev_release_line_component_uid", "(.+)")`
	if !strings.HasSuffix(expr, want) {
		t.Errorf("a release line pod's component UID must fill the topology UID label, got %s", expr)
	}
}
