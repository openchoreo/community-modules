// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

func TestWriteReleaseLineScopeFilter(t *testing.T) {
	f := releaseLineFields{component: "`c`", line: "`l`", lineComponent: "`lc`"}
	tests := []struct {
		name         string
		componentIDs []string
		lineID       string
		allLines     bool
		want         string
	}{
		{name: "no component or line"},
		{name: "every line without a component", allLines: true},
		{name: "one line matches on its UID alone", componentIDs: []string{"comp"}, lineID: "line",
			want: "| filter `l` = \"line\"\n"},
		{name: "one line takes precedence over every line", componentIDs: []string{"comp"}, lineID: "line", allLines: true,
			want: "| filter `l` = \"line\"\n"},
		{name: "main line", componentIDs: []string{"comp"},
			want: "| filter `c` = \"comp\"\n| filter not ispresent(`l`)\n"},
		{name: "main line of several components", componentIDs: []string{"c1", "c2"},
			want: "| filter `c` = \"c1\" or `c` = \"c2\"\n| filter not ispresent(`l`)\n"},
		{name: "every line", componentIDs: []string{"comp"}, allLines: true,
			want: "| filter `c` = \"comp\" or `lc` = \"comp\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeReleaseLineScopeFilter(&b, f, tt.lineID, tt.allLines, tt.componentIDs...)
			if got := b.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildComponentQueryReleaseLine(t *testing.T) {
	params := ComponentLogsParams{Namespace: "default", ComponentIDs: []string{"comp-uid"}}

	if got := buildComponentQuery(params); !strings.Contains(got,
		"| filter `kubernetes.labels.openchoreo.dev/component-uid` = \"comp-uid\"\n"+
			"| filter not ispresent(`kubernetes.labels.openchoreo.dev/release-line-uid`)\n") {
		t.Errorf("a component query must cover its main line only:\n%s", got)
	}

	params.AllReleaseLines = true
	if got := buildComponentQuery(params); !strings.Contains(got,
		"| filter `kubernetes.labels.openchoreo.dev/component-uid` = \"comp-uid\" or "+
			"`kubernetes.labels.openchoreo.dev/release-line-component-uid` = \"comp-uid\"\n") ||
		strings.Contains(got, "ispresent") {
		t.Errorf("an every line query must cover its release lines' pods:\n%s", got)
	}

	params.ReleaseLineID = testReleaseLineUID
	got := buildComponentQuery(params)
	if !strings.Contains(got, "| filter `kubernetes.labels.openchoreo.dev/release-line-uid` = \""+testReleaseLineUID+"\"\n") ||
		strings.Contains(got, "| filter `kubernetes.labels.openchoreo.dev/component-uid`") {
		t.Errorf("a release line query must match the line UID alone:\n%s", got)
	}
	if !strings.Contains(got, "`kubernetes.labels.openchoreo.dev/release-line-component-uid` as releaseLineComponentUid") {
		t.Errorf("the query must project the release line component UID:\n%s", got)
	}
}

func TestBuildComponentEventsQueryReleaseLine(t *testing.T) {
	params := ComponentEventsParams{Namespace: "default", ComponentID: "comp-uid"}

	if got := buildComponentEventsQuery(params); !strings.Contains(got,
		"| filter `resource.k8s.object.label.openchoreo.dev/component-uid` = \"comp-uid\"\n"+
			"| filter not ispresent(`resource.k8s.object.label.openchoreo.dev/release-line-uid`)\n") {
		t.Errorf("a component query must cover its main line only:\n%s", got)
	}

	params.AllReleaseLines = true
	if got := buildComponentEventsQuery(params); !strings.Contains(got,
		"| filter `resource.k8s.object.label.openchoreo.dev/component-uid` = \"comp-uid\" or "+
			"`resource.k8s.object.label.openchoreo.dev/release-line-component-uid` = \"comp-uid\"\n") {
		t.Errorf("an every line query must cover its release lines' objects:\n%s", got)
	}

	params.ReleaseLineID = testReleaseLineUID
	got := buildComponentEventsQuery(params)
	if !strings.Contains(got, "| filter `resource.k8s.object.label.openchoreo.dev/release-line-uid` = \""+testReleaseLineUID+"\"\n") ||
		strings.Contains(got, "| filter `resource.k8s.object.label.openchoreo.dev/component-uid`") {
		t.Errorf("a release line query must match the line UID alone:\n%s", got)
	}
}

// A release line's records name their component through the release line component UID label.
func TestGetComponentLogsAttributesReleaseLineToComponent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	api := &queryStubLogsAPI{
		resultsQueue: [][]cwltypes.ResultField{{
			{Field: aws.String("@timestamp"), Value: aws.String(now.Format("2006-01-02 15:04:05.000"))},
			{Field: aws.String("@message"), Value: aws.String("hello")},
			{Field: aws.String("releaseLineComponentUid"), Value: aws.String("33333333-3333-3333-3333-333333333333")},
		}},
	}
	res, err := newQueryTestClient(api).GetComponentLogs(context.Background(), ComponentLogsParams{
		Namespace: "default", StartTime: now.Add(-time.Hour), EndTime: now,
	})
	if err != nil {
		t.Fatalf("GetComponentLogs() error = %v", err)
	}
	if got := res.Logs[0].ComponentUID; got != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("ComponentUID = %q, want the release line component UID", got)
	}
}

func TestGetComponentEventsAttributesReleaseLineToComponent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	api := &queryStubLogsAPI{
		resultsQueue: [][]cwltypes.ResultField{{
			{Field: aws.String("@timestamp"), Value: aws.String(now.Format("2006-01-02 15:04:05.000"))},
			{Field: aws.String("releaseLineComponentUid"), Value: aws.String("33333333-3333-3333-3333-333333333333")},
		}},
	}
	res, err := newEventsTestClient(api).GetComponentEvents(context.Background(), ComponentEventsParams{
		Namespace: "default", StartTime: now.Add(-time.Hour), EndTime: now,
	})
	if err != nil {
		t.Fatalf("GetComponentEvents() error = %v", err)
	}
	if got := res.Events[0].ComponentUID; got != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("ComponentUID = %q, want the release line component UID", got)
	}
}

