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

func TestBuildComponentLogsQueryV1_ReleaseLine(t *testing.T) {
	qb := NewQueryBuilder("logs-")
	params := ComponentLogsQueryParamsV1{
		StartTime: "2025-06-15T00:00:00Z", EndTime: "2025-06-15T23:59:59Z",
		NamespaceName: "test-ns", ComponentID: "comp-uid", Limit: 10,
	}
	want := `"kubernetes.labels.openchoreo_dev/release-line-uid":"` + testReleaseLineID + `"`

	query, err := qb.BuildComponentLogsQueryV1(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(queryJSON(t, query), "release-line-uid") {
		t.Errorf("query without a release line filters on one: %s", queryJSON(t, query))
	}

	params.ReleaseLineID = testReleaseLineID
	query, err = qb.BuildComponentLogsQueryV1(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(queryJSON(t, query), want) {
		t.Errorf("missing release line term %s in %s", want, queryJSON(t, query))
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
	if strings.Contains(queryJSON(t, query), "release-line-uid") {
		t.Errorf("rule without a release line filters on one: %s", queryJSON(t, query))
	}

	params.Metadata.ReleaseLineUID = testReleaseLineID
	query, err = qb.BuildLogAlertingRuleQuery(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `"kubernetes.labels.openchoreo_dev/release-line-uid":{"boost":1,"value":"` + testReleaseLineID + `"}`
	if !strings.Contains(queryJSON(t, query), want) {
		t.Errorf("missing release line term %s in %s", want, queryJSON(t, query))
	}
}
