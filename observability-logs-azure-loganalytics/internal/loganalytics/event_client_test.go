// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

var eventColumns = []string{
	"TimeGenerated", "Message", "EventType", "Reason", "ObjectNamespace", "ObjectKind", "ObjectName",
	"NamespaceName", "ComponentName", "ComponentUID", "ProjectName", "ProjectUID",
	"EnvironmentName", "EnvironmentUID",
}

func eventRow(ts, reason string) azlogs.Row {
	return azlogs.Row{
		ts, "Back-off restarting failed container", "Warning", reason, "acme-dev", "Pod", "api-7d9f-x2k",
		"acme", "api", testComponentUID, "shop", testProjectUID, "development", testEnvironmentUID,
	}
}

func eventsResponse(rows []azlogs.Row, total *int64) azlogs.QueryWorkspaceResponse {
	tables := []azlogs.Table{table(eventColumns, rows...)}
	if total != nil {
		tables = append(tables, table([]string{"Total"}, azlogs.Row{float64(*total)}))
	}
	return azlogs.QueryWorkspaceResponse{QueryResults: azlogs.QueryResults{Tables: tables}}
}

func ptrInt64(n int64) *int64 { return &n }

func sweepParams(limit int) EventsParams {
	p := eventParams()
	p.Limit = limit
	p.Reasons = []string{"BackOff"}
	return p
}

// badRequestError builds the error azlogs returns for a query the service
// rejects, carrying the service's JSON body.
func badRequestError(body string) error {
	return runtime.NewResponseError(&http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Body:       io.NopCloser(strings.NewReader(body)),
		Request: &http.Request{
			Method: http.MethodPost,
			URL:    &url.URL{Scheme: "https", Host: "api.loganalytics.azure.com", Path: "/v1/workspaces/ws/query"},
		},
	})
}

const missingOTelLogsBody = `{"error":{"message":"The request had some invalid properties","code":"BadArgumentError",` +
	`"innererror":{"code":"SemanticError","message":"A semantic error occurred.",` +
	`"innererror":{"code":"SEM0100","message":"'where' operator: Failed to resolve table or column expression named 'OTelLogs'"}}}}`

func TestGetEvents_MapsRows(t *testing.T) {
	api := &stubAPI{resp: eventsResponse([]azlogs.Row{eventRow("2026-09-01T10:00:00.1234567Z", "BackOff")}, ptrInt64(1))}

	got, err := testClient(api).GetEvents(context.Background(), sweepParams(100))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if got.Total != 1 || len(got.Events) != 1 {
		t.Fatalf("got total=%d events=%d", got.Total, len(got.Events))
	}
	e := got.Events[0]
	want := EventEntry{
		Timestamp:       time.Date(2026, 9, 1, 10, 0, 0, 123456700, time.UTC),
		Message:         "Back-off restarting failed container",
		Type:            "Warning",
		Reason:          "BackOff",
		ObjectNamespace: "acme-dev",
		ObjectKind:      "Pod",
		ObjectName:      "api-7d9f-x2k",
		NamespaceName:   "acme",
		ComponentName:   "api",
		ComponentUID:    testComponentUID,
		ProjectName:     "shop",
		ProjectUID:      testProjectUID,
		EnvironmentName: "development",
		EnvironmentUID:  testEnvironmentUID,
	}
	if e != want {
		t.Errorf("got %+v\nwant %+v", e, want)
	}
	if !strings.Contains(api.lastKQL, "let Base = OTelLogs") {
		t.Errorf("query did not target the default table:\n%s", api.lastKQL)
	}
}

// The Timespan only bounds the scan; it must cover the whole window even
// though it is formatted to whole seconds.
func TestGetEvents_TimespanCoversTheWindow(t *testing.T) {
	api := &stubAPI{resp: eventsResponse(nil, ptrInt64(0))}
	p := sweepParams(100)
	p.StartTime = time.Date(2026, 9, 1, 10, 0, 0, 500_000_000, time.UTC)
	p.EndTime = time.Date(2026, 9, 1, 11, 0, 0, 500_000_000, time.UTC)

	if _, err := testClient(api).GetEvents(context.Background(), p); err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if api.lastBody.Timespan == nil {
		t.Fatal("no Timespan sent")
	}
	start, end, err := api.lastBody.Timespan.Values()
	if err != nil {
		t.Fatalf("parsing Timespan: %v", err)
	}
	if start.After(p.StartTime) || end.Before(p.EndTime) {
		t.Errorf("Timespan %s does not cover [%s, %s)", *api.lastBody.Timespan, p.StartTime, p.EndTime)
	}
}

