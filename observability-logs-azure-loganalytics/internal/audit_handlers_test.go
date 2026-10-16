// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
)

func auditHandler(client *loganalytics.Client) *LogsHandler {
	return NewLogsHandler(client, nil, nil, slog.New(slog.DiscardHandler))
}

// auditOffHandler has no audit workspace, so every audit call declines.
func auditOffHandler(t *testing.T) *LogsHandler {
	t.Helper()
	c := loganalytics.NewClientWithQueryAPI(nil, loganalytics.Config{WorkspaceID: "logs-ws"}, nil)
	return auditHandler(c)
}

func auditQueryBody() gen.AuditLogsQueryRequest {
	return gen.AuditLogsQueryRequest{
		StartTime: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	}
}

func TestQueryAuditLogs_RequiresABody(t *testing.T) {
	resp, err := auditOffHandler(t).QueryAuditLogs(context.Background(), gen.QueryAuditLogsRequestObject{})
	if err != nil {
		t.Fatalf("QueryAuditLogs: %v", err)
	}
	if _, ok := resp.(gen.QueryAuditLogs400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestQueryAuditLogs_RejectsAnInvertedWindow(t *testing.T) {
	body := auditQueryBody()
	body.StartTime, body.EndTime = body.EndTime, body.StartTime

	resp, err := auditOffHandler(t).QueryAuditLogs(context.Background(),
		gen.QueryAuditLogsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("QueryAuditLogs: %v", err)
	}
	if _, ok := resp.(gen.QueryAuditLogs400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

// An omitted time arrives as year 1 and passes the ordering check.
func TestQueryAuditLogs_RejectsAMissingTime(t *testing.T) {
	window := auditQueryBody()

	for name, body := range map[string]gen.AuditLogsQueryRequest{
		"no startTime": {EndTime: window.EndTime},
		"no endTime":   {StartTime: window.StartTime},
		"neither":      {},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := auditOffHandler(t).QueryAuditLogs(context.Background(),
				gen.QueryAuditLogsRequestObject{Body: &body})
			if err != nil {
				t.Fatalf("QueryAuditLogs: %v", err)
			}
			if _, ok := resp.(gen.QueryAuditLogs400JSONResponse); !ok {
				t.Errorf("got %T, want 400", resp)
			}
		})
	}
}

func TestQueryAuditLogs_RejectsAnOutOfRangeLimit(t *testing.T) {
	for _, limit := range []int{0, -1, 1001} {
		body := auditQueryBody()
		body.Limit = &limit

		resp, err := auditOffHandler(t).QueryAuditLogs(context.Background(),
			gen.QueryAuditLogsRequestObject{Body: &body})
		if err != nil {
			t.Fatalf("QueryAuditLogs: %v", err)
		}
		if _, ok := resp.(gen.QueryAuditLogs400JSONResponse); !ok {
			t.Errorf("limit %d: got %T, want 400", limit, resp)
		}
	}
}

func TestQueryAuditLogs_DeclinesWhenAuditIsNotConfigured(t *testing.T) {
	body := auditQueryBody()

	resp, err := auditOffHandler(t).QueryAuditLogs(context.Background(),
		gen.QueryAuditLogsRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("QueryAuditLogs: %v", err)
	}

	// 501, never an empty 200, which would read as no activity.
	got, ok := resp.(gen.QueryAuditLogs501JSONResponse)
	if !ok {
		t.Fatalf("got %T, want 501", resp)
	}
	if got.ErrorCode == nil || *got.ErrorCode == "" {
		t.Error("501 should carry an error code")
	}
}

func TestQueryAuditLogFilterValues_DeclinesWhenAuditIsNotConfigured(t *testing.T) {
	body := gen.AuditLogFilterValuesRequest{Filter: gen.ActorId, Query: auditQueryBody()}

	resp, err := auditOffHandler(t).QueryAuditLogFilterValues(context.Background(),
		gen.QueryAuditLogFilterValuesRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("QueryAuditLogFilterValues: %v", err)
	}
	got, ok := resp.(gen.QueryAuditLogFilterValues501JSONResponse)
	if !ok {
		t.Fatalf("got %T, want 501", resp)
	}
	if got.ErrorCode == nil || *got.ErrorCode == "" {
		t.Error("501 should carry an error code")
	}
}

func TestQueryAuditLogFilterValues_RejectsAnUnknownFilter(t *testing.T) {
	body := gen.AuditLogFilterValuesRequest{
		Filter: gen.AuditLogFilterValuesRequestFilter("resource.uid"),
		Query:  auditQueryBody(),
	}

	resp, err := auditOffHandler(t).QueryAuditLogFilterValues(context.Background(),
		gen.QueryAuditLogFilterValuesRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("QueryAuditLogFilterValues: %v", err)
	}

	// Checked before the 501 the unconfigured client would otherwise return.
	if _, ok := resp.(gen.QueryAuditLogFilterValues400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestQueryAuditLogFilterValues_RejectsANonPositiveMaxValues(t *testing.T) {
	zero := 0
	body := gen.AuditLogFilterValuesRequest{
		Filter:    gen.ActorId,
		Query:     auditQueryBody(),
		MaxValues: &zero,
	}

	resp, err := auditOffHandler(t).QueryAuditLogFilterValues(context.Background(),
		gen.QueryAuditLogFilterValuesRequestObject{Body: &body})
	if err != nil {
		t.Fatalf("QueryAuditLogFilterValues: %v", err)
	}
	if _, ok := resp.(gen.QueryAuditLogFilterValues400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestAuditFilterColumns_CoversEveryContractFilter(t *testing.T) {
	// A missing mapping means the picker offers a filter the adapter refuses.
	for _, f := range []gen.AuditLogFilterValuesRequestFilter{
		gen.Action, gen.ActorEntitlements, gen.ActorId, gen.ActorIssuer,
		gen.ActorSessionId, gen.ActorType, gen.Category, gen.OperationId,
		gen.Producer, gen.ResourceComponent, gen.ResourceEnvironment,
		gen.ResourceName, gen.ResourceNamespace, gen.ResourceProject,
		gen.ResourceResource, gen.ResourceType, gen.Result, gen.SourceIp,
		gen.Surface, gen.UserAgent,
	} {
		mapped, ok := auditFilterColumns[f]
		if !ok {
			t.Errorf("filter %q has no mapping", f)
			continue
		}
		if !loganalytics.IsListableAuditFilter(mapped) {
			t.Errorf("filter %q maps to %q, which has no column", f, mapped)
		}
	}
}

func TestAuditParamsFromRequest_CarriesEveryFilterGroup(t *testing.T) {
	body := auditQueryBody()
	limit := 25
	body.Limit = &limit
	body.Action = &[]string{"create_project"}
	body.Category = &[]gen.AuditLogsQueryRequestCategory{gen.Management}
	body.Result = &[]gen.AuditLogsQueryRequestResult{gen.AuditLogsQueryRequestResultDenied}
	body.Surface = &[]gen.AuditLogsQueryRequestSurface{gen.Rest}
	body.Actor = &gen.AuditLogsActorFilter{
		Id:           &[]string{"alice"},
		Entitlements: &[]string{"platform"},
	}
	body.Resource = &gen.AuditLogsResourceFilter{
		Project: &[]string{"shop"},
	}

	got, msg := auditParamsFromRequest(body)
	if msg != "" {
		t.Fatalf("unexpected rejection: %s", msg)
	}

	if got.Limit != 25 || got.SortOrder != loganalytics.SortDesc {
		t.Errorf("limit/sort = %d/%s", got.Limit, got.SortOrder)
	}
	// The enum-typed groups are the easy ones to drop when converting.
	if len(got.Categories) != 1 || got.Categories[0] != "management" {
		t.Errorf("categories = %v", got.Categories)
	}
	if len(got.Results) != 1 || got.Results[0] != "denied" {
		t.Errorf("results = %v", got.Results)
	}
	if len(got.Surfaces) != 1 || got.Surfaces[0] != "rest" {
		t.Errorf("surfaces = %v", got.Surfaces)
	}
	if len(got.ActorIDs) != 1 || len(got.ActorEntitlements) != 1 {
		t.Errorf("actor filters = %+v / %+v", got.ActorIDs, got.ActorEntitlements)
	}
	if len(got.ResourceProjects) != 1 {
		t.Errorf("resource filters = %+v", got.ResourceProjects)
	}
}

func TestAuditParamsFromRequest_DefaultsSortToDescending(t *testing.T) {
	got, msg := auditParamsFromRequest(auditQueryBody())
	if msg != "" {
		t.Fatalf("unexpected rejection: %s", msg)
	}
	// Newest first is what an investigator opens the trail expecting.
	if got.SortOrder != loganalytics.SortDesc {
		t.Errorf("SortOrder = %s, want desc", got.SortOrder)
	}
}
