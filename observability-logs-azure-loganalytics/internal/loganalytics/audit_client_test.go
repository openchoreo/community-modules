// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

// auditRecordColumns is derived from the projection, not copied: a parallel
// list would keep passing against itself after the projection changed.
var auditRecordColumns = auditProjectionColumns()

// auditRow lines up with auditRecordColumns positionally.
func auditRow(eventID string) azlogs.Row {
	return azlogs.Row{
		"2026-09-01T10:00:00.0000000Z", "1.0", eventID, "create_project", "management",
		"success", "req-1", "10.0.0.1", "occ/1.3", "openchoreo-api",
		"rest", "CreateProject", "user", "alice", "https://idp.example",
		"sess-1", "POST", "/api/v1/projects", "Project",
		"acme", "acme/dev", "shop",
		"api", "", "uid-1", "shop",
		"openchoreo-control-plane", "api-server-7d9f", "api-server",
		`{"groups":["platform","dev"]}`, "", "",
	}
}

func TestAuditRowMatchesTheProjection(t *testing.T) {
	if got, want := len(auditRow("evt")), len(auditRecordColumns); got != want {
		t.Fatalf("auditRow has %d values but the projection has %d columns; "+
			"update auditRow to match auditProjection", got, want)
	}
}

func newAuditClient(t *testing.T, api QueryAPI) *Client {
	t.Helper()
	return NewClientWithQueryAPI(api, Config{
		WorkspaceID:      "logs-ws",
		AuditWorkspaceID: "audit-ws",
		AuditTable:       auditTestTable,
	}, nil)
}

func TestGetAuditLogs_DeclinesWhenNoAuditWorkspaceConfigured(t *testing.T) {
	c := NewClientWithQueryAPI(&stubAPI{}, Config{WorkspaceID: "logs-ws"}, nil)

	if c.AuditEnabled() {
		t.Fatal("audit must be off without an audit workspace")
	}

	_, err := c.GetAuditLogs(context.Background(), auditTestParams())
	if !errors.Is(err, ErrAuditNotConfigured) {
		t.Errorf("err = %v, want ErrAuditNotConfigured", err)
	}
}

func TestGetAuditLogs_MissingTableDeclinesRatherThanReportingAnEmptyWindow(t *testing.T) {
	api := &stubAPI{err: badRequestError(
		`{"error":{"message":"The request had some invalid properties","code":"BadArgumentError",` +
			`"innererror":{"code":"SemanticError","message":"A semantic error occurred.",` +
			`"innererror":{"code":"SEM0100",` +
			`"message":"'where' operator: Failed to resolve table or column expression named '` + auditTestTable + `'"}}}}`)}
	c := newAuditClient(t, api)

	_, err := c.GetAuditLogs(context.Background(), auditTestParams())

	// An empty page would read as "nothing happened" rather than "not here".
	if !errors.Is(err, ErrAuditNotConfigured) {
		t.Errorf("err = %v, want ErrAuditNotConfigured", err)
	}
}

func TestGetAuditLogs_ReturnsRecordsAndWindowTotal(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
		table(auditRecordColumns, auditRow("evt-1"), auditRow("evt-2")),
		table([]string{"Total"}, azlogs.Row{float64(4321)}),
	}}}}
	c := newAuditClient(t, api)

	got, err := c.GetAuditLogs(context.Background(), auditTestParams())
	if err != nil {
		t.Fatalf("GetAuditLogs: %v", err)
	}

	if len(got.Records) != 2 {
		t.Fatalf("got %d records, want 2", len(got.Records))
	}
	// Total is the window, not the page: a caller pages until it has Total.
	if got.Total != 4321 {
		t.Errorf("Total = %d, want 4321", got.Total)
	}

	r := got.Records[0]
	if r.EventID != "evt-1" || r.Actor.ID != "alice" || r.Result != "success" {
		t.Errorf("record mis-mapped: %+v", r)
	}
	if r.HTTP == nil || r.HTTP.Method != "POST" || r.HTTP.Path != "/api/v1/projects" {
		t.Errorf("http info mis-mapped: %+v", r.HTTP)
	}
	if r.Resource == nil || r.Resource.Project != "shop" || r.Resource.UID != "uid-1" {
		t.Errorf("resource mis-mapped: %+v", r.Resource)
	}
	// Collector fields come from the collector, never the producer's own line.
	if r.Collector == nil || r.Collector.ContainerName != "api-server" {
		t.Errorf("collector info mis-mapped: %+v", r.Collector)
	}
	if got := r.Actor.Entitlements["groups"]; len(got) != 2 || got[0] != "platform" {
		t.Errorf("entitlements mis-decoded: %+v", r.Actor.Entitlements)
	}
}