func TestGetEvents_SkipsRowsWithoutTimestamp(t *testing.T) {
	api := &stubAPI{resp: eventsResponse([]azlogs.Row{
		eventRow("", "BackOff"),
		eventRow("2026-09-01T10:00:00Z", "Killing"),
	}, ptrInt64(2))}

	got, err := testClient(api).GetEvents(context.Background(), sweepParams(100))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(got.Events) != 1 || got.Events[0].Reason != "Killing" {
		t.Errorf("got %+v", got.Events)
	}
}

// Tables are found by column, not position, so the result does not rest on
// the service preserving statement order.
func TestGetEvents_FindsTotalRegardlessOfTableOrder(t *testing.T) {
	resp := eventsResponse([]azlogs.Row{eventRow("2026-09-01T10:00:00Z", "BackOff")}, ptrInt64(7))
	tables := resp.Tables
	resp.Tables = []azlogs.Table{tables[1], tables[0]}

	got, err := testClient(&stubAPI{resp: resp}).GetEvents(context.Background(), sweepParams(100))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if got.Total != 7 || len(got.Events) != 1 {
		t.Errorf("got total=%d events=%d", got.Total, len(got.Events))
	}
}

// total is how a caller learns a read stopped short, so it is never capped.
func TestGetEvents_TotalIsNotCapped(t *testing.T) {
	api := &stubAPI{resp: eventsResponse([]azlogs.Row{eventRow("2026-09-01T10:00:00Z", "BackOff")}, ptrInt64(11001))}

	got, err := testClient(api).GetEvents(context.Background(), sweepParams(1))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if got.Total != 11001 {
		t.Errorf("total = %d, want 11001", got.Total)
	}
}

// A page extended to its timestamp boundary exceeds limit; trimming it in Go
// would split the group the query just completed.
func TestGetEvents_KeepsTheBoundaryGroupPastLimit(t *testing.T) {
	api := &stubAPI{resp: eventsResponse([]azlogs.Row{
		eventRow("2026-09-01T10:00:00Z", "A"),
		eventRow("2026-09-01T10:00:01Z", "B"),
		eventRow("2026-09-01T10:00:01Z", "C"),
		eventRow("2026-09-01T10:00:01Z", "D"),
	}, ptrInt64(4))}

	got, err := testClient(api).GetEvents(context.Background(), sweepParams(2))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(got.Events) != 4 || got.Total != 4 {
		t.Errorf("got total=%d events=%d, want 4/4", got.Total, len(got.Events))
	}
}

func TestGetEvents_TotalNeverBelowThePage(t *testing.T) {
	api := &stubAPI{resp: eventsResponse([]azlogs.Row{
		eventRow("2026-09-01T10:00:00Z", "A"),
		eventRow("2026-09-01T10:00:01Z", "B"),
	}, ptrInt64(1))}

	got, err := testClient(api).GetEvents(context.Background(), sweepParams(100))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if got.Total != 2 {
		t.Errorf("total = %d, want 2", got.Total)
	}
}

func TestGetEvents_MissingTotalOnShortPageReportsThePage(t *testing.T) {
	api := &stubAPI{resp: eventsResponse([]azlogs.Row{eventRow("2026-09-01T10:00:00Z", "A")}, nil)}

	got, err := testClient(api).GetEvents(context.Background(), sweepParams(100))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if got.Total != 1 {
		t.Errorf("total = %d, want 1", got.Total)
	}
}

// On a full page the page size cannot stand in for Total: reporting it would
// tell a caller the window was read when it may not have been.
func TestGetEvents_MissingTotalOnFullPageFails(t *testing.T) {
	api := &stubAPI{resp: eventsResponse([]azlogs.Row{
		eventRow("2026-09-01T10:00:00Z", "A"),
		eventRow("2026-09-01T10:00:01Z", "B"),
	}, nil)}

	if _, err := testClient(api).GetEvents(context.Background(), sweepParams(2)); err == nil {
		t.Fatal("expected an error when Total is missing for a full page")
	}
}

func TestGetEvents_NoRowsTable(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{}}

	got, err := testClient(api).GetEvents(context.Background(), sweepParams(100))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if got.Events == nil || len(got.Events) != 0 || got.Total != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestGetEvents_SurfacesServiceError(t *testing.T) {
	var info azlogs.ErrorInfo
	if err := info.UnmarshalJSON([]byte(`{"code":"PartialError","message":"one statement failed"}`)); err != nil {
		t.Fatalf("unmarshalling ErrorInfo: %v", err)
	}
	resp := eventsResponse([]azlogs.Row{eventRow("2026-09-01T10:00:00Z", "A")}, ptrInt64(1))
	resp.Error = &info

	_, err := testClient(&stubAPI{resp: resp}).GetEvents(context.Background(), sweepParams(100))
	if err == nil || !strings.Contains(err.Error(), "PartialError") {
		t.Fatalf("expected the service-attached error to surface, got %v", err)
	}
}

