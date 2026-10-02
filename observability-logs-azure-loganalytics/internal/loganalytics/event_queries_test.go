// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"strings"
	"testing"
	"time"
)

const (
	testComponentUID   = "3f9c2b1e-8a4d-4c6e-9b2f-1d7e5a0c4b8f"
	testProjectUID     = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
	testEnvironmentUID = "0f1e2d3c-4b5a-4968-8776-655443322110"
)

func eventParams() EventsParams {
	return EventsParams{
		StartTime: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Limit:     100,
	}
}

func buildEvents(t *testing.T, p EventsParams) string {
	t.Helper()
	kql, err := BuildEventsKQL(p, DefaultEventsTable, DefaultEventsScopeName)
	if err != nil {
		t.Fatalf("BuildEventsKQL: %v", err)
	}
	return kql
}

// baseStatement returns the `let Base = ...;` statement, where every filter lives.
func baseStatement(t *testing.T, kql string) string {
	t.Helper()
	end := strings.Index(kql, ";\n")
	if !strings.HasPrefix(kql, "let Base = ") || end < 0 {
		t.Fatalf("query does not start with a Base statement:\n%s", kql)
	}
	return kql[:end]
}

func assertContains(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in:\n%s", w, s)
		}
	}
}

func assertNotContains(t *testing.T, s string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(s, u) {
			t.Errorf("unexpected %q in:\n%s", u, s)
		}
	}
}

func TestBuildEventsKQL_ComponentScope(t *testing.T) {
	p := eventParams()
	p.Component = &ComponentEventScope{
		Namespace:      "acme",
		ComponentUID:   testComponentUID,
		ProjectUID:     testProjectUID,
		EnvironmentUID: testEnvironmentUID,
	}
	base := baseStatement(t, buildEvents(t, p))

	assertContains(t, base,
		`let Base = OTelLogs`,
		`| where ScopeName == "`+DefaultEventsScopeName+`"`,
		`| where ResourceAttributes has "acme"`,
		`tostring(ResourceAttributes["k8s.object.label.openchoreo.dev/namespace"]) == "acme"`,
		`tostring(ResourceAttributes["k8s.object.label.openchoreo.dev/component-uid"]) == "`+testComponentUID+`"`,
		`tostring(ResourceAttributes["k8s.object.label.openchoreo.dev/project-uid"]) == "`+testProjectUID+`"`,
		`tostring(ResourceAttributes["k8s.object.label.openchoreo.dev/environment-uid"]) == "`+testEnvironmentUID+`"`,
	)
	assertNotContains(t, base, "k8s.namespace.name", "k8s.event.reason")
}

func TestBuildEventsKQL_ComponentScopeOmitsBlankUIDs(t *testing.T) {
	p := eventParams()
	p.Component = &ComponentEventScope{Namespace: "acme"}
	base := baseStatement(t, buildEvents(t, p))

	assertNotContains(t, base, "component-uid", "project-uid", "environment-uid")
}

func TestBuildEventsKQL_WorkflowScope(t *testing.T) {
	p := eventParams()
	p.Workflow = &WorkflowEventScope{Namespace: "acme", WorkflowRunName: "build-42", TaskName: "push-image"}
	base := baseStatement(t, buildEvents(t, p))

	assertContains(t, base,
		`| where Attributes has "workflows-acme"`,
		`tostring(Attributes["k8s.namespace.name"]) == "workflows-acme"`,
		`tostring(ResourceAttributes["k8s.object.name"]) startswith_cs "build-42"`,
		`tostring(ResourceAttributes["k8s.object.name"]) contains_cs "push-image"`,
	)
	assertNotContains(t, base, "openchoreo.dev/namespace")
}

func TestBuildEventsKQL_WorkflowScopeWithoutTask(t *testing.T) {
	p := eventParams()
	p.Workflow = &WorkflowEventScope{Namespace: "acme", WorkflowRunName: "build-42"}
	base := baseStatement(t, buildEvents(t, p))

	assertNotContains(t, base, "contains_cs")
}

