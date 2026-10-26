// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-aws-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-aws-cloudwatch/internal/cloudwatch"
)

func makeComponentEventsScopeBody(t *testing.T, scope gen.ComponentSearchScope) gen.EventsQueryRequest_SearchScope {
	t.Helper()
	var s gen.EventsQueryRequest_SearchScope
	if err := s.FromComponentSearchScope(scope); err != nil {
		t.Fatalf("FromComponentSearchScope: %v", err)
	}
	return s
}

func makeWorkflowEventsScopeBody(t *testing.T, scope gen.WorkflowSearchScope) gen.EventsQueryRequest_SearchScope {
	t.Helper()
	var s gen.EventsQueryRequest_SearchScope
	if err := s.FromWorkflowSearchScope(scope); err != nil {
		t.Fatalf("FromWorkflowSearchScope: %v", err)
	}
	return s
}

func TestQueryEventsRejectsNilBody(t *testing.T) {
	handler := newTestHandler(&queryStubClient{}, nil)
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if _, ok := resp.(gen.QueryEvents400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryEventsRejectsEmptyComponentNamespace(t *testing.T) {
	handler := newTestHandler(&queryStubClient{}, nil)
	scope := makeComponentEventsScopeBody(t, gen.ComponentSearchScope{Namespace: ""})
	body := &gen.EventsQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: scope,
	}
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if _, ok := resp.(gen.QueryEvents400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryEventsRejectsWorkflowWithoutRunName(t *testing.T) {
	handler := newTestHandler(&queryStubClient{}, nil)
	emptyRun := ""
	scope := makeWorkflowEventsScopeBody(t, gen.WorkflowSearchScope{Namespace: "default", WorkflowRunName: &emptyRun})
	body := &gen.EventsQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: scope,
	}
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if _, ok := resp.(gen.QueryEvents400JSONResponse); !ok {
		t.Fatalf("expected 400, got %T", resp)
	}
}

func TestQueryEventsComponentReturnsResults(t *testing.T) {
	now := time.Now().UTC()
	client := &queryStubClient{
		componentEventsResult: &cloudwatch.EventsResult{
			Events: []cloudwatch.EventEntry{{
				Timestamp:       now,
				Message:         "Back-off restarting failed container",
				Type:            "Warning",
				Reason:          "BackOff",
				ObjectKind:      "Pod",
				ObjectName:      "reading-list-abc",
				ObjectNamespace: "dp-default",
				ComponentUID:    "33333333-3333-3333-3333-333333333333",
				ComponentName:   "reading-list",
				NamespaceName:   "default",
			}},
			TotalCount: 1,
			Took:       7,
		},
	}
	handler := newTestHandler(client, nil)
	componentUID := "33333333-3333-3333-3333-333333333333"
	scope := makeComponentEventsScopeBody(t, gen.ComponentSearchScope{
		Namespace:    "default",
		ComponentUid: &componentUID,
	})
	body := &gen.EventsQueryRequest{
		StartTime:   now.Add(-time.Hour),
		EndTime:     now,
		SearchScope: scope,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	ok, isOK := resp.(gen.QueryEvents200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200, got %T", resp)
	}
	if ok.Total == nil || *ok.Total != 1 {
		t.Fatalf("unexpected total: %#v", ok.Total)
	}
	if ok.Events == nil || len(*ok.Events) != 1 {
		t.Fatalf("expected 1 event, got %#v", ok.Events)
	}
	got := (*ok.Events)[0]
	if got.Type == nil || *got.Type != "Warning" || got.Reason == nil || *got.Reason != "BackOff" {
		t.Fatalf("unexpected event: %#v", got)
	}
	if got.Metadata == nil || got.Metadata.ObjectKind == nil || *got.Metadata.ObjectKind != "Pod" {
		t.Fatalf("unexpected metadata: %#v", got.Metadata)
	}
	if got.Metadata.ComponentUid == nil {
		t.Fatalf("expected component UID to be parsed")
	}
	// The component scope must reach the client unchanged.
	if client.lastComponentEvents == nil || client.lastComponentEvents.ComponentID != componentUID {
		t.Fatalf("unexpected component params: %#v", client.lastComponentEvents)
	}
}

func TestQueryEventsWorkflowReturnsResults(t *testing.T) {
	now := time.Now().UTC()
	client := &queryStubClient{
		workflowEventsResult: &cloudwatch.EventsResult{
			Events: []cloudwatch.EventEntry{{
				Timestamp:  now,
				Message:    "Created pod",
				Type:       "Normal",
				Reason:     "SuccessfulCreate",
				ObjectKind: "Job",
				ObjectName: "build-123-clone",
			}},
			TotalCount: 1,
		},
	}
	handler := newTestHandler(client, nil)
	runName := "build-123"
	taskName := "clone"
	scope := makeWorkflowEventsScopeBody(t, gen.WorkflowSearchScope{
		Namespace:       "default",
		WorkflowRunName: &runName,
		TaskName:        &taskName,
	})
	body := &gen.EventsQueryRequest{
		StartTime:   now.Add(-time.Hour),
		EndTime:     now,
		SearchScope: scope,
	}

	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if _, ok := resp.(gen.QueryEvents200JSONResponse); !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if client.lastWorkflowEvents == nil || client.lastWorkflowEvents.WorkflowRunName != runName || client.lastWorkflowEvents.TaskName != taskName {
		t.Fatalf("unexpected workflow params: %#v", client.lastWorkflowEvents)
	}
}

func TestQueryEventsComponentReturnsServerErrorOnClientFailure(t *testing.T) {
	handler := newTestHandler(&queryStubClient{componentEventsErr: errors.New("aws boom")}, nil)
	scope := makeComponentEventsScopeBody(t, gen.ComponentSearchScope{Namespace: "default"})
	body := &gen.EventsQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: scope,
	}
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if _, ok := resp.(gen.QueryEvents500JSONResponse); !ok {
		t.Fatalf("expected 500, got %T", resp)
	}
}

// A missing events log group is surfaced by the client as an empty result (not an
// error), so the handler must return 200 with an empty events array.
func TestQueryEventsComponentEmptyResultReturns200(t *testing.T) {
	client := &queryStubClient{componentEventsResult: &cloudwatch.EventsResult{Events: []cloudwatch.EventEntry{}, TotalCount: 0}}
	handler := newTestHandler(client, nil)
	scope := makeComponentEventsScopeBody(t, gen.ComponentSearchScope{Namespace: "default"})
	body := &gen.EventsQueryRequest{
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now(),
		SearchScope: scope,
	}
	resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	ok, isOK := resp.(gen.QueryEvents200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200, got %T", resp)
	}
	if ok.Total == nil || *ok.Total != 0 {
		t.Fatalf("expected total 0, got %#v", ok.Total)
	}
	if ok.Events == nil || len(*ok.Events) != 0 {
		t.Fatalf("expected empty events array, got %#v", ok.Events)
	}
}
