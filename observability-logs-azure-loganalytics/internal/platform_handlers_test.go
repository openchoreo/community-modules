// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
)

type stubQueryAPI struct {
	tables  []azlogs.Table
	err     error
	lastKQL string
}

func (s *stubQueryAPI) QueryWorkspace(_ context.Context, _ string, body azlogs.QueryBody,
	_ *azlogs.QueryWorkspaceOptions) (azlogs.QueryWorkspaceResponse, error) {
	if body.Query != nil {
		s.lastKQL = *body.Query
	}
	if s.err != nil {
		return azlogs.QueryWorkspaceResponse{}, s.err
	}
	return azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{Tables: s.tables},
	}, nil
}

func platformHandler(api loganalytics.QueryAPI) *LogsHandler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := loganalytics.NewClientWithQueryAPI(api, loganalytics.Config{WorkspaceID: "ws"}, logger)
	return NewLogsHandler(client, nil, nil, logger)
}

func cols(names ...string) []azlogs.Column {
	out := make([]azlogs.Column, 0, len(names))
	for i := range names {
		n := names[i]
		out = append(out, azlogs.Column{Name: &n})
	}
	return out
}

func recordTables() []azlogs.Table {
	return []azlogs.Table{
		{
			Columns: cols("TimeGenerated", "LogMessage", "Level", "ClusterInstance",
				"PodNamespace", "PodName", "ContainerName", "NodeName", "ContainerImage", "Labels"),
			Rows: []azlogs.Row{{
				"2026-09-01T10:00:00Z", "reconcile failed", "ERROR", "aks-prod-01",
				"openchoreo-control-plane", "controller-manager-0", "manager", "aks-node-1",
				"ghcr.io/openchoreo/controller:1.3.0",
				`{"openchoreo.dev/plane":"controlplane"}`,
			}},
		},
		{Columns: cols("Total"), Rows: []azlogs.Row{{float64(1)}}},
	}
}

func window() (time.Time, time.Time) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return start, start.Add(24 * time.Hour)
}