// An unscoped sweep is narrowed by reasons and the window only; nothing may
// widen or replace them.
func TestBuildEventsKQL_UnscopedSweepFiltersOnlyByReasons(t *testing.T) {
	p := eventParams()
	p.Reasons = []string{"DeploymentSucceeded", "DeploymentFailed"}
	base := baseStatement(t, buildEvents(t, p))

	assertContains(t, base,
		`| where Attributes has_any ("DeploymentSucceeded", "DeploymentFailed")`,
		`tostring(Attributes["k8s.event.reason"]) in ("DeploymentSucceeded", "DeploymentFailed")`,
	)
	assertNotContains(t, base, "openchoreo.dev/", "k8s.namespace.name", "k8s.object.name")
}

func TestBuildEventsKQL_ReasonsNarrowScopedQuery(t *testing.T) {
	p := eventParams()
	p.Component = &ComponentEventScope{Namespace: "acme"}
	p.Reasons = []string{"BackOff"}
	base := baseStatement(t, buildEvents(t, p))

	assertContains(t, base,
		`openchoreo.dev/namespace"]) == "acme"`,
		`tostring(Attributes["k8s.event.reason"]) in ("BackOff")`,
	)
}

func TestBuildEventsKQL_RejectsBadScopeCombinations(t *testing.T) {
	unscoped := eventParams()
	if _, err := BuildEventsKQL(unscoped, DefaultEventsTable, DefaultEventsScopeName); err == nil {
		t.Error("an unscoped query without reasons must be refused")
	}

	both := eventParams()
	both.Component = &ComponentEventScope{Namespace: "acme"}
	both.Workflow = &WorkflowEventScope{Namespace: "acme", WorkflowRunName: "r"}
	if _, err := BuildEventsKQL(both, DefaultEventsTable, DefaultEventsScopeName); err == nil {
		t.Error("a query with two scopes must be refused")
	}
}

// The window is [start, end): a caller resumes from the last timestamp it was
// given, so the start must be inclusive.
func TestBuildEventsKQL_WindowIsStartInclusiveEndExclusive(t *testing.T) {
	p := eventParams()
	p.Reasons = []string{"BackOff"}
	base := baseStatement(t, buildEvents(t, p))

	assertContains(t, base,
		`| where TimeGenerated >= datetime(2026-09-01T00:00:00.0000000Z) and TimeGenerated < datetime(2026-09-02T00:00:00.0000000Z)`)
}

