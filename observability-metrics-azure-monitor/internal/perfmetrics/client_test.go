// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

// fakeAPI is an azlogsAPI test double.
type fakeAPI struct {
	resp    azlogs.QueryWorkspaceResponse
	err     error
	lastKQL string
}

func (f *fakeAPI) QueryWorkspace(_ context.Context, _ string, body azlogs.QueryBody,
	_ *azlogs.QueryWorkspaceOptions) (azlogs.QueryWorkspaceResponse, error) {
	if body.Query != nil {
		f.lastKQL = *body.Query
	}
	return f.resp, f.err
}

func col(name string) azlogs.Column { return azlogs.Column{Name: to.Ptr(name)} }

// resourceResponse builds a Perf result table with the three projected columns.
func resourceResponse(rows []azlogs.Row) azlogs.QueryWorkspaceResponse {
	var resp azlogs.QueryWorkspaceResponse
	resp.Tables = []azlogs.Table{{
		Name:    to.Ptr("PrimaryResult"),
		Columns: []azlogs.Column{col("CounterName"), col("TimeGenerated"), col("Value")},
		Rows:    rows,
	}}
	return resp
}

func TestGetResourceMetrics_MapsAllSeries(t *testing.T) {
	ts := "2026-06-04T10:00:00Z"
	api := &fakeAPI{resp: resourceResponse([]azlogs.Row{
		{CounterCPUUsageNanoCores, ts, float64(500_000_000)},   // 0.5 cores
		{CounterCPURequestNanoCores, ts, float64(250_000_000)}, // 0.25 cores
		{CounterCPULimitNanoCores, ts, float64(1_000_000_000)}, // 1 core
		{CounterMemoryWorkingSetBytes, ts, float64(128 << 20)}, // 128 MiB
		{CounterMemoryRequestBytes, ts, float64(64 << 20)},
		{CounterMemoryLimitBytes, ts, float64(256 << 20)},
	})}

	c := NewClientWithAPI(api, Config{WorkspaceID: "ws"}, nil)
	res, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "dp-acme-dev",
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
		Step:      5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertSingle(t, "cpuUsage", res.CPUUsage, 0.5)
	assertSingle(t, "cpuRequests", res.CPURequests, 0.25)
	assertSingle(t, "cpuLimits", res.CPULimits, 1.0)
	assertSingle(t, "memoryUsage", res.MemoryUsage, float64(128<<20))
	assertSingle(t, "memoryRequests", res.MemoryRequests, float64(64<<20))
	assertSingle(t, "memoryLimits", res.MemoryLimits, float64(256<<20))
}

func TestGetResourceMetrics_EmptyTable(t *testing.T) {
	api := &fakeAPI{resp: resourceResponse(nil)}
	c := NewClientWithAPI(api, Config{WorkspaceID: "ws"}, nil)

	res, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "ns",
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.CPUUsage) != 0 || len(res.MemoryUsage) != 0 {
		t.Errorf("expected empty series, got cpu=%d mem=%d", len(res.CPUUsage), len(res.MemoryUsage))
	}
}

func TestGetResourceMetrics_TimeValidation(t *testing.T) {
	c := NewClientWithAPI(&fakeAPI{}, Config{WorkspaceID: "ws"}, nil)

	_, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{Namespace: "ns"})
	if err == nil {
		t.Error("expected error for zero times")
	}

	now := time.Now()
	_, err = c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "ns",
		StartTime: now,
		EndTime:   now.Add(-time.Hour), // end before start
	})
	if err == nil {
		t.Error("expected error for end before start")
	}
}

func TestGetResourceMetrics_PropagatesAPIError(t *testing.T) {
	api := &fakeAPI{err: errors.New("boom")}
	c := NewClientWithAPI(api, Config{WorkspaceID: "ws"}, nil)

	_, err := c.GetResourceMetrics(context.Background(), MetricsQueryParams{
		Namespace: "ns",
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
	})
	if err == nil {
		t.Fatal("expected API error to propagate")
	}
}

func TestPing_OK(t *testing.T) {
	api := &fakeAPI{resp: resourceResponse([]azlogs.Row{{CounterCPUUsageNanoCores, "2026-06-04T10:00:00Z", 1.0}})}
	c := NewClientWithAPI(api, Config{WorkspaceID: "ws"}, nil)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("unexpected ping error: %v", err)
	}
}

func TestPing_PropagatesError(t *testing.T) {
	api := &fakeAPI{err: errors.New("unauthorized")}
	c := NewClientWithAPI(api, Config{WorkspaceID: "ws"}, nil)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error")
	}
}

func assertSingle(t *testing.T, name string, pts []TimeValuePoint, want float64) {
	t.Helper()
	if len(pts) != 1 {
		t.Fatalf("%s: expected 1 point, got %d", name, len(pts))
	}
	if pts[0].Value != want {
		t.Errorf("%s: value = %v, want %v", name, pts[0].Value, want)
	}
}
