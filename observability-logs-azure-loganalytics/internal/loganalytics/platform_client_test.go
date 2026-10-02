// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package loganalytics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

// stubAPI records the query it was asked to run and replays a canned response.
type stubAPI struct {
	resp     azlogs.QueryWorkspaceResponse
	err      error
	lastKQL  string
	lastBody azlogs.QueryBody
}

func (s *stubAPI) QueryWorkspace(_ context.Context, _ string, body azlogs.QueryBody,
	_ *azlogs.QueryWorkspaceOptions) (azlogs.QueryWorkspaceResponse, error) {
	s.lastBody = body
	if body.Query != nil {
		s.lastKQL = *body.Query
	}
	return s.resp, s.err
}

func testClient(api QueryAPI) *Client {
	return NewClientWithQueryAPI(api, Config{WorkspaceID: "ws"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func table(cols []string, rows ...azlogs.Row) azlogs.Table {
	c := make([]azlogs.Column, 0, len(cols))
	for i := range cols {
		name := cols[i]
		c = append(c, azlogs.Column{Name: &name})
	}
	return azlogs.Table{Columns: c, Rows: rows}
}

func recordsTable(rows ...azlogs.Row) azlogs.Table {
	return table([]string{
		"TimeGenerated", "LogMessage", "Level", "ClusterInstance", "PodNamespace",
		"PodName", "ContainerName", "NodeName", "ContainerImage", "Labels",
	}, rows...)
}

// goodRow mirrors the projection. ContainerImage arrives already assembled -
// the strcat over imageRepo/image/imageTag happens in KQL, not in Go - so this
// row carries the full reference the query produces, not AMA's raw `image`.
func goodRow() azlogs.Row {
	return azlogs.Row{
		"2026-09-01T10:00:00.123Z", "reconcile failed", "ERROR", "aks-prod-01",
		"openchoreo-control-plane", "controller-manager-0", "manager", "aks-node-1",
		"ghcr.io/openchoreo/controller:1.3.0",
		`{"openchoreo.dev/plane":"controlplane","app.kubernetes.io/name":"controller"}`,
	}
}

func params() PlatformLogsParams {
	return PlatformLogsParams{
		StartTime: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Limit:     100,
	}
}

func TestGetPlatformLogs_MapsEveryField(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
			recordsTable(goodRow()),
			table([]string{"Total"}, azlogs.Row{float64(42)}),
		}},
	}}

	got, err := testClient(api).GetPlatformLogs(context.Background(), params())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(got.Logs))
	}
	e := got.Logs[0]
	if e.LogMessage != "reconcile failed" || e.Level != "ERROR" {
		t.Errorf("message/level not mapped: %+v", e)
	}
	if e.ClusterInstance != "aks-prod-01" || e.NodeName != "aks-node-1" {
		t.Errorf("cluster/node not mapped: %+v", e)
	}
	if e.ContainerImage != "ghcr.io/openchoreo/controller:1.3.0" {
		t.Errorf("image not mapped: %+v", e)
	}
	if e.Labels["openchoreo.dev/plane"] != "controlplane" {
		t.Errorf("labels not mapped verbatim: %+v", e.Labels)
	}
	if e.Timestamp.IsZero() {
		t.Error("timestamp not parsed")
	}
	if got.TotalCount != 42 {
		t.Errorf("total = %d, want 42 from the count statement", got.TotalCount)
	}
}

// The total is read by column name, so it survives the service returning the
// two statements' tables in either order.
func TestGetPlatformLogs_TotalIsFoundRegardlessOfTableOrder(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
			table([]string{"Total"}, azlogs.Row{float64(7)}),
			recordsTable(goodRow()),
		}},
	}}

	got, err := testClient(api).GetPlatformLogs(context.Background(), params())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TotalCount != 7 || len(got.Logs) != 1 {
		t.Errorf("got total=%d logs=%d, want 7 and 1", got.TotalCount, len(got.Logs))
	}
}

// The contract caps total at 1000, and the query deliberately counts one past
// the cap to tell "exactly 1000" from "more".
func TestGetPlatformLogs_TotalIsCapped(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
			recordsTable(goodRow()),
			table([]string{"Total"}, azlogs.Row{float64(1001)}),
		}},
	}}

	got, err := testClient(api).GetPlatformLogs(context.Background(), params())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TotalCount != MaxPlatformTotal {
		t.Errorf("total = %d, want it capped at %d", got.TotalCount, MaxPlatformTotal)
	}
}