// The events table appears only once events are ingested; until then there
// are, truthfully, no events.
func TestGetEvents_MissingTableReturnsNoEvents(t *testing.T) {
	api := &stubAPI{err: badRequestError(missingOTelLogsBody)}

	got, err := testClient(api).GetEvents(context.Background(), sweepParams(100))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if got.Events == nil || len(got.Events) != 0 || got.Total != 0 {
		t.Errorf("got %+v", got)
	}
}

// Only the configured table counts as missing; a bad column is a real error.
func TestGetEvents_OtherBadRequestsFail(t *testing.T) {
	body := strings.ReplaceAll(missingOTelLogsBody, "'OTelLogs'", "'ResourceAttributes'")
	api := &stubAPI{err: badRequestError(body)}

	if _, err := testClient(api).GetEvents(context.Background(), sweepParams(100)); err == nil {
		t.Fatal("expected an error for a missing column")
	}
}

// isMissingTable reads the service's error body, not the formatted error
// text, and walks its nested errors to whatever depth they arrive at.
func TestIsMissingTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"live missing-table body", badRequestError(missingOTelLogsBody), true},
		{"shallower nesting", badRequestError(`{"error":{"code":"SEM0100",` +
			`"message":"'where' operator: Failed to resolve table or column expression named 'OTelLogs'"}}`), true},
		{"deeper nesting", badRequestError(`{"error":{"code":"BadArgumentError","innererror":{"code":"SemanticError",` +
			`"innererror":{"code":"Wrapper","innererror":{"code":"SEM0100",` +
			`"message":"Failed to resolve table or column expression named 'OTelLogs'"}}}}}`), true},
		{"another table", badRequestError(strings.ReplaceAll(missingOTelLogsBody, "OTelLogs", "Other_CL")), false},
		{"table named under another code", badRequestError(strings.ReplaceAll(missingOTelLogsBody, "SEM0100", "SEM0001")), false},
		// The formatted error still carries this text; only the decoded body counts.
		{"not a JSON body", badRequestError("Failed to resolve table or column expression named 'OTelLogs'"), false},
		{"no response", &azcore.ResponseError{StatusCode: http.StatusBadRequest}, false},
		{"not a 400", &azcore.ResponseError{StatusCode: http.StatusInternalServerError}, false},
		{"not a response error", errors.New("Failed to resolve table or column expression named 'OTelLogs'"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMissingTable(tc.err, "OTelLogs"); got != tc.want {
				t.Errorf("isMissingTable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetEvents_TransportErrorFails(t *testing.T) {
	api := &stubAPI{err: errors.New("connection reset")}

	if _, err := testClient(api).GetEvents(context.Background(), sweepParams(100)); err == nil {
		t.Fatal("expected the transport error to surface")
	}
}

func TestGetEvents_BuilderErrorFailsWithoutQuerying(t *testing.T) {
	api := &stubAPI{}

	if _, err := testClient(api).GetEvents(context.Background(), eventParams()); err == nil {
		t.Fatal("expected an unscoped query without reasons to be refused")
	}
	if api.lastKQL != "" {
		t.Error("a refused query must not reach the service")
	}
}

func TestGetEvents_UsesConfiguredTable(t *testing.T) {
	api := &stubAPI{err: badRequestError(strings.ReplaceAll(missingOTelLogsBody, "OTelLogs", "Events_CL"))}
	c := NewClientWithQueryAPI(api, Config{WorkspaceID: "ws", EventsTable: "Events_CL", EventsScopeName: "s"}, nil)

	got, err := c.GetEvents(context.Background(), sweepParams(100))
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(got.Events) != 0 || !strings.Contains(api.lastKQL, "let Base = Events_CL") {
		t.Errorf("configured table not used:\n%s", api.lastKQL)
	}
}

func TestProbeEventsTable(t *testing.T) {
	ok := &stubAPI{}
	if err := testClient(ok).ProbeEventsTable(context.Background()); err != nil {
		t.Errorf("probe of an existing table failed: %v", err)
	}
	if ok.lastKQL != "OTelLogs | take 0" {
		t.Errorf("unexpected probe query %q", ok.lastKQL)
	}

	missing := &stubAPI{err: badRequestError(missingOTelLogsBody)}
	if err := testClient(missing).ProbeEventsTable(context.Background()); err == nil {
		t.Error("probe of a missing table should fail")
	}
}