func TestBuildAlertFilterPatternReleaseLine(t *testing.T) {
	params := LogAlertParams{EnvironmentUID: "env-1", ComponentUID: "comp-1", SearchPattern: "ERROR"}

	got, err := BuildAlertFilterPattern(params)
	if err != nil {
		t.Fatalf("BuildAlertFilterPattern() error = %v", err)
	}
	if want := `{ ($.kubernetes.labels.* = "env-1") && ($.kubernetes.labels.['openchoreo.dev/component-uid'] = "comp-1") && ($.kubernetes.labels.['openchoreo.dev/release-line-uid'] NOT EXISTS) && ($.log = "*ERROR*") }`; got != want {
		t.Errorf("rule without a line: pattern = %s, want %s", got, want)
	}

	params.ReleaseLineUID = testReleaseLineUID
	got, err = BuildAlertFilterPattern(params)
	if err != nil {
		t.Fatalf("BuildAlertFilterPattern() error = %v", err)
	}
	if want := `{ ($.kubernetes.labels.* = "env-1") && ($.kubernetes.labels.['openchoreo.dev/release-line-uid'] = "` + testReleaseLineUID + `") && ($.log = "*ERROR*") }`; got != want {
		t.Errorf("release line rule: pattern = %s, want %s", got, want)
	}
}

func TestCreateAlertTagsReleaseLine(t *testing.T) {
	for name, lineUID := range map[string]string{"main line": "", "release line": testReleaseLineUID} {
		t.Run(name, func(t *testing.T) {
			order := []string{}
			alarms := &stubAlarmsAPI{order: &order}
			client := newTestClient(&stubLogsAPI{order: &order}, alarms)

			params := validAlertParams()
			params.ReleaseLineUID = lineUID
			if _, err := client.CreateAlert(context.Background(), params); err != nil {
				t.Fatalf("CreateAlert() error = %v", err)
			}
			tags := map[string]string{}
			for _, tag := range alarms.putMetricAlarmInput.Tags {
				tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
			}
			if got, ok := tags[TagReleaseLineUID]; got != lineUID || ok != (lineUID != "") {
				t.Errorf("%s tag = %q (present %v), want %q", TagReleaseLineUID, got, ok, lineUID)
			}
			if got := tags[TagComponentUID]; got != params.ComponentUID {
				t.Errorf("%s tag = %q, want %q", TagComponentUID, got, params.ComponentUID)
			}
		})
	}
}
