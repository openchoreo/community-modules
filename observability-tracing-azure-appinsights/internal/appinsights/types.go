// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

import "time"

// TracesParams carries the validated inputs for the traces list and span
// queries. UIDs are optional; Namespace is required and enforced by the
// handler before a query is built.
type TracesParams struct {
	StartTime         time.Time
	EndTime           time.Time
	Namespace         string
	ComponentUID      string
	ProjectUID        string
	EnvironmentUID    string
	TraceID           string
	Limit             int
	SortOrder         string // "asc" or "desc"
	IncludeAttributes bool
}

// TraceEntry is one row of the traces list query: a per-OperationId summary
// computed by the KQL summarize.
type TraceEntry struct {
	TraceID      string
	TraceName    string
	StartTime    time.Time
	EndTime      time.Time
	DurationNs   int64
	SpanCount    int
	RootSpanID   string
	RootSpanName string
	RootSpanKind string
	HasErrors    bool
}

// Span is one row of the span queries. ParentSpanID is normalized: the
// azuremonitor exporter writes ParentId == OperationId for root spans, which
// the mapping layer converts to "" so consumers can rely on the same
// root-span convention as the sibling adapters.
type Span struct {
	SpanID              string
	Name                string
	SpanKind            string // SERVER (AppRequests) or CLIENT (AppDependencies)
	ParentSpanID        string
	StartTime           time.Time
	EndTime             time.Time
	DurationNanoseconds int64
	Status              string // "ok" or "error"
	Attributes          map[string]interface{}
	ResourceAttributes  map[string]interface{}
}

// TracesResult is the traces list query output.
type TracesResult struct {
	Traces []TraceEntry
	Total  int
	TookMs int
}

// SpansResult is the span list query output.
type SpansResult struct {
	Spans  []Span
	Total  int
	TookMs int
}