func TestGetAuditLogs_RefusesAFullPageWithNoTotal(t *testing.T) {
	rows := make([]azlogs.Row, 2)
	for i := range rows {
		rows[i] = auditRow("evt")
	}
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
		table(auditRecordColumns, rows...),
	}}}}
	c := newAuditClient(t, api)

	p := auditTestParams()
	p.Limit = 2

	// Reporting len(records) here would read as "window fully read" and the
	// caller would stop before records it never saw.
	if _, err := c.GetAuditLogs(context.Background(), p); err == nil {
		t.Error("expected an error when a full page carries no Total")
	}
}

// The query excludes these rows, so this covers the mapping guard behind it.
// Total is deliberately not asserted: keeping the two consistent is the
// query's job, checked in TestBuildAuditLogsKQL_ExcludesUnusableRowsFromEveryStatement.
func TestGetAuditLogs_SkipsRecordsMissingAnIdentityField(t *testing.T) {
	bad := auditRow("evt-2")
	bad[13] = "" // ActorId

	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
		table(auditRecordColumns, auditRow("evt-1"), bad),
		table([]string{"Total"}, azlogs.Row{float64(2)}),
	}}}}
	c := newAuditClient(t, api)

	got, err := c.GetAuditLogs(context.Background(), auditTestParams())
	if err != nil {
		t.Fatalf("GetAuditLogs: %v", err)
	}

	// Zero-valued, it would read as a real finding about an anonymous action.
	if len(got.Records) != 1 || got.Records[0].EventID != "evt-1" {
		t.Errorf("expected the record with no actor id skipped, got %+v", got.Records)
	}
}

func TestGetAuditLogs_KeepsARecordWithNoResolvedOperation(t *testing.T) {
	row := auditRow("evt-1")
	row[3] = "" // Action
	row[4] = "" // Category

	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
		table(auditRecordColumns, row),
		table([]string{"Total"}, azlogs.Row{float64(1)}),
	}}}}
	c := newAuditClient(t, api)

	got, err := c.GetAuditLogs(context.Background(), auditTestParams())
	if err != nil {
		t.Fatalf("GetAuditLogs: %v", err)
	}

	// A request rejected before an operation resolved carries these empty and
	// is exactly the kind of record an investigator wants to see.
	if len(got.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(got.Records))
	}
}

