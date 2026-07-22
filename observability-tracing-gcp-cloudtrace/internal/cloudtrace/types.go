// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

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

// Span is one mapped Cloud Trace v1 span. IDs are formatted as 16-char hex
// strings (the v1 API returns them as uint64), following the OTLP ID
// convention. ParentSpanID is "" for root spans.
type Span struct {
	SpanID              string
	Name                string
	SpanKind            string
	ParentSpanID        string
	StartTime           time.Time
	EndTime             time.Time
	DurationNanoseconds int64
	Status              string // "ok", "error", or "unset"
	StatusMessage       string // human-readable description, set only on error
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
