// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/api/gen"
)

const (
	evComponentUID   = "3f9c2b1e-8a4d-4c6e-9b2f-1d7e5a0c4b8f"
	evProjectUID     = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
	evEnvironmentUID = "0f1e2d3c-4b5a-4968-8776-655443322110"
)

func eventTables(total int, rows ...azlogs.Row) []azlogs.Table {
	return []azlogs.Table{
		{
			Columns: cols("TimeGenerated", "Message", "EventType", "Reason", "ObjectNamespace", "ObjectKind",
				"ObjectName", "NamespaceName", "ComponentName", "ComponentUID", "ProjectName", "ProjectUID",
				"EnvironmentName", "EnvironmentUID"),
			Rows: rows,
		},
		{Columns: cols("Total"), Rows: []azlogs.Row{{float64(total)}}},
	}
}

func evRow(ts, reason string) azlogs.Row {
	return azlogs.Row{
		ts, "Scaled up replica set api-7d9f to 2", "Normal", reason, "acme-dev", "Deployment", "api",
		"acme", "api", evComponentUID, "shop", evProjectUID, "development", evEnvironmentUID,
	}
}

func componentScope(t *testing.T, s gen.ComponentSearchScope) *gen.EventsQueryRequest_SearchScope {
	t.Helper()
	var scope gen.EventsQueryRequest_SearchScope
	if err := scope.FromComponentSearchScope(s); err != nil {
		t.Fatalf("building component scope: %v", err)
	}
	return &scope
}

func workflowScope(t *testing.T, s gen.WorkflowSearchScope) *gen.EventsQueryRequest_SearchScope {
	t.Helper()
	var scope gen.EventsQueryRequest_SearchScope
	if err := scope.FromWorkflowSearchScope(s); err != nil {
		t.Fatalf("building workflow scope: %v", err)
	}
	return &scope
}

func eventsBody() *gen.EventsQueryRequest {
	start, end := window()
	return &gen.EventsQueryRequest{StartTime: start, EndTime: end}
}

func strPtr(s string) *string { return &s }

func intPtr(n int) *int { return &n }

func queryEvents(t *testing.T, api *stubQueryAPI, body *gen.EventsQueryRequest) gen.QueryEventsResponseObject {
	t.Helper()
	resp, err := platformHandler(api).QueryEvents(context.Background(), gen.QueryEventsRequestObject{Body: body})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return resp
}

func expectEvents400(t *testing.T, resp gen.QueryEventsResponseObject, wantMsg string) {
	t.Helper()
	r, ok := resp.(gen.QueryEvents400JSONResponse)
	if !ok {
		t.Fatalf("got %T, want 400", resp)
	}
	if r.Message == nil || !strings.Contains(*r.Message, wantMsg) {
		t.Errorf("message = %v, want it to contain %q", r.Message, wantMsg)
	}
	if r.ErrorCode == nil || *r.ErrorCode != errCodeBadRequest {
		t.Errorf("errorCode = %v, want %s", r.ErrorCode, errCodeBadRequest)
	}
}

func expectEvents200(t *testing.T, resp gen.QueryEventsResponseObject) gen.QueryEvents200JSONResponse {
	t.Helper()
	r, ok := resp.(gen.QueryEvents200JSONResponse)
	if !ok {
		t.Fatalf("got %T, want 200", resp)
	}
	if r.Events == nil {
		t.Fatal("events must always be present")
	}
	return r
}

func TestQueryEvents_NilBody(t *testing.T) {
	expectEvents400(t, queryEvents(t, &stubQueryAPI{}, nil), "request body is required")
}

// A missing scope must never read as "all namespaces" on its own.
func TestQueryEvents_NoScopeNoReasons(t *testing.T) {
	api := &stubQueryAPI{}
	expectEvents400(t, queryEvents(t, api, eventsBody()),
		"searchScope is required unless reasons is set for an unscoped sweep")
	if api.lastKQL != "" {
		t.Error("a rejected request must not reach the workspace")
	}
}

func TestQueryEvents_NoScopeEmptyReasons(t *testing.T) {
	body := eventsBody()
	body.Reasons = &[]string{}
	expectEvents400(t, queryEvents(t, &stubQueryAPI{}, body),
		"searchScope is required unless reasons is set for an unscoped sweep")
}

