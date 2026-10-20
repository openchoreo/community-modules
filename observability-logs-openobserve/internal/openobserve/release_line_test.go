// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

const testReleaseLineID = "93f65294-926f-475b-98f1-dc4f152f38da"

func TestGenerateComponentLogsQuery_ReleaseLine(t *testing.T) {
	params := ComponentLogsParams{
		Namespace: "test-ns", ComponentIDs: []string{"comp-uid"},
		StartTime: evStart, EndTime: evEnd, Limit: 10,
	}
	want := "kubernetes_labels_openchoreo_dev_release_line_uid = '" + testReleaseLineID + "'"

	for name, gen := range map[string]func(ComponentLogsParams, string, *slog.Logger) ([]byte, error){
		"query": generateComponentLogsQuery,
		"count": generateComponentLogsCountQuery,
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := gen(params, "default", testLogger())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sql, _ := sqlOf(t, raw); strings.Contains(sql, "release_line_uid") {
				t.Errorf("query without a release line filters on one: %s", sql)
			}

			p := params
			p.ReleaseLineID = testReleaseLineID
			raw, err = gen(p, "default", testLogger())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sql, _ := sqlOf(t, raw); !strings.Contains(sql, want) {
				t.Errorf("missing release line condition %q in %s", want, sql)
			}
		})
	}
}

func TestGenerateComponentEventsQuery_ReleaseLine(t *testing.T) {
	params := EventsQueryParams{
		Namespace: "default", ComponentID: "comp-uid",
		StartTime: evStart, EndTime: evEnd, Limit: 50,
	}
	want := "k8s_object_label_openchoreo_dev_release_line_uid = '" + testReleaseLineID + "'"

	raw, err := generateComponentEventsQuery(params, "k8s_events", testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql, _ := sqlOf(t, raw); strings.Contains(sql, "release_line_uid") {
		t.Errorf("query without a release line filters on one: %s", sql)
	}

	params.ReleaseLineID = testReleaseLineID
	for name, gen := range map[string]func(EventsQueryParams, string, *slog.Logger) ([]byte, error){
		"query": generateComponentEventsQuery,
		"count": generateComponentEventsCountQuery,
	} {
		raw, err := gen(params, "k8s_events", testLogger())
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if sql, _ := sqlOf(t, raw); !strings.Contains(sql, want) {
			t.Errorf("%s: missing release line condition %q in %s", name, want, sql)
		}
	}
}

func TestGenerateAlertConfig_ReleaseLine(t *testing.T) {
	enabled := true
	name := "rule"
	params := LogAlertParams{
		Name: &name, Namespace: "ns", ProjectUID: "proj-uid", EnvironmentUID: "env-uid", ComponentUID: "comp-uid",
		SearchPattern: "error", Operator: "gt", ThresholdValue: 1, Window: "5m", Interval: "1m", Enabled: &enabled,
	}

	alert := func(t *testing.T, p LogAlertParams) (string, map[string]interface{}) {
		t.Helper()
		raw, err := generateAlertConfig(p, "default", testLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var cfg map[string]interface{}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		sql, _ := cfg["query_condition"].(map[string]interface{})["sql"].(string)
		attrs, _ := cfg["context_attributes"].(map[string]interface{})
		return sql, attrs
	}

	sql, attrs := alert(t, params)
	if strings.Contains(sql, "release_line_uid") {
		t.Errorf("rule without a release line filters on one: %s", sql)
	}
	if _, ok := attrs["releaseLineUid"]; ok {
		t.Errorf("rule without a release line stores one: %v", attrs)
	}

	params.ReleaseLineUID = testReleaseLineID
	sql, attrs = alert(t, params)
	want := "kubernetes_labels_openchoreo_dev_release_line_uid = '" + testReleaseLineID + "'"
	if !strings.Contains(sql, want) {
		t.Errorf("missing release line condition %q in %s", want, sql)
	}
	if got := ExtractSearchPattern(sql); got != "error" {
		t.Errorf("search pattern = %q, want %q", got, "error")
	}
	if attrs["releaseLineUid"] != testReleaseLineID {
		t.Errorf("context_attributes.releaseLineUid = %v, want %s", attrs["releaseLineUid"], testReleaseLineID)
	}
}

func TestAuditConditions_FiltersByReleaseLine(t *testing.T) {
	conditions, ok := auditConditions(AuditLogsParams{
		ResourceComponents:   []string{"hello"},
		ResourceReleaseLines: []string{"next"},
	}, auditColumns("resource_component", "resource_release_line"))
	if !ok {
		t.Fatal("expected the conditions to be buildable")
	}
	want := []string{`(resource_component = 'hello')`, `(resource_release_line = 'next')`}
	if strings.Join(conditions, " AND ") != strings.Join(want, " AND ") {
		t.Errorf("got %v, want %v", conditions, want)
	}
}

func TestAuditValuesSource_ReadsTheReleaseLineColumn(t *testing.T) {
	source, ok := auditValuesSource("audit_logs", "resource.release_line", nil, auditColumns("resource_release_line"))
	if !ok {
		t.Fatal("expected a source")
	}
	if !strings.Contains(source, "resource_release_line as value") {
		t.Errorf("source = %s, want it to read resource_release_line", source)
	}
}

func TestParseAuditRecord_ReadsTheReleaseLine(t *testing.T) {
	line := strings.Replace(auditLine, `"resource":{`, `"resource":{"release_line":"next",`, 1)
	record, err := ParseAuditRecord(map[string]interface{}{"log": line})
	if err != nil {
		t.Fatal(err)
	}
	if record.Resource == nil || record.Resource.ReleaseLine != "next" {
		t.Errorf("resource = %+v, want release_line next", record.Resource)
	}
}
