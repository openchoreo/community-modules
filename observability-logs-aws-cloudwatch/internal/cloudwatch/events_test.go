// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudwatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func newEventsTestClient(api *queryStubLogsAPI) *Client {
	return NewClientWithAWS(api, &stubAlarmsAPI{}, &stsStub{}, Config{
		LogGroupName:       "/aws/containerinsights/application",
		EventsLogGroupName: "/aws/containerinsights/events",
		QueryTimeout:       2 * time.Second,
		PollEvery:          5 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestGetComponentEventsMapsAliasedColumns(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	api := &queryStubLogsAPI{
		resultsQueue: [][]cwltypes.ResultField{
			{
				{Field: aws.String("@timestamp"), Value: aws.String(now.Format("2006-01-02 15:04:05.000"))},
				{Field: aws.String("message"), Value: aws.String("Back-off restarting failed container")},
				{Field: aws.String("type"), Value: aws.String("Warning")},
				{Field: aws.String("reason"), Value: aws.String("BackOff")},
				{Field: aws.String("objectKind"), Value: aws.String("Pod")},
				{Field: aws.String("objectName"), Value: aws.String("reading-list-abc")},
				{Field: aws.String("objectNamespace"), Value: aws.String("dp-default")},
				{Field: aws.String("componentUid"), Value: aws.String("33333333-3333-3333-3333-333333333333")},
				{Field: aws.String("componentName"), Value: aws.String("reading-list")},
				{Field: aws.String("namespaceName"), Value: aws.String("default")},
				{Field: aws.String("@ptr"), Value: aws.String("ignore-me")},
			},
		},
	}
	c := newEventsTestClient(api)
	res, err := c.GetComponentEvents(context.Background(), ComponentEventsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err != nil {
		t.Fatalf("GetComponentEvents() error = %v", err)
	}
	if len(res.Events) != 1 || res.TotalCount != 1 {
		t.Fatalf("expected 1 event, got %#v", res)
	}
	got := res.Events[0]
	if got.Message != "Back-off restarting failed container" || got.Type != "Warning" || got.Reason != "BackOff" {
		t.Fatalf("unexpected event fields: %#v", got)
	}
	if got.ObjectKind != "Pod" || got.ObjectName != "reading-list-abc" || got.ObjectNamespace != "dp-default" {
		t.Fatalf("unexpected object metadata: %#v", got)
	}
	if got.ComponentName != "reading-list" || got.ComponentUID != "33333333-3333-3333-3333-333333333333" || got.NamespaceName != "default" {
		t.Fatalf("unexpected scope metadata: %#v", got)
	}
	if !got.Timestamp.Equal(now) {
		t.Fatalf("unexpected timestamp: %s", got.Timestamp)
	}
}

// TestGetComponentEventsMissingLogGroupDegradesGracefully asserts that a missing
// events log group (collector not deployed) yields an empty result, not an error.
func TestGetComponentEventsMissingLogGroupDegradesGracefully(t *testing.T) {
	now := time.Now().UTC()
	api := &queryStubLogsAPI{
		startErr: &cwltypes.ResourceNotFoundException{Message: aws.String("log group does not exist")},
	}
	c := newEventsTestClient(api)
	res, err := c.GetComponentEvents(context.Background(), ComponentEventsParams{
		Namespace: "default",
		StartTime: now.Add(-time.Hour),
		EndTime:   now,
	})
	if err != nil {
		t.Fatalf("expected graceful empty result, got error = %v", err)
	}
	if res == nil || len(res.Events) != 0 || res.TotalCount != 0 {
		t.Fatalf("expected empty result, got %#v", res)
	}
}

func TestGetWorkflowEventsPropagatesNonNotFoundErrors(t *testing.T) {
	now := time.Now().UTC()
	api := &queryStubLogsAPI{startErr: errors.New("throttled")}
	c := newEventsTestClient(api)
	_, err := c.GetWorkflowEvents(context.Background(), WorkflowEventsParams{
		Namespace:       "default",
		WorkflowRunName: "build-1",
		StartTime:       now.Add(-time.Hour),
		EndTime:         now,
	})
	if err == nil {
		t.Fatal("expected non-NotFound error to propagate")
	}
}