// timestamp and log are required on PlatformLog, so a row missing either is
// dropped rather than served as a zero value.
func TestGetPlatformLogs_SkipsMalformedRows(t *testing.T) {
	noTime := goodRow()
	noTime[0] = ""
	noMsg := goodRow()
	noMsg[1] = ""

	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
			recordsTable(goodRow(), noTime, noMsg),
			table([]string{"Total"}, azlogs.Row{float64(3)}),
		}},
	}}

	got, err := testClient(api).GetPlatformLogs(context.Background(), params())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Logs) != 1 {
		t.Errorf("got %d logs, want only the well-formed one", len(got.Logs))
	}
}

// If the count statement produced nothing the page is still correct, so it is
// served rather than failed.
func TestGetPlatformLogs_FallsBackWhenTotalMissing(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{recordsTable(goodRow())}},
	}}

	got, err := testClient(api).GetPlatformLogs(context.Background(), params())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TotalCount != 1 {
		t.Errorf("total = %d, want the page size as a fallback", got.TotalCount)
	}
}

func TestGetPlatformLogs_PropagatesBackendError(t *testing.T) {
	api := &stubAPI{err: errors.New("workspace unreachable")}
	if _, err := testClient(api).GetPlatformLogs(context.Background(), params()); err == nil {
		t.Fatal("expected the backend error to surface")
	}
}

func TestGetPlatformLogFilterValues(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{
			table([]string{"Value", "Count"},
				azlogs.Row{"openchoreo-control-plane", float64(120)},
				azlogs.Row{"cert-manager", float64(30)},
			),
			table([]string{"TotalValues"}, azlogs.Row{float64(2)}),
		}},
	}}

	got, err := testClient(api).GetPlatformLogFilterValues(context.Background(),
		PlatformLogFilterValuesParams{Query: params(), Filter: FilterNamespace, MaxValues: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Filter != FilterNamespace {
		t.Errorf("filter = %q, want it echoed back", got.Filter)
	}
	if len(got.Values) != 2 || got.Values[0].Value != "openchoreo-control-plane" || got.Values[0].Count != 120 {
		t.Errorf("values not mapped: %+v", got.Values)
	}
	if got.TotalValues != 2 {
		t.Errorf("totalValues = %d, want 2", got.TotalValues)
	}
}

func TestParsePodLabels(t *testing.T) {
	if got := parsePodLabels(""); got != nil {
		t.Errorf("empty blob should map to nil, got %v", got)
	}
	if got := parsePodLabels("not json"); got != nil {
		t.Errorf("malformed blob should map to nil, got %v", got)
	}
	got := parsePodLabels(`{"a":"1","n":2,"z":null}`)
	if got["a"] != "1" || got["n"] != "2" || got["z"] != "" {
		t.Errorf("unexpected mapping: %v", got)
	}
}

// A multi-statement query can be answered partially, with the service
// attaching an error to an otherwise-200 response. Reporting the page as
// complete in that case would be worse than failing.
func TestGetPlatformLogs_SurfacesServiceError(t *testing.T) {
	var info azlogs.ErrorInfo
	if err := info.UnmarshalJSON([]byte(`{"code":"PartialError","message":"one statement failed"}`)); err != nil {
		t.Fatalf("unmarshalling ErrorInfo: %v", err)
	}

	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{
			Tables: []azlogs.Table{recordsTable(goodRow())},
			Error:  &info,
		},
	}}

	_, err := testClient(api).GetPlatformLogs(context.Background(), params())
	if err == nil {
		t.Fatal("expected the service-attached error to surface")
	}
	if !strings.Contains(err.Error(), "PartialError") {
		t.Errorf("error should name the service code, got %v", err)
	}
}

// The exported constructor makes a nil logger easy to pass, and the degraded
// paths log.
func TestNewClientWithQueryAPI_ToleratesNilLogger(t *testing.T) {
	api := &stubAPI{resp: azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{Tables: []azlogs.Table{recordsTable(goodRow())}},
	}}
	client := NewClientWithQueryAPI(api, Config{WorkspaceID: "ws"}, nil)

	// No Total table, so this takes the path that warns.
	if _, err := client.GetPlatformLogs(context.Background(), params()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
