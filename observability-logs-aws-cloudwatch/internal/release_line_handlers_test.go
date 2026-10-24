// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/openchoreo/community-modules/observability-logs-aws-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-aws-cloudwatch/internal/cloudwatch"
)

const testReleaseLineUID = "93f65294-926f-475b-98f1-dc4f152f38da"

type releaseLineStubClient struct {
	stubLogsClient
	logs   *cloudwatch.ComponentLogsParams
	events *cloudwatch.ComponentEventsParams
	alert  *cloudwatch.LogAlertParams
}

func (c *releaseLineStubClient) GetComponentLogs(_ context.Context, p cloudwatch.ComponentLogsParams) (*cloudwatch.ComponentLogsResult, error) {
	c.logs = &p
	return &cloudwatch.ComponentLogsResult{}, nil
}

func (c *releaseLineStubClient) GetWorkflowLogs(_ context.Context, _ cloudwatch.WorkflowLogsParams) (*cloudwatch.WorkflowLogsResult, error) {
	return &cloudwatch.WorkflowLogsResult{}, nil
}

func (c *releaseLineStubClient) GetComponentEvents(_ context.Context, p cloudwatch.ComponentEventsParams) (*cloudwatch.EventsResult, error) {
	c.events = &p
	return &cloudwatch.EventsResult{}, nil
}

func (c *releaseLineStubClient) GetAlert(context.Context, string, string) (*cloudwatch.AlertDetail, error) {
	if c.alert == nil {
		return nil, cloudwatch.ErrAlertNotFound
	}
	return &cloudwatch.AlertDetail{Name: c.alert.Name}, nil
}

func (c *releaseLineStubClient) CreateAlert(_ context.Context, p cloudwatch.LogAlertParams) (string, error) {
	c.alert = &p
	return "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test", nil
}

func (c *releaseLineStubClient) UpdateAlert(_ context.Context, _, _ string, p cloudwatch.LogAlertParams) (string, error) {
	c.alert = &p
	return "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:test", nil
}

func releaseLineCases() map[string]*string {
	line := testReleaseLineUID
	return map[string]*string{"with a line": &line, "without a line": nil}
}

func wantReleaseLineHeader(line *string) string {
	if line == nil {
		return ""
	}
	return "true"
}

func TestQueryLogs_ReleaseLineHeader(t *testing.T) {
	for name, line := range releaseLineCases() {
		t.Run(name, func(t *testing.T) {
			client := &releaseLineStubClient{}
			handler := newTestHandler(client, nil)
			var scope gen.LogsQueryRequest_SearchScope
			if err := scope.FromComponentSearchScope(gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: ptr("comp-uid"), ReleaseLineUid: line,
			}); err != nil {
				t.Fatal(err)
			}
			resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &gen.LogsQueryRequest{
				StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), SearchScope: scope,
			}})
			if err != nil {
				t.Fatalf("QueryLogs() error = %v", err)
			}
			ok, is := resp.(gen.QueryLogs200JSONResponse)
			if !is {
				t.Fatalf("expected 200, got %T", resp)
			}
			if line != nil && client.logs.ReleaseLineID != *line {
				t.Errorf("ReleaseLineID = %q, want %q", client.logs.ReleaseLineID, *line)
			}
			if got, want := ok.Headers.OpenChoreoReleaseLineApplied, wantReleaseLineHeader(line); got != want {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, want)
			}
		})
	}
}

func TestQueryLogs_WorkflowScopeNeverSetsReleaseLineHeader(t *testing.T) {
	handler := newTestHandler(&releaseLineStubClient{}, nil)
	var scope gen.LogsQueryRequest_SearchScope
	if err := scope.FromWorkflowSearchScope(gen.WorkflowSearchScope{Namespace: "default", WorkflowRunName: ptr("run-1")}); err != nil {
		t.Fatal(err)
	}
	resp, err := handler.QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &gen.LogsQueryRequest{
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), SearchScope: scope,
	}})
	if err != nil {
		t.Fatalf("QueryLogs() error = %v", err)
	}
	ok, is := resp.(gen.QueryLogs200JSONResponse)
	if !is {
		t.Fatalf("expected 200, got %T", resp)
	}
	if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "" {
		t.Errorf("OpenChoreo-Release-Line-Applied = %q, want empty", got)
	}
}

