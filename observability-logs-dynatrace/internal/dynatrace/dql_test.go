// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package dynatrace

import (
	"strings"
	"testing"
	"time"
)

func TestStrEscapesLiteral(t *testing.T) {
	tests := map[string]string{
		`plain`:            `"plain"`,
		`say "hi"`:         `"say \"hi\""`,
		`back\slash`:       `"back\\slash"`,
		"line\nbreak\ttab": `"line\nbreak\ttab"`,
		`") | fetch x`:     `"\") | fetch x"`,
	}
	for in, want := range tests {
		if got := str(in); got != want {
			t.Errorf("str(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestFieldQuotesAndStripsBackticks(t *testing.T) {
	if got := field("k8s.object.label.openchoreo.dev/component-uid"); got != "`k8s.object.label.openchoreo.dev/component-uid`" {
		t.Errorf("unexpected field: %s", got)
	}
	if got := field("a`b"); got != "`ab`" {
		t.Errorf("backtick not stripped: %s", got)
	}
}

func TestAnyOf(t *testing.T) {
	if got := anyOf("f", nil); got != "" {
		t.Errorf("empty slice should not filter, got %q", got)
	}
	if got := anyOf("f", []string{"a"}); got != "`f` == \"a\"" {
		t.Errorf("single value: %s", got)
	}
	if got := anyOf("f", []string{"a", "b"}); got != "(`f` == \"a\" or `f` == \"b\")" {
		t.Errorf("multi value: %s", got)
	}
}

func TestArrayHasAny(t *testing.T) {
	if got := arrayHasAny("arr", nil); got != "" {
		t.Errorf("empty slice should not filter, got %q", got)
	}
	if got := arrayHasAny("arr", []string{"x", "y"}); got != "(iAny(`arr`[] == \"x\") or iAny(`arr`[] == \"y\"))" {
		t.Errorf("unexpected: %s", got)
	}
}

func TestAndSkipsEmpty(t *testing.T) {
	if got := and("", "a", "", "b"); got != "a and b" {
		t.Errorf("unexpected: %s", got)
	}
}

func TestSortDirectionWhitelist(t *testing.T) {
	for in, want := range map[string]string{"asc": "asc", "ASC": "asc", "desc": "desc", "": "desc", "asc; drop": "desc"} {
		if got := sortDirection(in); got != want {
			t.Errorf("sortDirection(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestFetchLogsFrom(t *testing.T) {
	if got := fetchLogsFrom("").String(); got != "fetch logs" {
		t.Errorf("unexpected: %s", got)
	}
	if got := fetchLogsFrom("audit_365").String(); got != `fetch logs, bucket: {"audit_365"}` {
		t.Errorf("unexpected: %s", got)
	}
}

func testClient() *Client {
	return NewClient(Config{
		PlatformURL:         "https://env.apps.dynatrace.com",
		ContainerLogsSource: "oc-logs",
		AuditLogsSource:     "oc-audit",
		QueryTimeout:        time.Second,
	}, StaticToken("t"), nil, discardLogger())
}

func TestComponentLogsQueries(t *testing.T) {
	c := testClient()
	records, count, err := c.GenerateComponentLogsQueries(ComponentLogsParams{
		Namespace:     "default",
		ProjectID:     "p-1",
		EnvironmentID: "e-1",
		ComponentID:   "c-1",
		SearchPhrase:  `"quoted"`,
		Limit:         50,
		SortOrder:     "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "fetch logs\n" +
		"| filter `log.source` == \"oc-logs\"\n" +
		"| filter `openchoreo.namespace` == \"default\" and `openchoreo.project_uid` == \"p-1\" and " +
		"`openchoreo.environment_uid` == \"e-1\" and `openchoreo.component_uid` == \"c-1\" and " +
		"contains(`content`, \"\\\"quoted\\\"\", caseSensitive: false)\n" +
		"| sort `timestamp` asc\n" +
		"| limit 50\n" +
		"| fields `timestamp`, `content`, `k8s.namespace.name`, `k8s.pod.name`, `k8s.container.name`, " +
		"`openchoreo.namespace`, `openchoreo.project`, `openchoreo.project_uid`, `openchoreo.component`, " +
		"`openchoreo.component_uid`, `openchoreo.environment`, `openchoreo.environment_uid`"
	if records != want {
		t.Errorf("records query:\n%s\nwant:\n%s", records, want)
	}
	if !strings.HasSuffix(count, "| summarize total = count()") || strings.Contains(count, "limit") {
		t.Errorf("count query: %s", count)
	}
}

func TestComponentLogsRequiresNamespace(t *testing.T) {
	if _, _, err := testClient().GenerateComponentLogsQueries(ComponentLogsParams{}); err == nil {
		t.Fatal("expected error")
	}
	if _, _, err := testClient().GenerateWorkflowLogsQueries(WorkflowLogsParams{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestWorkflowLogsQueries(t *testing.T) {
	records, _, err := testClient().GenerateWorkflowLogsQueries(WorkflowLogsParams{
		Namespace: "default", WorkflowRunName: "build-1", LogLevels: []string{"ERROR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"`k8s.namespace.name` == \"workflows-default\"",
		"`openchoreo.workflow_run` == \"build-1\"",
		"contains(`content`, \"error\", caseSensitive: false)",
		"| limit 100",
		"| sort `timestamp` desc",
	} {
		if !strings.Contains(records, want) {
			t.Errorf("records query missing %q:\n%s", want, records)
		}
	}
}

func TestClampLimit(t *testing.T) {
	for in, want := range map[int]int{0: 100, -1: 100, 5: 5, 1000: 1000, 5000: 1000} {
		if got := clampLimit(in); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestPlatformQueries(t *testing.T) {
	c := testClient()
	records, count := c.GeneratePlatformLogsQueries(PlatformLogsParams{
		ClusterInstances: []string{"c1", "c2"},
		Namespaces:       []string{"ns"},
		Labels:           map[string]string{"z": "1", "app.kubernetes.io/name": "api"},
	})
	for _, want := range []string{
		"(`k8s.cluster.name` == \"c1\" or `k8s.cluster.name` == \"c2\")",
		"`k8s.namespace.name` == \"ns\"",
		// sorted: app.kubernetes.io/name before z
		"iAny(`k8s.pod.labels`[] == \"app.kubernetes.io/name=api\") and iAny(`k8s.pod.labels`[] == \"z=1\")",
		"`k8s.pod.labels`",
	} {
		if !strings.Contains(records, want) {
			t.Errorf("records query missing %q:\n%s", want, records)
		}
	}
	if !strings.Contains(count, "summarize total = count()") {
		t.Errorf("count query: %s", count)
	}
}

func TestPlatformFilterValuesQueries(t *testing.T) {
	c := testClient()
	values, total := c.GeneratePlatformFilterValuesQueries(PlatformLogsParams{Namespaces: []string{"ns"}}, fPodName, "api", 10)
	for _, want := range []string{
		"isNotNull(`k8s.pod.name`) and `k8s.pod.name` != \"\"",
		"contains(`k8s.pod.name`, \"api\", caseSensitive: false)",
		"summarize record_count = count(), by: {value = `k8s.pod.name`}",
		"sort record_count desc, value asc",
		"limit 10",
	} {
		if !strings.Contains(values, want) {
			t.Errorf("values query missing %q:\n%s", want, values)
		}
	}
	if !strings.Contains(total, "countDistinctExact(`k8s.pod.name`)") {
		t.Errorf("total query: %s", total)
	}
}

func TestPlatformFilterFieldAndClear(t *testing.T) {
	if f, ok := PlatformFilterField("podName"); !ok || f != fPodName {
		t.Errorf("podName -> %s %v", f, ok)
	}
	if _, ok := PlatformFilterField("labels"); ok {
		t.Error("labels should not be listable")
	}
	p := PlatformLogsParams{
		ClusterInstances: []string{"a"}, Namespaces: []string{"b"}, PodNames: []string{"c"}, ContainerNames: []string{"d"},
	}
	for _, f := range []string{"clusterInstance", "namespace", "podName", "containerName"} {
		cleared := ClearFilterSelections(p, f)
		n := len(cleared.ClusterInstances) + len(cleared.Namespaces) + len(cleared.PodNames) + len(cleared.ContainerNames)
		if n != 3 {
			t.Errorf("clearing %s left %d selections, want 3", f, n)
		}
	}
}

func TestEventsQueries(t *testing.T) {
	records, count, err := GenerateEventsQueries(EventsQueryParams{
		Component: &ComponentEventsScope{Namespace: "ns", ComponentID: "c-1"},
		Reasons:   []string{"Started"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"isNotNull(`k8s.event.reason`)",
		"`k8s.object.label.openchoreo.dev/namespace` == \"ns\"",
		"`k8s.object.label.openchoreo.dev/component-uid` == \"c-1\"",
		"`k8s.event.reason` == \"Started\"",
	} {
		if !strings.Contains(records, want) {
			t.Errorf("records query missing %q:\n%s", want, records)
		}
	}
	if strings.Contains(records, "log.source") {
		t.Error("events are not scoped by log.source")
	}
	if !strings.Contains(count, "summarize total = count()") {
		t.Errorf("count: %s", count)
	}

	records, _, err = GenerateEventsQueries(EventsQueryParams{
		Workflow: &WorkflowEventsScope{Namespace: "ns", WorkflowRunName: "run-1", TaskName: "build"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"startsWith(`k8s.object.name`, \"run-1\")",
		"`k8s.namespace.name` == \"workflows-ns\"",
		"contains(`k8s.object.name`, \"build\", caseSensitive: true)",
	} {
		if !strings.Contains(records, want) {
			t.Errorf("workflow query missing %q:\n%s", want, records)
		}
	}
}

func TestEventsQueriesRejectInvalidScopes(t *testing.T) {
	cases := []EventsQueryParams{
		{},
		{Component: &ComponentEventsScope{}},
		{Workflow: &WorkflowEventsScope{Namespace: "ns"}},
	}
	for i, p := range cases {
		if _, _, err := GenerateEventsQueries(p); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if _, _, err := GenerateEventsQueries(EventsQueryParams{Reasons: []string{"X"}}); err != nil {
		t.Errorf("unscoped with reasons should be allowed: %v", err)
	}
}

func TestAuditQueries(t *testing.T) {
	c := testClient()
	c.auditBucket = "audit_365"
	records, count := c.GenerateAuditLogsQueries(AuditLogsParams{
		ActorIDs:          []string{"alice"},
		ActorEntitlements: []string{"admins"},
		Results:           []string{"denied"},
		SearchPhrase:      "Create",
		SortOrder:         "asc",
		Limit:             10,
	})
	for _, want := range []string{
		`fetch logs, bucket: {"audit_365"}`,
		"`log.source` == \"oc-audit\"",
		"`audit.actor.id` == \"alice\"",
		"iAny(`audit.actor.entitlements`[] == \"admins\")",
		"`audit.result` == \"denied\"",
		"contains(`content`, \"Create\", caseSensitive: true)",
		"sort `timestamp` asc, `audit.event_id` asc",
		"limit 10",
	} {
		if !strings.Contains(records, want) {
			t.Errorf("records query missing %q:\n%s", want, records)
		}
	}
	if !strings.HasPrefix(count, `fetch logs, bucket: {"audit_365"}`) {
		t.Errorf("count query not bucket-scoped: %s", count)
	}
}

func TestAuditTimelineQuery(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := testClient().GenerateAuditTimelineQuery(AuditLogsParams{StartTime: start}, 15*time.Minute)
	want := "summarize record_count = count(), by: {bucket = toLong(floor((unixMillisFromTimestamp(`timestamp`) - 1767225600000) / 900000)), result = `audit.result`}"
	if !strings.Contains(q, want) {
		t.Errorf("timeline query:\n%s\nwant to contain:\n%s", q, want)
	}
}

func TestAuditFilterValuesQueries(t *testing.T) {
	c := testClient()
	values, total, err := c.GenerateAuditFilterValuesQueries(AuditLogsParams{}, "action", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(values, "by: {value = `audit.action`}") || !strings.Contains(total, "countDistinctExact(`audit.action`)") {
		t.Errorf("unexpected queries:\n%s\n%s", values, total)
	}

	values, total, err = c.GenerateAuditFilterValuesQueries(AuditLogsParams{}, AuditEntitlementsFilter, "adm", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"fieldsAdd value = arrayDistinct(`audit.actor.entitlements`)",
		"expand value",
		"contains(value, \"adm\", caseSensitive: false)",
		"summarize record_count = count(), by: {value}",
	} {
		if !strings.Contains(values, want) {
			t.Errorf("entitlements values query missing %q:\n%s", want, values)
		}
	}
	if !strings.Contains(total, "countDistinctExact(value)") {
		t.Errorf("entitlements total: %s", total)
	}

	if _, _, err := c.GenerateAuditFilterValuesQueries(AuditLogsParams{}, "event_id", "", 5); err == nil {
		t.Error("event_id should not be listable")
	}
}
