// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

import "time"

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
	Status              string
	Attributes          map[string]interface{}
	ResourceAttributes  map[string]interface{}
}

type TracesResult struct {
	Traces []TraceEntry
	Total  int
	TookMs int
}

type SpansResult struct {
	Spans  []Span
	Total  int
	TookMs int
}