func TestQueryEvents_RejectsInvalidRequests(t *testing.T) {
	tooMany := make([]string, maxEventReasons+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("Reason%d", i)
	}
	nsOnly := gen.ComponentSearchScope{Namespace: "acme"}

	cases := []struct {
		name   string
		mutate func(*gen.EventsQueryRequest)
		want   string
	}{
		{"end before start", func(b *gen.EventsQueryRequest) {
			b.StartTime, b.EndTime = b.EndTime, b.StartTime
			b.SearchScope = componentScope(t, nsOnly)
		}, "endTime must be greater than or equal to startTime"},
		{"limit zero", func(b *gen.EventsQueryRequest) {
			b.Limit = intPtr(0)
			b.SearchScope = componentScope(t, nsOnly)
		}, "limit must be between 1 and 1000"},
		{"limit above max", func(b *gen.EventsQueryRequest) {
			b.Limit = intPtr(1001)
			b.SearchScope = componentScope(t, nsOnly)
		}, "limit must be between 1 and 1000"},
		{"bad sort order", func(b *gen.EventsQueryRequest) {
			o := gen.EventsQueryRequestSortOrder("sideways")
			b.SortOrder = &o
			b.SearchScope = componentScope(t, nsOnly)
		}, "sortOrder must be one of asc, desc"},
		{"empty reasons with a scope", func(b *gen.EventsQueryRequest) {
			b.Reasons = &[]string{}
			b.SearchScope = componentScope(t, nsOnly)
		}, "reasons must contain at least one value"},
		{"too many reasons", func(b *gen.EventsQueryRequest) {
			b.Reasons = &tooMany
		}, "reasons cannot contain more than 32 values"},
		{"duplicate reason", func(b *gen.EventsQueryRequest) {
			b.Reasons = &[]string{"BackOff", "BackOff"}
		}, `reasons contains "BackOff" more than once`},
		{"blank reason", func(b *gen.EventsQueryRequest) {
			b.Reasons = &[]string{"BackOff", "  "}
		}, "reasons must not contain empty values"},
		{"component scope without namespace", func(b *gen.EventsQueryRequest) {
			b.SearchScope = componentScope(t, gen.ComponentSearchScope{Namespace: " "})
		}, "searchScope.namespace is required"},
		{"workflow scope without namespace", func(b *gen.EventsQueryRequest) {
			b.SearchScope = workflowScope(t, gen.WorkflowSearchScope{WorkflowRunName: strPtr("build-42")})
		}, "searchScope.namespace is required"},
		{"workflow scope with blank run", func(b *gen.EventsQueryRequest) {
			b.SearchScope = workflowScope(t, gen.WorkflowSearchScope{Namespace: "acme", WorkflowRunName: strPtr("")})
		}, "searchScope.workflowRunName must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := eventsBody()
			tc.mutate(body)
			api := &stubQueryAPI{}
			expectEvents400(t, queryEvents(t, api, body), tc.want)
			if api.lastKQL != "" {
				t.Error("a rejected request must not reach the workspace")
			}
		})
	}
}

