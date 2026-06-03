// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"strings"
	"testing"
	"time"
)

func TestBuildComponentLogsKQL_AllFiltersPresent(t *testing.T) {
	p := ComponentLogsParams{
		Namespace:      "default",
		ComponentUID:   "comp-uid-1",
		ProjectUID:     "proj-uid-1",
		EnvironmentUID: "env-uid-1",
		StartTime:      time.Date(2026, 5, 26, 0, 0, 0, 0, time.UTC),
		EndTime:        time.Date(2026, 5, 26, 1, 0, 0, 0, time.UTC),
		Limit:          50,
		SortOrder:      SortDesc,
		SearchPhrase:   "connection refused",
		LogLevels:      []string{"ERROR", "WARN"},
	}

	got := BuildComponentLogsKQL(p)

	expects := []string{
		"ContainerLogV2",
		`parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/namespace"]`,
		`"default"`,
		`parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/component-uid"]`,
		`parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/project-uid"]`,
		`parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/environment-uid"]`,
		`"comp-uid-1"`,
		`"proj-uid-1"`,
		`"env-uid-1"`,
		`LogLevel in ("ERROR", "WARN")`,
		`tostring(LogMessage) contains "connection refused"`,
		`order by TimeGenerated desc`,
		`take 50`,
		`project`,
	}
	for _, e := range expects {
		if !strings.Contains(got, e) {
			t.Errorf("KQL missing %q\nKQL:\n%s", e, got)
		}
	}
}

func TestBuildComponentLogsKQL_OnlyNamespace(t *testing.T) {
	p := ComponentLogsParams{
		Namespace: "default",
		Limit:     100,
	}
	got := BuildComponentLogsKQL(p)

	mustContain := []string{
		"ContainerLogV2",
		`parse_json(tostring(KubernetesMetadata.podLabels))["openchoreo.dev/namespace"]`,
		`"default"`,
		`order by TimeGenerated desc`,
		`take 100`,
	}
	for _, e := range mustContain {
		if !strings.Contains(got, e) {
			t.Errorf("KQL missing %q\nKQL:\n%s", e, got)
		}
	}

	// These appear in the project clause unconditionally, but must NOT appear
	// as `where` filters when the corresponding UID is empty. Check the where
	// section only (everything before `| project`).
	whereSection := got
	if idx := strings.Index(got, "| project"); idx >= 0 {
		whereSection = got[:idx]
	}
	mustNotContain := []string{
		`component-uid`,
		`project-uid`,
		`environment-uid`,
		`LogLevel in`,
		`tostring(LogMessage) contains`,
	}
	for _, e := range mustNotContain {
		if strings.Contains(whereSection, e) {
			t.Errorf("KQL where-section should not contain %q when filter is empty\nKQL:\n%s", e, got)
		}
	}
}

func TestBuildComponentLogsKQL_SortAsc(t *testing.T) {
	p := ComponentLogsParams{Namespace: "ns", SortOrder: SortAsc, Limit: 10}
	got := BuildComponentLogsKQL(p)
	if !strings.Contains(got, "order by TimeGenerated asc") {
		t.Errorf("expected asc sort, got:\n%s", got)
	}
}

func TestBuildWorkflowLogsKQL_WorkflowsNamespacePrefix(t *testing.T) {
	p := WorkflowLogsParams{
		Namespace:       "default",
		WorkflowRunName: "build-2026-05-26-abc",
		Limit:           20,
		SortOrder:       SortDesc,
	}
	got := BuildWorkflowLogsKQL(p)

	mustContain := []string{
		`PodNamespace == "workflows-default"`,
		`PodName startswith "build-2026-05-26-abc"`,
		`ContainerName !in ("init", "wait")`,
		`order by TimeGenerated desc`,
		`take 20`,
	}
	for _, e := range mustContain {
		if !strings.Contains(got, e) {
			t.Errorf("workflow KQL missing %q\nKQL:\n%s", e, got)
		}
	}
}

func TestPingKQL(t *testing.T) {
	got := PingKQL()
	if got != `ContainerLogV2 | take 1` {
		t.Errorf("PingKQL = %q, want %q", got, `ContainerLogV2 | take 1`)
	}
}

func TestKQLStringEscaping(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`hello`, `"hello"`},
		{`with "quote"`, `"with \"quote\""`},
		{`back\slash`, `"back\\slash"`},
		{"line1\nline2", `"line1 line2"`},
		{"line1\rline2", `"line1 line2"`},
	}
	for _, tt := range tests {
		got := kqlString(tt.in)
		if got != tt.want {
			t.Errorf("kqlString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
