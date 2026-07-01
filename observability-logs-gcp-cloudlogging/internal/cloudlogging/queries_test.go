// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import (
	"strings"
	"testing"
	"time"
)

func TestBuildComponentLogsFilter_LabelPathDotsSanitized(t *testing.T) {
	// The single most important assertion: the modern GKE managed logging
	// agent surfaces pod labels under labels."k8s-pod/<key>" with DOTS in the
	// key replaced by underscores. Verified against live cluster entries —
	// e.g. openchoreo.dev/component-uid -> k8s-pod/openchoreo_dev/component-uid.
	// If this regresses to the dotted form, the filter silently matches nothing.
	defer restoreSanitize(SanitizePodLabelDots)
	SanitizePodLabelDots = true

	f := BuildComponentLogsFilter(ComponentLogsParams{
		Namespace:    "dp-default-development-4b8b4fdc",
		ComponentUID: "11111111-1111-1111-1111-111111111111",
	})

	want := `labels."k8s-pod/openchoreo_dev/component-uid"="11111111-1111-1111-1111-111111111111"`
	if !strings.Contains(f, want) {
		t.Fatalf("expected filter to contain %q\nfilter:\n%s", want, f)
	}
	if strings.Contains(f, "openchoreo.dev") {
		t.Fatalf("filter must not contain the dotted label form; got:\n%s", f)
	}
}

func TestBuildComponentLogsFilter_LabelPathDotsPreservedWhenDisabled(t *testing.T) {
	// With sanitization disabled (for clusters whose agent preserves dots),
	// the raw dotted key is used verbatim.
	defer restoreSanitize(SanitizePodLabelDots)
	SanitizePodLabelDots = false

	f := BuildComponentLogsFilter(ComponentLogsParams{
		Namespace:    "ns1",
		ComponentUID: "c-uid",
	})
	want := `labels."k8s-pod/openchoreo.dev/component-uid"="c-uid"`
	if !strings.Contains(f, want) {
		t.Fatalf("expected dotted form when disabled; got:\n%s", f)
	}
}

// restoreSanitize resets the package toggle after a test mutates it.
func restoreSanitize(prev bool) { SanitizePodLabelDots = prev }

func TestBuildComponentLogsFilter_RequiredAndOptionalClauses(t *testing.T) {
	start := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)

	f := BuildComponentLogsFilter(ComponentLogsParams{
		Namespace:      "ns1",
		ComponentUID:   "c-uid",
		ProjectUID:     "p-uid",
		EnvironmentUID: "e-uid",
		StartTime:      start,
		EndTime:        end,
		LogLevels:      []string{"ERROR"},
		SearchPhrase:   "boom",
	})

	for _, want := range []string{
		`resource.type="k8s_container"`,
		`labels."k8s-pod/openchoreo_dev/namespace"="ns1"`,
		`labels."k8s-pod/openchoreo_dev/component-uid"="c-uid"`,
		`labels."k8s-pod/openchoreo_dev/project-uid"="p-uid"`,
		`labels."k8s-pod/openchoreo_dev/environment-uid"="e-uid"`,
		`timestamp>="2026-06-25T00:00:00Z"`,
		`timestamp<="2026-06-26T00:00:00Z"`,
		`severity="ERROR"`,
		`textPayload:"boom"`,
		`jsonPayload.message:"boom"`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("filter missing %q\nfilter:\n%s", want, f)
		}
	}
}

func TestBuildComponentLogsFilter_OnlyNamespaceRequired(t *testing.T) {
	f := BuildComponentLogsFilter(ComponentLogsParams{Namespace: "ns1"})
	if strings.Contains(f, "component-uid") || strings.Contains(f, "project-uid") || strings.Contains(f, "environment-uid") {
		t.Fatalf("optional UID clauses should be absent when empty; got:\n%s", f)
	}
	if strings.Contains(f, "severity=") || strings.Contains(f, "Payload:") {
		t.Fatalf("severity/phrase clauses should be absent when empty; got:\n%s", f)
	}
}

func TestSeverityClause_WarnMapsToWarning(t *testing.T) {
	if got := severityClause([]string{"WARN"}); got != `severity="WARNING"` {
		t.Errorf("WARN should map to GCP WARNING; got %q", got)
	}
	if got := severityClause([]string{"DEBUG", "ERROR"}); got != `(severity="DEBUG" OR severity="ERROR")` {
		t.Errorf("multiple levels should OR together; got %q", got)
	}
	if got := severityClause([]string{"bogus"}); got != "" {
		t.Errorf("unrecognized level should drop out; got %q", got)
	}
}

func TestBuildWorkflowLogsFilter(t *testing.T) {
	f := BuildWorkflowLogsFilter(WorkflowLogsParams{
		Namespace:       "ns1",
		WorkflowRunName: "build-123",
	})
	for _, want := range []string{
		`resource.labels.namespace_name="workflows-ns1"`,
		`resource.labels.pod_name:"build-123"`,
		`resource.labels.container_name!="init"`,
		`resource.labels.container_name!="wait"`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("workflow filter missing %q\nfilter:\n%s", want, f)
		}
	}
}

func TestQuote_EscapesInjection(t *testing.T) {
	got := quote(`a"b\c` + "\n" + "d")
	want := `"a\"b\\c d"`
	if got != want {
		t.Errorf("quote() = %q, want %q", got, want)
	}
}

func TestToGCPSeverity(t *testing.T) {
	cases := map[string]string{
		"debug": "DEBUG", "INFO": "INFO", "warn": "WARNING",
		"WARNING": "WARNING", "Error": "ERROR", "weird": "",
	}
	for in, want := range cases {
		if got := toGCPSeverity(in); got != want {
			t.Errorf("toGCPSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}
