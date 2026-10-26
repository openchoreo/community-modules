// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"strings"
	"testing"
)

const testReleaseLineID = "93f65294-926f-475b-98f1-dc4f152f38da"

func queryJSON(t *testing.T, query map[string]interface{}) string {
	t.Helper()
	b, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	return string(b)
}

func TestReleaseLineScopeFilter(t *testing.T) {
	f := releaseLineFields{component: "c.uid", line: "l.uid", lineComponent: "lc.uid"}
	tests := []struct {
		name        string
		componentID string
		lineID      string
		allLines    bool
		want        string
	}{
		{name: "no component or line", want: "null"},
		{name: "every line without a component", allLines: true, want: "null"},
		{name: "one line matches on its UID alone", componentID: "comp-uid", lineID: "next-uid",
			want: `{"term":{"l.uid":"next-uid"}}`},
		{name: "one line takes precedence over every line", componentID: "comp-uid", lineID: "next-uid", allLines: true,
			want: `{"term":{"l.uid":"next-uid"}}`},
		{name: "default line", componentID: "comp-uid",
			want: `{"bool":{"filter":[{"term":{"c.uid":"comp-uid"}}],"must_not":[{"exists":{"field":"l.uid"}}]}}`},
		{name: "every line", componentID: "comp-uid", allLines: true,
			want: `{"bool":{"minimum_should_match":1,"should":[{"term":{"c.uid":"comp-uid"}},{"term":{"lc.uid":"comp-uid"}}]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := queryJSON(t, releaseLineScopeFilter(f, tt.componentID, tt.lineID, tt.allLines))
			if got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBuildComponentLogsQueryV1_ReleaseLine(t *testing.T) {
	qb := NewQueryBuilder("logs-")
	params := ComponentLogsQueryParamsV1{
		StartTime: "2025-06-15T00:00:00Z", EndTime: "2025-06-15T23:59:59Z",
		NamespaceName: "test-ns", ComponentID: "comp-uid", Limit: 10,
	}
	build := func() string {
		t.Helper()
		query, err := qb.BuildComponentLogsQueryV1(params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return queryJSON(t, query)
	}

	if got := build(); !strings.Contains(got, `"must_not":[{"exists":{"field":"kubernetes.labels.openchoreo_dev/release-line-uid"}}]`) ||
		strings.Contains(got, "release-line-component-uid") {
		t.Errorf("a component query must cover its default line only: %s", got)
	}

	params.AllReleaseLines = true
	if got := build(); !strings.Contains(got, `{"term":{"kubernetes.labels.openchoreo_dev/release-line-component-uid":"comp-uid"}}`) {
		t.Errorf("an every line query must cover its release lines' pods: %s", got)
	}

	params.ReleaseLineID = testReleaseLineID
	if got := build(); !strings.Contains(got, `{"term":{"kubernetes.labels.openchoreo_dev/release-line-uid":"`+testReleaseLineID+`"}}`) ||
		strings.Contains(got, "component-uid") {
		t.Errorf("a release line query must match the line UID alone: %s", got)
	}
}

func TestBuildComponentEventsQuery_ReleaseLine(t *testing.T) {
	qb := NewQueryBuilder("k8s-events-")
	query, err := qb.BuildComponentEventsQuery(EventsQueryParams{
		StartTime: "2026-06-05T00:00:00Z", EndTime: "2026-06-06T00:00:00Z",
		NamespaceName: "default", ComponentID: "comp-uid", ReleaseLineID: testReleaseLineID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasTermFilter(mustConditions(t, query), EvReleaseLineID, testReleaseLineID) {
		t.Errorf("missing release line term on %s", EvReleaseLineID)
	}
	if strings.Contains(queryJSON(t, query), "component-uid") {
		t.Errorf("release line query must not filter on the component UID: %s", queryJSON(t, query))
	}
	if EvReleaseLineID != "resource.k8s.object.label.openchoreo.dev/release-line-uid" {
		t.Errorf("unexpected release-line-uid field path: %s", EvReleaseLineID)
	}
}

func TestBuildLogAlertingRuleQuery_ReleaseLine(t *testing.T) {
	qb := NewQueryBuilder("logs-")
	params := AlertingRuleRequest{
		Metadata: AlertingRuleMetadata{
			Name: "rule", ComponentUID: "comp-uid", ProjectUID: "proj-uid", EnvironmentUID: "env-uid",
		},
		Source:    AlertingRuleSource{Query: "error"},
		Condition: AlertingRuleCondition{Window: "5m"},
	}

	query, err := qb.BuildLogAlertingRuleQuery(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := queryJSON(t, query); !strings.Contains(got, `"must_not":[{"exists":{"field":"kubernetes.labels.openchoreo_dev/release-line-uid"}}]`) {
		t.Errorf("a default line rule must exclude release line documents: %s", got)
	}

	params.Metadata.ReleaseLineUID = testReleaseLineID
	query, err = qb.BuildLogAlertingRuleQuery(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"term":{"kubernetes.labels.openchoreo_dev/release-line-uid":"` + testReleaseLineID + `"}}`
	if got := queryJSON(t, query); !strings.Contains(got, want) || strings.Contains(got, "component-uid") {
		t.Errorf("a release line rule must match the line UID alone: %s", got)
	}
}

// A release line's log and event entries are attributed to their component like the default line's.
func TestParseEntries_ReleaseLineComponentUID(t *testing.T) {
	const componentUID = "d2108e2c-f912-4cab-a0b4-71afde25c5ac"
	tests := map[string]struct{ logLabels, eventLabels map[string]interface{} }{
		"default line": {
			logLabels:   map[string]interface{}{ReplaceDots(ComponentID): componentUID},
			eventLabels: map[string]interface{}{strings.TrimPrefix(EvComponentID, "resource."): componentUID},
		},
		"release line": {
			logLabels: map[string]interface{}{
				ReplaceDots(ReleaseLineComponentID): componentUID, ReplaceDots(ReleaseLineID): testReleaseLineID,
			},
			eventLabels: map[string]interface{}{
				strings.TrimPrefix(EvReleaseLineComponentID, "resource."): componentUID,
				strings.TrimPrefix(EvReleaseLineID, "resource."):          testReleaseLineID,
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			log := ParseLogEntry(Hit{Source: map[string]interface{}{
				"kubernetes": map[string]interface{}{"labels": tt.logLabels},
			}})
			if log.ComponentID != componentUID {
				t.Errorf("log entry component UID = %q, want %q", log.ComponentID, componentUID)
			}
			event := ParseEventHit(Hit{Source: map[string]interface{}{"resource": tt.eventLabels}})
			if event.ComponentID != componentUID {
				t.Errorf("event entry component UID = %q, want %q", event.ComponentID, componentUID)
			}
		})
	}
}