func TestQueryEvents_ReleaseLineHeader(t *testing.T) {
	for name, line := range releaseLineCases() {
		t.Run(name, func(t *testing.T) {
			client := &releaseLineStubClient{}
			handler := newTestHandler(client, nil)
			scope := makeComponentEventsScopeBody(t, gen.ComponentSearchScope{
				Namespace: "default", ComponentUid: ptr("comp-uid"), ReleaseLineUid: line,
			})
			resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &gen.EventsQueryRequest{
				StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), SearchScope: scope,
			}})
			if err != nil {
				t.Fatalf("QueryEvents() error = %v", err)
			}
			ok, is := resp.(gen.QueryEvents200JSONResponse)
			if !is {
				t.Fatalf("expected 200, got %T", resp)
			}
			if line != nil && client.events.ReleaseLineID != *line {
				t.Errorf("ReleaseLineID = %q, want %q", client.events.ReleaseLineID, *line)
			}
			if got, want := ok.Headers.OpenChoreoReleaseLineApplied, wantReleaseLineHeader(line); got != want {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want %q", got, want)
			}
		})
	}
}

func TestQueryEvents_UnscopedRequests(t *testing.T) {
	handler := newTestHandler(&releaseLineStubClient{}, nil)
	query := func(reasons *[]string) gen.QueryEventsResponseObject {
		resp, err := handler.QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &gen.EventsQueryRequest{
			StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(), Reasons: reasons,
		}})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		return resp
	}
	if resp := query(nil); func() bool { _, ok := resp.(gen.QueryEvents400JSONResponse); return !ok }() {
		t.Errorf("no scope and no reasons: expected 400, got %T", resp)
	}
	if resp := query(&[]string{"BackOff"}); func() bool { _, ok := resp.(gen.QueryEvents501JSONResponse); return !ok }() {
		t.Errorf("unscoped sweep: expected 501, got %T", resp)
	}
}

func TestAlertRule_ReleaseLineHeader(t *testing.T) {
	for name, line := range releaseLineCases() {
		t.Run(name, func(t *testing.T) {
			client := &releaseLineStubClient{}
			handler := newTestHandler(client, nil)
			req := alertRuleRequest()
			if line != nil {
				uid := openapi_types.UUID(uuid.MustParse(*line))
				req.Metadata.ReleaseLineUid = &uid
			}
			want := wantReleaseLineHeader(line)

			createResp, err := handler.CreateAlertRule(context.Background(), gen.CreateAlertRuleRequestObject{Body: req})
			if err != nil {
				t.Fatalf("CreateAlertRule() error = %v", err)
			}
			created, is := createResp.(gen.CreateAlertRule201JSONResponse)
			if !is {
				t.Fatalf("expected 201, got %T", createResp)
			}
			if got := created.Headers.OpenChoreoReleaseLineApplied; got != want {
				t.Errorf("create: OpenChoreo-Release-Line-Applied = %q, want %q", got, want)
			}
			if line != nil && client.alert.ReleaseLineUID != *line {
				t.Errorf("create: ReleaseLineUID = %q, want %q", client.alert.ReleaseLineUID, *line)
			}

			updateResp, err := handler.UpdateAlertRule(context.Background(), gen.UpdateAlertRuleRequestObject{
				RuleName: req.Metadata.Name, Body: req,
			})
			if err != nil {
				t.Fatalf("UpdateAlertRule() error = %v", err)
			}
			updated, is := updateResp.(gen.UpdateAlertRule200JSONResponse)
			if !is {
				t.Fatalf("expected 200, got %T", updateResp)
			}
			if got := updated.Headers.OpenChoreoReleaseLineApplied; got != want {
				t.Errorf("update: OpenChoreo-Release-Line-Applied = %q, want %q", got, want)
			}
		})
	}
}