func TestQueryEvents_ComponentScope(t *testing.T) {
	api := &stubQueryAPI{tables: eventTables(1, evRow("2026-09-01T10:00:00Z", "ScalingReplicaSet"))}
	body := eventsBody()
	body.SearchScope = componentScope(t, gen.ComponentSearchScope{
		Namespace:      "acme",
		ProjectUid:     strPtr(evProjectUID),
		ComponentUid:   strPtr(evComponentUID),
		EnvironmentUid: strPtr(evEnvironmentUID),
	})

	r := expectEvents200(t, queryEvents(t, api, body))
	if r.Total != 1 || len(*r.Events) != 1 {
		t.Fatalf("total=%d events=%d", r.Total, len(*r.Events))
	}
	if r.TookMs == nil {
		t.Error("tookMs must be set")
	}

	e := (*r.Events)[0]
	if e.Timestamp == nil || e.Timestamp.Format("15:04:05") != "10:00:00" {
		t.Errorf("timestamp = %v", e.Timestamp)
	}
	if derefString(e.Message) != "Scaled up replica set api-7d9f to 2" ||
		derefString(e.Type) != "Normal" || derefString(e.Reason) != "ScalingReplicaSet" {
		t.Errorf("unexpected event %+v", e)
	}
	if e.Metadata == nil {
		t.Fatal("metadata missing")
	}
	m := *e.Metadata
	if derefString(m.NamespaceName) != "acme" || derefString(m.ComponentName) != "api" ||
		derefString(m.ProjectName) != "shop" || derefString(m.EnvironmentName) != "development" ||
		derefString(m.ObjectKind) != "Deployment" || derefString(m.ObjectName) != "api" ||
		derefString(m.ObjectNamespace) != "acme-dev" {
		t.Errorf("unexpected metadata %+v", m)
	}
	if m.ComponentUid == nil || m.ComponentUid.String() != evComponentUID ||
		m.ProjectUid == nil || m.ProjectUid.String() != evProjectUID ||
		m.EnvironmentUid == nil || m.EnvironmentUid.String() != evEnvironmentUID {
		t.Errorf("UIDs not mapped: %+v", m)
	}

	for _, want := range []string{
		`openchoreo.dev/namespace"]) == "acme"`,
		`openchoreo.dev/project-uid"]) == "` + evProjectUID + `"`,
		`openchoreo.dev/component-uid"]) == "` + evComponentUID + `"`,
		`openchoreo.dev/environment-uid"]) == "` + evEnvironmentUID + `"`,
		"top 100 by TimeGenerated desc",
	} {
		if !strings.Contains(api.lastKQL, want) {
			t.Errorf("query missing %q:\n%s", want, api.lastKQL)
		}
	}
}

func TestQueryEvents_WorkflowScope(t *testing.T) {
	api := &stubQueryAPI{tables: eventTables(0)}
	body := eventsBody()
	body.SearchScope = workflowScope(t, gen.WorkflowSearchScope{
		Namespace: "acme", WorkflowRunName: strPtr("build-42"), TaskName: strPtr("push-image"),
	})
	asc := gen.EventsQueryRequestSortOrderAsc
	body.SortOrder = &asc

	r := expectEvents200(t, queryEvents(t, api, body))
	if r.Total != 0 || len(*r.Events) != 0 {
		t.Errorf("total=%d events=%d", r.Total, len(*r.Events))
	}
	for _, want := range []string{
		`== "workflows-acme"`,
		`startswith_cs "build-42"`,
		`contains_cs "push-image"`,
		"top 100 by TimeGenerated asc",
	} {
		if !strings.Contains(api.lastKQL, want) {
			t.Errorf("query missing %q:\n%s", want, api.lastKQL)
		}
	}
}

func TestQueryEvents_UnscopedReasonSweep(t *testing.T) {
	api := &stubQueryAPI{tables: eventTables(1, evRow("2026-09-01T10:00:00Z", "DeploymentSucceeded"))}
	body := eventsBody()
	body.Reasons = &[]string{"DeploymentSucceeded", "DeploymentFailed"}

	r := expectEvents200(t, queryEvents(t, api, body))
	if r.Total != 1 || len(*r.Events) != 1 || *(*r.Events)[0].Reason != "DeploymentSucceeded" {
		t.Errorf("unexpected response %+v", r)
	}
	if !strings.Contains(api.lastKQL, `in ("DeploymentSucceeded", "DeploymentFailed")`) {
		t.Errorf("reasons filter missing:\n%s", api.lastKQL)
	}
	// Filters live in the Base statement; the projection reads the label too.
	base, _, _ := strings.Cut(api.lastKQL, ";\n")
	if strings.Contains(base, "openchoreo.dev/namespace") || strings.Contains(base, "k8s.namespace.name") {
		t.Errorf("an unscoped sweep must not be narrowed by a namespace:\n%s", base)
	}
}

func TestQueryEvents_ReasonsNarrowAScopedQuery(t *testing.T) {
	api := &stubQueryAPI{tables: eventTables(0)}
	body := eventsBody()
	body.SearchScope = componentScope(t, gen.ComponentSearchScope{Namespace: "acme"})
	body.Reasons = &[]string{"BackOff"}

	expectEvents200(t, queryEvents(t, api, body))
	if !strings.Contains(api.lastKQL, `in ("BackOff")`) {
		t.Errorf("reasons filter missing:\n%s", api.lastKQL)
	}
}

