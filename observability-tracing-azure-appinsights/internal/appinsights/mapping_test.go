// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

func tableResponse(colNames []string, rows []azlogs.Row) azlogs.QueryWorkspaceResponse {
	cols := make([]azlogs.Column, 0, len(colNames))
	for _, name := range colNames {
		cols = append(cols, azlogs.Column{Name: to.Ptr(name)})
	}
	return azlogs.QueryWorkspaceResponse{
		QueryResults: azlogs.QueryResults{
			Tables: []azlogs.Table{{Columns: cols, Rows: rows}},
		},
	}
}

// Column layout and cell values mirror rows captured from the Phase 0 spike
// on oc-obs-dev-aks (telemetrygen traces exported by azuremonitorexporter).
func TestMapTraceRows(t *testing.T) {
	resp := tableResponse(
		[]string{"TraceId", "SpanCount", "ErrorCount", "RootSpanId", "RootSpanName", "RootSpanKind", "StartTime", "EndTime"},
		[]azlogs.Row{
			{"4372fc01295900a9", float64(4), float64(0), "2419e7552dfbe055", "lets-go", "CLIENT", "2026-06-11T12:17:35.123Z", "2026-06-11T12:17:35.246Z"},
			{"86b5780d3ab169d1", float64(4), float64(2), "20f2162fb66e8810", "lets-go", "CLIENT", "2026-06-11T12:17:36.000Z", "2026-06-11T12:17:36.500Z"},
		},
	)

	traces, err := mapTraceRows(resp)
	if err != nil {
		t.Fatalf("mapTraceRows: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("got %d traces, want 2", len(traces))
	}

	first := traces[0]
	if first.TraceID != "4372fc01295900a9" {
		t.Errorf("TraceID = %q", first.TraceID)
	}
	if first.SpanCount != 4 {
		t.Errorf("SpanCount = %d, want 4", first.SpanCount)
	}
	if first.TraceName != "lets-go" || first.RootSpanName != "lets-go" {
		t.Errorf("TraceName/RootSpanName = %q/%q", first.TraceName, first.RootSpanName)
	}
	if first.HasErrors {
		t.Error("HasErrors = true, want false")
	}
	if first.DurationNs != 123*int64(time.Millisecond) {
		t.Errorf("DurationNs = %d, want %d", first.DurationNs, 123*int64(time.Millisecond))
	}
	if !traces[1].HasErrors {
		t.Error("second trace HasErrors = false, want true")
	}
}

func TestMapSpanRows(t *testing.T) {
	resp := tableResponse(
		[]string{"TimeGenerated", "SpanId", "ParentSpanId", "Name", "SpanKind", "DurationMs", "Success", "SpanEnd", "Properties", "Measurements"},
		[]azlogs.Row{
			{
				"2026-06-11T12:17:35.123Z", "2419e7552dfbe055", "", "lets-go", "CLIENT",
				float64(0.123), true, "2026-06-11T12:17:35.123123Z",
				`{"openchoreo.dev/namespace":"spike-ns","openchoreo.dev/component-uid":"test-comp-123","k8s.pod.name":"telemetrygen","peer.service":"telemetrygen-server","service.name":"spike-checkout"}`,
				`{"retry.count":2}`,
			},
			{
				"2026-06-11T12:17:35.124Z", "4eb9f14da3aeacea", "2419e7552dfbe055", "okey-dokey-0", "SERVER",
				float64(0.1), false, "2026-06-11T12:17:35.124100Z",
				nil, nil,
			},
		},
	)

	spans, err := mapSpanRows(resp)
	if err != nil {
		t.Fatalf("mapSpanRows: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}

	root := spans[0]
	if root.SpanID != "2419e7552dfbe055" || root.ParentSpanID != "" {
		t.Errorf("root SpanID/ParentSpanID = %q/%q", root.SpanID, root.ParentSpanID)
	}
	if root.SpanKind != "CLIENT" {
		t.Errorf("SpanKind = %q", root.SpanKind)
	}
	if root.Status != "ok" {
		t.Errorf("Status = %q, want ok", root.Status)
	}
	if root.DurationNanoseconds != 123000 {
		t.Errorf("DurationNanoseconds = %d, want 123000", root.DurationNanoseconds)
	}

	// Resource/span attribute split by key prefix.
	if _, ok := root.ResourceAttributes["openchoreo.dev/namespace"]; !ok {
		t.Error("openchoreo.dev/namespace missing from ResourceAttributes")
	}
	if _, ok := root.ResourceAttributes["k8s.pod.name"]; !ok {
		t.Error("k8s.pod.name missing from ResourceAttributes")
	}
	if _, ok := root.ResourceAttributes["service.name"]; !ok {
		t.Error("service.name missing from ResourceAttributes")
	}
	if _, ok := root.Attributes["peer.service"]; !ok {
		t.Error("peer.service missing from Attributes")
	}
	if v, ok := root.Attributes["retry.count"]; !ok || v != float64(2) {
		t.Errorf("Measurements not merged into Attributes: %v", root.Attributes)
	}

	child := spans[1]
	if child.ParentSpanID != "2419e7552dfbe055" {
		t.Errorf("child ParentSpanID = %q", child.ParentSpanID)
	}
	if child.Status != "error" {
		t.Errorf("child Status = %q, want error", child.Status)
	}
	if child.Attributes != nil || child.ResourceAttributes != nil {
		t.Error("expected nil attribute maps when Properties is nil")
	}
}

func TestMapSpanRows_MeasurementsWithoutProperties(t *testing.T) {
	// A span can carry Measurements (numeric attributes) even when Properties
	// is empty; those attributes must not be dropped.
	resp := tableResponse(
		[]string{"TimeGenerated", "SpanId", "ParentSpanId", "Name", "SpanKind", "DurationMs", "Success", "SpanEnd", "Properties", "Measurements"},
		[]azlogs.Row{
			{
				"2026-06-11T12:17:35.123Z", "a1", "", "n", "SERVER",
				float64(1), true, "2026-06-11T12:17:35.124Z",
				nil, `{"retry.count":2}`,
			},
		},
	)
	spans, err := mapSpanRows(resp)
	if err != nil {
		t.Fatalf("mapSpanRows: %v", err)
	}
	if v, ok := spans[0].Attributes["retry.count"]; !ok || v != float64(2) {
		t.Errorf("Measurements not merged when Properties nil: %v", spans[0].Attributes)
	}
	if spans[0].ResourceAttributes != nil {
		t.Errorf("ResourceAttributes = %v, want nil", spans[0].ResourceAttributes)
	}
}

func TestMapSpanRows_SuccessAsString(t *testing.T) {
	// The query API can deliver booleans as strings depending on column type.
	resp := tableResponse(
		[]string{"TimeGenerated", "SpanId", "ParentSpanId", "Name", "SpanKind", "DurationMs", "Success", "SpanEnd"},
		[]azlogs.Row{
			{"2026-06-11T12:17:35.123Z", "a1", "", "n", "SERVER", float64(1), "False", "2026-06-11T12:17:35.124Z"},
			{"2026-06-11T12:17:35.123Z", "a2", "", "n", "SERVER", float64(1), nil, "2026-06-11T12:17:35.124Z"},
		},
	)
	spans, err := mapSpanRows(resp)
	if err != nil {
		t.Fatalf("mapSpanRows: %v", err)
	}
	if spans[0].Status != "error" {
		t.Errorf("Status = %q, want error", spans[0].Status)
	}
	if spans[1].Status != "unset" {
		t.Errorf("Status = %q, want unset", spans[1].Status)
	}
}

func TestMapTraceRows_NoTables(t *testing.T) {
	if _, err := mapTraceRows(azlogs.QueryWorkspaceResponse{}); err == nil {
		t.Error("expected error for empty response")
	}
}