func TestKQLDatetime(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"whole second", time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), "datetime(2026-09-01T10:00:00.0000000Z)"},
		{"on a tick", time.Date(2026, 9, 1, 10, 0, 0, 123456700, time.UTC), "datetime(2026-09-01T10:00:00.1234567Z)"},
		// Stored times sit on 100ns ticks, so rounding up keeps `>= t` exact.
		{"between ticks rounds up", time.Date(2026, 9, 1, 10, 0, 0, 123456701, time.UTC), "datetime(2026-09-01T10:00:00.1234568Z)"},
		{"rounding carries into the second", time.Date(2026, 9, 1, 10, 0, 0, 999999999, time.UTC), "datetime(2026-09-01T10:00:01.0000000Z)"},
		{"converted to UTC", time.Date(2026, 9, 1, 15, 30, 0, 0, time.FixedZone("IST", 5*3600+1800)), "datetime(2026-09-01T10:00:00.0000000Z)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kqlDatetime(tc.in); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// Ascending: the page ends at the limit-th event's timestamp and takes every
// event bearing it, so a group is never split across reads.
func TestBuildEventsKQL_AscendingBoundary(t *testing.T) {
	p := eventParams()
	p.Limit = 50
	p.SortOrder = SortAsc
	p.Reasons = []string{"BackOff"}
	kql := buildEvents(t, p)

	assertContains(t, kql,
		"let Boundary = toscalar(Base | top 50 by TimeGenerated asc | summarize max(TimeGenerated));",
		"| where TimeGenerated <= Boundary",
		"| order by TimeGenerated asc, _ItemId asc",
		"| take 10050",
	)
}

func TestBuildEventsKQL_DescendingBoundary(t *testing.T) {
	p := eventParams()
	p.Limit = 50
	p.SortOrder = SortDesc
	p.Reasons = []string{"BackOff"}
	kql := buildEvents(t, p)

	assertContains(t, kql,
		"let Boundary = toscalar(Base | top 50 by TimeGenerated desc | summarize min(TimeGenerated));",
		"| where TimeGenerated >= Boundary",
		"| order by TimeGenerated desc, _ItemId desc",
	)
}

func TestBuildEventsKQL_DefaultsToDescAndDefaultLimit(t *testing.T) {
	p := eventParams()
	p.Limit = 0
	p.SortOrder = ""
	p.Reasons = []string{"BackOff"}
	kql := buildEvents(t, p)

	assertContains(t, kql, "top 100 by TimeGenerated desc", "| take 10100")
}

// total counts the whole window: nothing may truncate the set before it is
// counted, or a truncated read could look complete.
func TestBuildEventsKQL_CountsTheWholeWindow(t *testing.T) {
	p := eventParams()
	p.Limit = 1000
	p.Reasons = []string{"BackOff"}
	kql := buildEvents(t, p)

	if !strings.HasSuffix(kql, ";\nBase\n| summarize Total = count()") {
		t.Errorf("unexpected count statement:\n%s", kql)
	}
	if n := strings.Count(kql, "summarize Total"); n != 1 {
		t.Errorf("want exactly one Total statement, got %d", n)
	}
}

func TestBuildEventsKQL_StatementOrderAndProjection(t *testing.T) {
	p := eventParams()
	p.Reasons = []string{"BackOff"}
	kql := buildEvents(t, p)

	statements := strings.Split(kql, ";\n")
	if len(statements) != 4 {
		t.Fatalf("want 4 statements (Base, Boundary, rows, Total), got %d:\n%s", len(statements), kql)
	}
	if !strings.HasPrefix(statements[1], "let Boundary") ||
		!strings.HasPrefix(statements[2], "Base\n| where TimeGenerated") ||
		!strings.HasPrefix(statements[3], "Base\n| summarize Total") {
		t.Errorf("statements out of order:\n%s", kql)
	}
	// Type is a standard Log Analytics column, so the event type is projected
	// under another name.
	assertContains(t, statements[2],
		"Message         = tostring(Body)",
		"EventType       = tostring(SeverityText)",
		`Reason          = tostring(Attributes["k8s.event.reason"])`,
		`ObjectKind      = tostring(ResourceAttributes["k8s.object.kind"])`,
		`EnvironmentUID  = tostring(ResourceAttributes["k8s.object.label.openchoreo.dev/environment-uid"])`,
	)
}

func TestBuildEventsKQL_UsesConfiguredTableAndScope(t *testing.T) {
	p := eventParams()
	p.Reasons = []string{"BackOff"}
	kql, err := BuildEventsKQL(p, "KubeEvents_CL", "custom/scope")
	if err != nil {
		t.Fatalf("BuildEventsKQL: %v", err)
	}
	assertContains(t, baseStatement(t, kql), "let Base = KubeEvents_CL", `| where ScopeName == "custom/scope"`)
}

// Values reach the query only as escaped literals, and only values whose JSON
// form is themselves get a term pre-filter.
func TestBuildEventsKQL_EscapesValues(t *testing.T) {
	p := eventParams()
	p.Component = &ComponentEventScope{Namespace: `acme" or 1==1 //`, ComponentUID: "a\nb"}
	p.Reasons = []string{`Back"Off`, "Killing"}
	base := baseStatement(t, buildEvents(t, p))

	assertContains(t, base,
		`openchoreo.dev/namespace"]) == "acme\" or 1==1 //"`,
		`openchoreo.dev/component-uid"]) == "a b"`,
		`in ("Back\"Off", "Killing")`,
	)
	assertNotContains(t, base, `has "acme`, "has_any", "a\nb")
}

func TestBuildEventsKQL_EscapesWorkflowValues(t *testing.T) {
	p := eventParams()
	p.Workflow = &WorkflowEventScope{Namespace: "acme", WorkflowRunName: `run"`, TaskName: `t\`}
	base := baseStatement(t, buildEvents(t, p))

	assertContains(t, base, `startswith_cs "run\""`, `contains_cs "t\\"`)
}

func TestEventsTableProbeKQL(t *testing.T) {
	if got := EventsTableProbeKQL("OTelLogs"); got != "OTelLogs | take 0" {
		t.Errorf("got %q", got)
	}
}
