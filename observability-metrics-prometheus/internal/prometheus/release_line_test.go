// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"strings"
	"testing"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

var releaseLineMatcher = `label_openchoreo_dev_release_line_uid="` + testReleaseLineUID + `"`

func TestLabelFilters_ReleaseLine(t *testing.T) {
	filters := map[string]func(line string) string{
		"query": func(line string) string { return BuildLabelFilter("ns", "comp", "proj", "env", line) },
		"alert": func(line string) string { return BuildComponentLabelFilter("comp", "proj", "env", line) },
	}
	for name, build := range filters {
		t.Run(name, func(t *testing.T) {
			if got := build(""); strings.Contains(got, "release_line") {
				t.Errorf("filter without a release line matches one: %s", got)
			}
			if got := build(testReleaseLineUID); !strings.Contains(got, releaseLineMatcher) {
				t.Errorf("filter %s missing %s", got, releaseLineMatcher)
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
			if !strings.Contains(expr, releaseLineMatcher) {
				t.Errorf("expression missing %s: %s", releaseLineMatcher, expr)
			}
		})
	}
}