func TestGetAuditLogs_FillsEmptyTimelineBuckets(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
		table(auditRecordColumns),
		table([]string{"Total"}, azlogs.Row{float64(3)}),
		table([]string{"BucketStart", ColResult, "BucketTotal"},
			azlogs.Row{"2026-09-01T00:00:00.0000000Z", "success", float64(2)},
			azlogs.Row{"2026-09-01T02:00:00.0000000Z", "denied", float64(1)},
		),
	}}}}
	c := newAuditClient(t, api)

	p := auditTestParams()
	p.StartTime = start
	p.EndTime = start.Add(4 * time.Hour)
	p.IncludeTimeline = true
	p.TimelineInterval = "1h"

	got, err := c.GetAuditLogs(context.Background(), p)
	if err != nil {
		t.Fatalf("GetAuditLogs: %v", err)
	}
	if got.Timeline == nil {
		t.Fatal("expected a timeline")
	}

	// KQL returns only the bins that had records; a gap would read as missing
	// data rather than as a quiet hour.
	if len(got.Timeline.Buckets) != 4 {
		t.Fatalf("got %d buckets, want 4 covering the window", len(got.Timeline.Buckets))
	}
	if got.Timeline.Interval != "1h" {
		t.Errorf("Interval = %q, want 1h", got.Timeline.Interval)
	}
	if got.Timeline.Buckets[0].Total != 2 || got.Timeline.Buckets[0].Counts["success"] != 2 {
		t.Errorf("first bucket = %+v", got.Timeline.Buckets[0])
	}
	if got.Timeline.Buckets[1].Total != 0 || len(got.Timeline.Buckets[1].Counts) != 0 {
		t.Errorf("expected an empty second bucket, got %+v", got.Timeline.Buckets[1])
	}
	if got.Timeline.Buckets[2].Counts["denied"] != 1 {
		t.Errorf("third bucket = %+v", got.Timeline.Buckets[2])
	}
}

func TestGetAuditLogFilterValues_ReturnsValuesAndDistinctTotal(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
		table([]string{"Value", "Count"},
			azlogs.Row{"alice", float64(9)},
			azlogs.Row{"bob", float64(3)},
		),
		table([]string{"TotalValues"}, azlogs.Row{float64(17)}),
	}}}}
	c := newAuditClient(t, api)

	got, err := c.GetAuditLogFilterValues(context.Background(), AuditFilterValuesParams{
		Query:  auditTestParams(),
		Filter: AuditFilterActorID,
	})
	if err != nil {
		t.Fatalf("GetAuditLogFilterValues: %v", err)
	}

	if len(got.Values) != 2 || got.Values[0].Value != "alice" || got.Values[0].Count != 9 {
		t.Errorf("values mis-mapped: %+v", got.Values)
	}
	// More distinct values exist than were returned, which is what tells a
	// caller the list is truncated.
	if got.TotalValues != 17 {
		t.Errorf("TotalValues = %d, want 17", got.TotalValues)
	}
}

func TestGetAuditLogFilterValues_DeclinesWhenNotConfigured(t *testing.T) {
	c := NewClientWithQueryAPI(&stubAPI{}, Config{WorkspaceID: "logs-ws"}, nil)

	_, err := c.GetAuditLogFilterValues(context.Background(), AuditFilterValuesParams{
		Query:  auditTestParams(),
		Filter: AuditFilterActorID,
	})
	if !errors.Is(err, ErrAuditNotConfigured) {
		t.Errorf("err = %v, want ErrAuditNotConfigured", err)
	}
}

func TestGetAuditLogs_QueriesTheAuditWorkspace(t *testing.T) {
	api := &recordingAPI{resp: azlogs.QueryWorkspaceResponse{QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
		table(auditRecordColumns),
		table([]string{"Total"}, azlogs.Row{float64(0)}),
	}}}}
	c := newAuditClient(t, api)

	if _, err := c.GetAuditLogs(context.Background(), auditTestParams()); err != nil {
		t.Fatalf("GetAuditLogs: %v", err)
	}

	// The audit table may live in a workspace of its own, so audit queries must
	// not be sent to the container-logs workspace.
	if api.lastWorkspace != "audit-ws" {
		t.Errorf("queried workspace %q, want audit-ws", api.lastWorkspace)
	}
}

// recordingAPI captures the workspace a query was sent to, which stubAPI drops.
type recordingAPI struct {
	resp          azlogs.QueryWorkspaceResponse
	err           error
	lastWorkspace string
}

func (r *recordingAPI) QueryWorkspace(_ context.Context, workspaceID string, _ azlogs.QueryBody,
	_ *azlogs.QueryWorkspaceOptions) (azlogs.QueryWorkspaceResponse, error) {
	r.lastWorkspace = workspaceID
	return r.resp, r.err
}