func TestQueryPlatformLogs_NilBody(t *testing.T) {
	resp, err := platformHandler(&stubQueryAPI{}).QueryPlatformLogs(
		context.Background(), gen.QueryPlatformLogsRequestObject{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryPlatformLogs400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

func TestQueryPlatformLogs_Success(t *testing.T) {
	start, end := window()
	api := &stubQueryAPI{tables: recordTables()}

	resp, err := platformHandler(api).QueryPlatformLogs(context.Background(),
		gen.QueryPlatformLogsRequestObject{Body: &gen.PlatformLogsQueryRequest{
			StartTime: start, EndTime: end,
		}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, isOK := resp.(gen.QueryPlatformLogs200JSONResponse)
	if !isOK {
		t.Fatalf("got %T, want 200", resp)
	}
	if len(ok.Logs) != 1 || ok.Total != 1 {
		t.Fatalf("got %d logs total=%d, want 1 and 1", len(ok.Logs), ok.Total)
	}
	log := ok.Logs[0]
	if log.Log != "reconcile failed" {
		t.Errorf("log = %q", log.Log)
	}
	if log.ClusterInstance == nil || *log.ClusterInstance != "aks-prod-01" {
		t.Errorf("clusterInstance not returned: %+v", log.ClusterInstance)
	}
	if log.NodeName == nil || *log.NodeName != "aks-node-1" {
		t.Errorf("nodeName not returned: %+v", log.NodeName)
	}
	if log.Labels == nil || (*log.Labels)["openchoreo.dev/plane"] != "controlplane" {
		t.Errorf("labels not returned verbatim: %+v", log.Labels)
	}
	// ContainerLogV2 carries no pod IP, so the optional field stays unset
	// rather than being filled with something misleading.
	if log.PodIp != nil {
		t.Errorf("podIp = %v, want nil - Log Analytics has no pod IP", *log.PodIp)
	}
}

func TestQueryPlatformLogs_FiltersReachTheQuery(t *testing.T) {
	start, end := window()
	api := &stubQueryAPI{tables: recordTables()}

	clusters := []string{"aks-prod-01"}
	namespaces := []string{"openchoreo-control-plane"}
	labels := map[string]string{"openchoreo.dev/plane": "controlplane"}
	levels := []gen.PlatformLogsQueryRequestLogLevels{gen.PlatformLogsQueryRequestLogLevelsERROR}

	if _, err := platformHandler(api).QueryPlatformLogs(context.Background(),
		gen.QueryPlatformLogsRequestObject{Body: &gen.PlatformLogsQueryRequest{
			StartTime:       start,
			EndTime:         end,
			ClusterInstance: &clusters,
			Namespace:       &namespaces,
			Labels:          &labels,
			LogLevels:       &levels,
		}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{
		`| where ClusterInstance in~ ("aks-prod-01")`,
		`| where PodNamespace in ("openchoreo-control-plane")`,
		`["openchoreo.dev/plane"]) == "controlplane"`,
		`| where Level in ("ERROR")`,
	} {
		if !strings.Contains(api.lastKQL, want) {
			t.Errorf("query is missing %q\n%s", want, api.lastKQL)
		}
	}
}

// A malformed label key is refused rather than dropped: dropping a filter
// widens the query, returning records the caller never selected.
func TestQueryPlatformLogs_RejectsMalformedLabelKey(t *testing.T) {
	start, end := window()
	labels := map[string]string{`bad key" or true or "`: "x"}

	resp, err := platformHandler(&stubQueryAPI{}).QueryPlatformLogs(context.Background(),
		gen.QueryPlatformLogsRequestObject{Body: &gen.PlatformLogsQueryRequest{
			StartTime: start, EndTime: end, Labels: &labels,
		}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bad, ok := resp.(gen.QueryPlatformLogs400JSONResponse)
	if !ok {
		t.Fatalf("got %T, want 400", resp)
	}
	if bad.Message == nil || !strings.Contains(*bad.Message, "not a valid Kubernetes label key") {
		t.Errorf("unhelpful message: %+v", bad.Message)
	}
}

func TestQueryPlatformLogs_RejectsBadWindowAndLimit(t *testing.T) {
	start, end := window()
	limit := 5000

	cases := map[string]gen.PlatformLogsQueryRequest{
		"end before start": {StartTime: end, EndTime: start},
		"limit over cap":   {StartTime: start, EndTime: end, Limit: &limit},
	}
	for name, body := range cases {
		b := body
		resp, err := platformHandler(&stubQueryAPI{}).QueryPlatformLogs(context.Background(),
			gen.QueryPlatformLogsRequestObject{Body: &b})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if _, ok := resp.(gen.QueryPlatformLogs400JSONResponse); !ok {
			t.Errorf("%s: got %T, want 400", name, resp)
		}
	}
}

func TestQueryPlatformLogs_BackendFailure(t *testing.T) {
	start, end := window()
	api := &stubQueryAPI{err: context.DeadlineExceeded}

	resp, err := platformHandler(api).QueryPlatformLogs(context.Background(),
		gen.QueryPlatformLogsRequestObject{Body: &gen.PlatformLogsQueryRequest{
			StartTime: start, EndTime: end,
		}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryPlatformLogs500JSONResponse); !ok {
		t.Errorf("got %T, want 500", resp)
	}
}

func TestQueryPlatformLogFilterValues_Success(t *testing.T) {
	start, end := window()
	api := &stubQueryAPI{tables: []azlogs.Table{
		{Columns: cols("Value", "Count"), Rows: []azlogs.Row{
			{"openchoreo-control-plane", float64(120)},
			{"cert-manager", float64(30)},
		}},
		{Columns: cols("TotalValues"), Rows: []azlogs.Row{{float64(2)}}},
	}}

	resp, err := platformHandler(api).QueryPlatformLogFilterValues(context.Background(),
		gen.QueryPlatformLogFilterValuesRequestObject{Body: &gen.PlatformLogFilterValuesRequest{
			Filter: gen.Namespace,
			Query:  gen.PlatformLogsQueryRequest{StartTime: start, EndTime: end},
		}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ok, isOK := resp.(gen.QueryPlatformLogFilterValues200JSONResponse)
	if !isOK {
		t.Fatalf("got %T, want 200", resp)
	}
	if ok.Filter != "namespace" || len(ok.Values) != 2 || ok.TotalValues != 2 {
		t.Errorf("unexpected response: %+v", ok)
	}
}

// limit and sortOrder are a record page size and ordering; no records are
// returned here, so they are accepted and ignored rather than rejected.
func TestQueryPlatformLogFilterValues_IgnoresLimitAndSortOrder(t *testing.T) {
	start, end := window()
	api := &stubQueryAPI{tables: []azlogs.Table{
		{Columns: cols("Value", "Count"), Rows: []azlogs.Row{{"a", float64(1)}}},
		{Columns: cols("TotalValues"), Rows: []azlogs.Row{{float64(1)}}},
	}}

	limit := 7
	sortOrder := gen.PlatformLogsQueryRequestSortOrderAsc

	resp, err := platformHandler(api).QueryPlatformLogFilterValues(context.Background(),
		gen.QueryPlatformLogFilterValuesRequestObject{Body: &gen.PlatformLogFilterValuesRequest{
			Filter: gen.PodName,
			Query: gen.PlatformLogsQueryRequest{
				StartTime: start, EndTime: end, Limit: &limit, SortOrder: &sortOrder,
			},
		}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryPlatformLogFilterValues200JSONResponse); !ok {
		t.Fatalf("got %T, want 200", resp)
	}
	if strings.Contains(api.lastKQL, "| take 7") || strings.Contains(api.lastKQL, "order by") {
		t.Errorf("record paging leaked into the values query\n%s", api.lastKQL)
	}
}

func TestQueryPlatformLogFilterValues_ClampsMaxValues(t *testing.T) {
	start, end := window()
	api := &stubQueryAPI{tables: []azlogs.Table{
		{Columns: cols("Value", "Count"), Rows: []azlogs.Row{{"a", float64(1)}}},
		{Columns: cols("TotalValues"), Rows: []azlogs.Row{{float64(1)}}},
	}}

	maxValues := 99999
	if _, err := platformHandler(api).QueryPlatformLogFilterValues(context.Background(),
		gen.QueryPlatformLogFilterValuesRequestObject{Body: &gen.PlatformLogFilterValuesRequest{
			Filter:    gen.ContainerName,
			MaxValues: &maxValues,
			Query:     gen.PlatformLogsQueryRequest{StartTime: start, EndTime: end},
		}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(api.lastKQL, "| take 1000") {
		t.Errorf("maxValues was not clamped to the contract cap\n%s", api.lastKQL)
	}
}

func TestQueryPlatformLogFilterValues_RejectsUnlistableFilter(t *testing.T) {
	start, end := window()
	resp, err := platformHandler(&stubQueryAPI{}).QueryPlatformLogFilterValues(context.Background(),
		gen.QueryPlatformLogFilterValuesRequestObject{Body: &gen.PlatformLogFilterValuesRequest{
			Filter: gen.PlatformLogFilterValuesRequestFilter("labels"),
			Query:  gen.PlatformLogsQueryRequest{StartTime: start, EndTime: end},
		}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryPlatformLogFilterValues400JSONResponse); !ok {
		t.Errorf("got %T, want 400", resp)
	}
}

// The observer negotiates capability at runtime: an adapter that cannot
// answer a signal must say 501, which the observer surfaces verbatim. A 404
// would read as a misrouted request instead.
func TestUnsupportedSignalsAnswer501(t *testing.T) {
	h := platformHandler(&stubQueryAPI{})
	ctx := context.Background()

	// errorCode is checked on each: the contract's 501 examples leave it empty,
	// but every other status this module serves carries one.
	cases := []struct {
		name string
		call func() (any, error)
		want func(any) (*string, bool)
	}{
		{
			"audit logs",
			func() (any, error) { return h.QueryAuditLogs(ctx, gen.QueryAuditLogsRequestObject{}) },
			func(r any) (*string, bool) {
				v, ok := r.(gen.QueryAuditLogs501JSONResponse)
				return v.ErrorCode, ok
			},
		},
		{
			"audit log filter values",
			func() (any, error) {
				return h.QueryAuditLogFilterValues(ctx, gen.QueryAuditLogFilterValuesRequestObject{})
			},
			func(r any) (*string, bool) {
				v, ok := r.(gen.QueryAuditLogFilterValues501JSONResponse)
				return v.ErrorCode, ok
			},
		},
	}

	for _, c := range cases {
		resp, err := c.call()
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		code, ok := c.want(resp)
		if !ok {
			t.Errorf("%s: got %T, want 501", c.name, resp)
			continue
		}
		if code == nil || *code == "" {
			t.Errorf("%s: 501 should carry an error code", c.name)
		}
	}
}

// The observer caps valueSearch too, but an adapter runs without in-process
// auth and does not assume the observer is its only caller.
func TestQueryPlatformLogFilterValues_RejectsOverlongValueSearch(t *testing.T) {
	start, end := window()
	long := strings.Repeat("a", 257)

	resp, err := platformHandler(&stubQueryAPI{}).QueryPlatformLogFilterValues(context.Background(),
		gen.QueryPlatformLogFilterValuesRequestObject{Body: &gen.PlatformLogFilterValuesRequest{
			Filter:      gen.Namespace,
			ValueSearch: &long,
			Query:       gen.PlatformLogsQueryRequest{StartTime: start, EndTime: end},
		}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bad, ok := resp.(gen.QueryPlatformLogFilterValues400JSONResponse)
	if !ok {
		t.Fatalf("got %T, want 400", resp)
	}
	if bad.Message == nil || !strings.Contains(*bad.Message, "valueSearch") {
		t.Errorf("unhelpful message: %+v", bad.Message)
	}
}

// 0 means "not supplied" in the contract and in the observer, so it must reach
// the query as the default rather than as a single value.
func TestQueryPlatformLogFilterValues_ZeroMaxValuesMeansDefault(t *testing.T) {
	start, end := window()
	api := &stubQueryAPI{tables: []azlogs.Table{
		{Columns: cols("Value", "Count"), Rows: []azlogs.Row{{"a", float64(1)}}},
		{Columns: cols("TotalValues"), Rows: []azlogs.Row{{float64(1)}}},
	}}

	zero := 0
	if _, err := platformHandler(api).QueryPlatformLogFilterValues(context.Background(),
		gen.QueryPlatformLogFilterValuesRequestObject{Body: &gen.PlatformLogFilterValuesRequest{
			Filter:    gen.Namespace,
			MaxValues: &zero,
			Query:     gen.PlatformLogsQueryRequest{StartTime: start, EndTime: end},
		}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(api.lastKQL, "| take 100") {
		t.Errorf("maxValues 0 should mean the default, not take 1\n%s", api.lastKQL)
	}
}

// The contract's maxLength counts characters, so a multi-byte phrase within the
// limit must be accepted.
func TestQueryPlatformLogFilterValues_ValueSearchCountsCharacters(t *testing.T) {
	start, end := window()
	api := &stubQueryAPI{tables: []azlogs.Table{
		{Columns: cols("Value", "Count"), Rows: []azlogs.Row{{"a", float64(1)}}},
		{Columns: cols("TotalValues"), Rows: []azlogs.Row{{float64(1)}}},
	}}

	// 256 characters, 768 bytes.
	wide := strings.Repeat("\u65e5", 256)
	resp, err := platformHandler(api).QueryPlatformLogFilterValues(context.Background(),
		gen.QueryPlatformLogFilterValuesRequestObject{Body: &gen.PlatformLogFilterValuesRequest{
			Filter:      gen.Namespace,
			ValueSearch: &wide,
			Query:       gen.PlatformLogsQueryRequest{StartTime: start, EndTime: end},
		}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(gen.QueryPlatformLogFilterValues200JSONResponse); !ok {
		t.Errorf("a 256-character multi-byte search should be accepted, got %T", resp)
	}
}