// Unparseable UIDs and empty strings are left out rather than served as zero
// values.
func TestQueryEvents_OmitsEmptyAndInvalidFields(t *testing.T) {
	row := azlogs.Row{"2026-09-01T10:00:00Z", "", "", "", "", "Pod", "p", "", "", "not-a-uuid", "", "", "", ""}
	api := &stubQueryAPI{tables: eventTables(1, row)}
	body := eventsBody()
	body.Reasons = &[]string{"BackOff"}

	e := (*expectEvents200(t, queryEvents(t, api, body)).Events)[0]
	if e.Message != nil || e.Type != nil || e.Reason != nil {
		t.Errorf("empty strings should be omitted: %+v", e)
	}
	if e.Metadata.ComponentUid != nil || e.Metadata.NamespaceName != nil {
		t.Errorf("invalid or empty metadata should be omitted: %+v", e.Metadata)
	}
	if e.Metadata.ObjectKind == nil || *e.Metadata.ObjectKind != "Pod" {
		t.Errorf("objectKind = %v", e.Metadata.ObjectKind)
	}
}

// total greater than the page tells the caller its read stopped short.
func TestQueryEvents_TotalExceedsPageWhenTruncated(t *testing.T) {
	api := &stubQueryAPI{tables: eventTables(3,
		evRow("2026-09-01T10:00:00Z", "A"),
		evRow("2026-09-01T10:00:01Z", "B"),
	)}
	body := eventsBody()
	body.Reasons = &[]string{"A", "B", "C"}
	body.Limit = intPtr(2)

	r := expectEvents200(t, queryEvents(t, api, body))
	if r.Total != 3 || len(*r.Events) != 2 {
		t.Errorf("total=%d events=%d, want 3/2", r.Total, len(*r.Events))
	}
}

func TestQueryEvents_TotalMatchesPageWhenWindowRead(t *testing.T) {
	api := &stubQueryAPI{tables: eventTables(2,
		evRow("2026-09-01T10:00:00Z", "A"),
		evRow("2026-09-01T10:00:01Z", "B"),
	)}
	body := eventsBody()
	body.Reasons = &[]string{"A", "B"}
	body.Limit = intPtr(2)

	r := expectEvents200(t, queryEvents(t, api, body))
	if r.Total != 2 || len(*r.Events) != 2 {
		t.Errorf("total=%d events=%d, want 2/2", r.Total, len(*r.Events))
	}
}

// The query extends a page to its timestamp boundary; the handler must serve
// the whole group rather than trim it back to limit.
func TestQueryEvents_DoesNotSplitATimestampGroup(t *testing.T) {
	api := &stubQueryAPI{tables: eventTables(4,
		evRow("2026-09-01T10:00:00Z", "A"),
		evRow("2026-09-01T10:00:01Z", "B"),
		evRow("2026-09-01T10:00:01Z", "C"),
		evRow("2026-09-01T10:00:01Z", "D"),
	)}
	body := eventsBody()
	body.Reasons = &[]string{"A", "B", "C", "D"}
	body.Limit = intPtr(2)
	asc := gen.EventsQueryRequestSortOrderAsc
	body.SortOrder = &asc

	r := expectEvents200(t, queryEvents(t, api, body))
	if r.Total != 4 || len(*r.Events) != 4 {
		t.Errorf("total=%d events=%d, want 4/4", r.Total, len(*r.Events))
	}
	if !strings.Contains(api.lastKQL, "| where TimeGenerated <= Boundary") {
		t.Errorf("page is not extended to its boundary:\n%s", api.lastKQL)
	}
}

func TestQueryEvents_BackendError(t *testing.T) {
	body := eventsBody()
	body.Reasons = &[]string{"BackOff"}

	resp := queryEvents(t, &stubQueryAPI{err: errors.New("boom")}, body)
	r, ok := resp.(gen.QueryEvents500JSONResponse)
	if !ok {
		t.Fatalf("got %T, want 500", resp)
	}
	if r.ErrorCode == nil || *r.ErrorCode != errCodeInternal {
		t.Errorf("errorCode = %v", r.ErrorCode)
	}
	if r.Message == nil || strings.Contains(*r.Message, "boom") {
		t.Errorf("backend detail must not leak into the response: %v", r.Message)
	}
}
