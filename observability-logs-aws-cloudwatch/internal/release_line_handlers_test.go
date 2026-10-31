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

func (c *releaseLineStubClient) GetWorkflowLogs(context.Context, cloudwatch.WorkflowLogsParams) (*cloudwatch.WorkflowLogsResult, error) {
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

var releaseLineScopes = map[string]struct {
	line     *string
	allLines *bool
}{
	"one line":   {line: ptr(testReleaseLineUID)},
	"every line": {allLines: ptr(true)},
	"main line":  {},
}

func TestQueryLogs_ReleaseLineScope(t *testing.T) {
	for name, tt := range releaseLineScopes {
		t.Run(name, func(t *testing.T) {
			client := &releaseLineStubClient{}
			resp, err := newTestHandler(client, nil).QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &gen.LogsQueryRequest{
				StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
				SearchScope: makeComponentSearchScopeBody(t, gen.ComponentSearchScope{
					Namespace: "default", ComponentUid: ptr("comp-uid"), ReleaseLineUid: tt.line, AllReleaseLines: tt.allLines,
				}),
			}})
			if err != nil {
				t.Fatalf("QueryLogs() error = %v", err)
			}
			ok, is := resp.(gen.QueryLogs200JSONResponse)
			if !is {
				t.Fatalf("expected 200, got %T", resp)
			}
			if got, want := client.logs.ReleaseLineID, deref(tt.line); got != want {
				t.Errorf("ReleaseLineID = %q, want %q", got, want)
			}
			if got, want := client.logs.AllReleaseLines, deref(tt.allLines); got != want {
				t.Errorf("AllReleaseLines = %v, want %v", got, want)
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", got)
			}
		})
	}
}

func TestQueryLogs_NoReleaseLineHeaderWithoutComponent(t *testing.T) {
	for name, scope := range map[string]gen.LogsQueryRequest_SearchScope{
		"namespace": makeComponentSearchScopeBody(t, gen.ComponentSearchScope{Namespace: "default", AllReleaseLines: ptr(true)}),
		"workflow":  makeWorkflowSearchScopeBody(t, gen.WorkflowSearchScope{Namespace: "default", WorkflowRunName: ptr("run-1")}),
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := newTestHandler(&releaseLineStubClient{}, nil).QueryLogs(context.Background(), gen.QueryLogsRequestObject{Body: &gen.LogsQueryRequest{
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
		})
	}
}

func TestQueryEvents_ReleaseLineScope(t *testing.T) {
	for name, tt := range releaseLineScopes {
		t.Run(name, func(t *testing.T) {
			client := &releaseLineStubClient{}
			resp, err := newTestHandler(client, nil).QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: &gen.EventsQueryRequest{
				StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
				SearchScope: makeComponentEventsScopeBody(t, gen.ComponentSearchScope{
					Namespace: "default", ComponentUid: ptr("comp-uid"), ReleaseLineUid: tt.line, AllReleaseLines: tt.allLines,
				}),
			}})
			if err != nil {
				t.Fatalf("QueryEvents() error = %v", err)
			}
			ok, is := resp.(gen.QueryEvents200JSONResponse)
			if !is {
				t.Fatalf("expected 200, got %T", resp)
			}
			if got, want := client.events.ReleaseLineID, deref(tt.line); got != want {
				t.Errorf("ReleaseLineID = %q, want %q", got, want)
			}
			if got, want := client.events.AllReleaseLines, deref(tt.allLines); got != want {
				t.Errorf("AllReleaseLines = %v, want %v", got, want)
			}
			if got := ok.Headers.OpenChoreoReleaseLineApplied; got != "true" {
				t.Errorf("OpenChoreo-Release-Line-Applied = %q, want true", got)
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
	if resp, ok := query(nil).(gen.QueryEvents400JSONResponse); !ok {
		t.Errorf("no scope and no reasons: expected 400, got %T", resp)
	}
	if resp, ok := query(&[]string{"BackOff"}).(gen.QueryEvents501JSONResponse); !ok {
		t.Errorf("unscoped sweep: expected 501, got %T", resp)
	}
}

// A rule covers its release line, or its component's main line.
func TestAlertRule_ReleaseLineHeader(t *testing.T) {
	for name, line := range map[string]*string{"release line": ptr(testReleaseLineUID), "main line": nil} {
		t.Run(name, func(t *testing.T) {
			client := &releaseLineStubClient{}
			handler := newTestHandler(client, nil)
			req := alertRuleRequest()
			want := "true"
			if line != nil {
				uid := openapi_types.UUID(uuid.MustParse(*line))
				req.Metadata.ReleaseLineUid = &uid
			}

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
			if got := client.alert.ReleaseLineUID; got != deref(line) {
				t.Errorf("create: ReleaseLineUID = %q, want %q", got, deref(line))
			}
			if got := client.alert.ComponentUID; got != req.Metadata.ComponentUid.String() {
				t.Errorf("create: ComponentUID = %q, want %q", got, req.Metadata.ComponentUid.String())
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

func TestUnsupportedSignalsReturnNotImplemented(t *testing.T) {
	handler := newTestHandler(&releaseLineStubClient{}, nil)
	ctx := context.Background()
	if resp, _ := handler.QueryAuditLogs(ctx, gen.QueryAuditLogsRequestObject{}); !isNotImplemented(resp) {
		t.Errorf("QueryAuditLogs: expected 501, got %T", resp)
	}
	if resp, _ := handler.QueryAuditLogFilterValues(ctx, gen.QueryAuditLogFilterValuesRequestObject{}); !isNotImplemented(resp) {
		t.Errorf("QueryAuditLogFilterValues: expected 501, got %T", resp)
	}
	if resp, _ := handler.QueryPlatformLogs(ctx, gen.QueryPlatformLogsRequestObject{}); !isNotImplemented(resp) {
		t.Errorf("QueryPlatformLogs: expected 501, got %T", resp)
	}
	if resp, _ := handler.QueryPlatformLogFilterValues(ctx, gen.QueryPlatformLogFilterValuesRequestObject{}); !isNotImplemented(resp) {
		t.Errorf("QueryPlatformLogFilterValues: expected 501, got %T", resp)
	}
}

func isNotImplemented(resp any) bool {
	switch resp.(type) {
	case gen.QueryAuditLogs501JSONResponse, gen.QueryAuditLogFilterValues501JSONResponse,
		gen.QueryPlatformLogs501JSONResponse, gen.QueryPlatformLogFilterValues501JSONResponse:
		return true
	}
	return false
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
