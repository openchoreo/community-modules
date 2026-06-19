// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

import (
	"strings"
	"testing"
	"time"
)

func baseParams() TracesParams {
	return TracesParams{
		StartTime: time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC),
		Namespace: "spike-ns",
		Limit:     20,
		SortOrder: "desc",
	}
}

func TestBuildTracesListKQL_NamespaceOnly(t *testing.T) {
	kql := BuildTracesListKQL(baseParams())

	for _, want := range []string{
		`union withsource=SourceTable AppRequests, AppDependencies`,
		`| extend IsRoot = (ParentId == OperationId or isempty(ParentId))`,
		`| where tostring(Properties["openchoreo.dev/namespace"]) == "spike-ns"`,
		`by TraceId = OperationId`,
		`| order by StartTime desc`,
		`| take 20`,
	} {
		if !strings.Contains(kql, want) {
			t.Errorf("missing %q in:\n%s", want, kql)
		}
	}
	for _, label := range []string{LabelComponentUID, LabelProjectUID, LabelEnvironmentUID} {
		if strings.Contains(kql, label) {
			t.Errorf("unexpected optional filter %q in:\n%s", label, kql)
		}
	}
}

func TestBuildTracesListKQL_AllScopeFilters(t *testing.T) {
	p := baseParams()
	p.ComponentUID = "9b2e5f1a-3c4d-4e6f-8a1b-2c3d4e5f6a7b"
	p.ProjectUID = "1f8c7d6e-5b4a-4938-2716-0a9b8c7d6e5f"
	p.EnvironmentUID = "a4d3c2b1-0f9e-4d8c-7b6a-5f4e3d2c1b0a"
	p.SortOrder = "asc"
	p.Limit = 100

	kql := BuildTracesListKQL(p)

	for _, want := range []string{
		`| where tostring(Properties["openchoreo.dev/namespace"]) == "spike-ns"`,
		`| where tostring(Properties["openchoreo.dev/component-uid"]) == "9b2e5f1a-3c4d-4e6f-8a1b-2c3d4e5f6a7b"`,
		`| where tostring(Properties["openchoreo.dev/project-uid"]) == "1f8c7d6e-5b4a-4938-2716-0a9b8c7d6e5f"`,
		`| where tostring(Properties["openchoreo.dev/environment-uid"]) == "a4d3c2b1-0f9e-4d8c-7b6a-5f4e3d2c1b0a"`,
		`| order by StartTime asc`,
		`| take 100`,
	} {
		if !strings.Contains(kql, want) {
			t.Errorf("missing %q in:\n%s", want, kql)
		}
	}
}

func TestBuildTracesListKQL_RootFallback(t *testing.T) {
	kql := BuildTracesListKQL(baseParams())
	// Root fields must fall back to the earliest span when the root span is
	// missing from the window.
	if !strings.Contains(kql, `RootSpanName = iff(isempty(RootSpanName), EarliestSpanName, RootSpanName)`) {
		t.Errorf("missing earliest-span fallback in:\n%s", kql)
	}
}

func TestBuildSpansKQL(t *testing.T) {
	p := baseParams()
	p.TraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	p.Limit = 1000

	kql := BuildSpansKQL(p)

	for _, want := range []string{
		`| where OperationId == "4bf92f3577b34da6a3ce929d0e0e4736"`,
		`| where tostring(Properties["openchoreo.dev/namespace"]) == "spike-ns"`,
		`ParentSpanId = iff(IsRoot, "", ParentId)`,
		`| order by TimeGenerated asc`,
		`| take 1000`,
	} {
		if !strings.Contains(kql, want) {
			t.Errorf("missing %q in:\n%s", want, kql)
		}
	}
	if strings.Contains(kql, "Properties,") && !p.IncludeAttributes {
		t.Errorf("attributes projected without includeAttributes in:\n%s", kql)
	}
}

func TestBuildSpansKQL_IncludeAttributes(t *testing.T) {
	p := baseParams()
	p.TraceID = "abc123"
	p.IncludeAttributes = true

	kql := BuildSpansKQL(p)
	if !strings.Contains(kql, "Properties") || !strings.Contains(kql, "Measurements") {
		t.Errorf("missing attribute projection in:\n%s", kql)
	}
}

func TestBuildSpanDetailsKQL(t *testing.T) {
	kql := BuildSpanDetailsKQL("4bf92f3577b34da6", "00f067aa0ba902b7")

	for _, want := range []string{
		`| where OperationId == "4bf92f3577b34da6"`,
		`| where Id == "00f067aa0ba902b7"`,
		`Properties`,
		`| take 1`,
	} {
		if !strings.Contains(kql, want) {
			t.Errorf("missing %q in:\n%s", want, kql)
		}
	}
}

func TestValidID(t *testing.T) {
	valid := []string{"4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7", "ABCDEF01"}
	for _, id := range valid {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	invalid := []string{
		"",
		`" | union ContainerLogV2 | where "" == "`,
		"abc-123",
		"abc 123",
		"abc\"123",
		strings.Repeat("a", 65),
	}
	for _, id := range invalid {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestKQLStringEscaping(t *testing.T) {
	cases := map[string]string{
		`plain`:           `"plain"`,
		`with"quote`:      `"with\"quote"`,
		`back\slash`:      `"back\\slash"`,
		"line\nbreak":     `"line break"`,
		"carriage\rret":   `"carriage ret"`,
		`" == "" or 1==1`: `"\" == \"\" or 1==1"`,
	}
	for in, want := range cases {
		if got := kqlString(in); got != want {
			t.Errorf("kqlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestScopeInjectionIsNeutralized(t *testing.T) {
	p := baseParams()
	p.Namespace = `x" or tostring(Properties["openchoreo.dev/namespace"]) != "`
	kql := BuildTracesListKQL(p)
	// The malicious quote must arrive escaped, never as a bare literal
	// terminator.
	if !strings.Contains(kql, `\" or tostring`) {
		t.Errorf("injection not escaped in:\n%s", kql)
	}
}
