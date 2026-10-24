// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

func TestBuildComponentQueryReleaseLineFilter(t *testing.T) {
	filter := "| filter `kubernetes.labels.openchoreo.dev/release-line-uid` = \"" + testReleaseLineUID + "\""

	withLine := buildComponentQuery(ComponentLogsParams{Namespace: "default", ComponentIDs: []string{"c1"}, ReleaseLineID: testReleaseLineUID})
	if !strings.Contains(withLine, filter) {
		t.Errorf("expected release line filter, got:\n%s", withLine)
	}

	withoutLine := buildComponentQuery(ComponentLogsParams{Namespace: "default", ComponentIDs: []string{"c1"}})
	if strings.Contains(withoutLine, "release-line-uid") {
		t.Errorf("expected no release line filter, got:\n%s", withoutLine)
	}
}

func TestBuildComponentEventsQueryReleaseLineFilter(t *testing.T) {
	filter := "| filter `resource.k8s.object.label.openchoreo.dev/release-line-uid` = \"" + testReleaseLineUID + "\""

	withLine := buildComponentEventsQuery(ComponentEventsParams{Namespace: "default", ComponentID: "c1", ReleaseLineID: testReleaseLineUID})
	if !strings.Contains(withLine, filter) {
		t.Errorf("expected release line filter, got:\n%s", withLine)
	}

	withoutLine := buildComponentEventsQuery(ComponentEventsParams{Namespace: "default", ComponentID: "c1"})
	if strings.Contains(withoutLine, "release-line-uid") {
		t.Errorf("expected no release line filter, got:\n%s", withoutLine)
	}
}

func TestBuildAlertFilterPatternReleaseLine(t *testing.T) {
	base := LogAlertParams{EnvironmentUID: "env-1", ComponentUID: "comp-1", SearchPattern: "ERROR"}
	lineCond := `($.kubernetes.labels.* = "` + testReleaseLineUID + `")`

	withLine := base
	withLine.ReleaseLineUID = testReleaseLineUID
	got, err := BuildAlertFilterPattern(withLine)
	if err != nil {
		t.Fatalf("BuildAlertFilterPattern() error = %v", err)
	}
	want := `{ ($.kubernetes.labels.* = "env-1") && ($.kubernetes.labels.* = "comp-1") && ` + lineCond + ` && ($.log = "*ERROR*") }`
	if got != want {
		t.Errorf("pattern = %s, want %s", got, want)
	}

	got, err = BuildAlertFilterPattern(base)
	if err != nil {
		t.Fatalf("BuildAlertFilterPattern() error = %v", err)
	}
	if strings.Contains(got, testReleaseLineUID) {
		t.Errorf("expected no release line condition, got %s", got)
	}
}

func TestCreateAlertScopesFilterAndTagsToReleaseLine(t *testing.T) {
	order := []string{}
	logs := &stubLogsAPI{order: &order}
	alarms := &stubAlarmsAPI{order: &order}
	client := newTestClient(logs, alarms)

	params := validAlertParams()
	params.ReleaseLineUID = testReleaseLineUID
	if _, err := client.CreateAlert(context.Background(), params); err != nil {
		t.Fatalf("CreateAlert() error = %v", err)
	}
	if pattern := aws.ToString(logs.putMetricFilterInput.FilterPattern); !strings.Contains(pattern, testReleaseLineUID) {
		t.Errorf("filter pattern missing release line: %s", pattern)
	}
	var tagged bool
	for _, tag := range alarms.putMetricAlarmInput.Tags {
		if aws.ToString(tag.Key) == TagReleaseLineUID && aws.ToString(tag.Value) == testReleaseLineUID {
			tagged = true
		}
	}
	if !tagged {
		t.Errorf("alarm tags missing %s", TagReleaseLineUID)
	}
}
