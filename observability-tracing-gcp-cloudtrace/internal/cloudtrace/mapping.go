// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudtrace

import (
	"fmt"
	"strconv"
	"strings"

	"cloud.google.com/go/trace/apiv1/tracepb"
)

// Label keys the googlecloud exporter (and the Cloud Trace v2->v1
// conversion) uses to carry span metadata that has no dedicated field in the
// v1 TraceSpan message.
const (
	labelStatusCode     = "g.co/status/code" // google.rpc.Code as decimal; 0 == OK
	labelOtelStatusCode = "otel.status_code" // "OK" / "ERROR" / "UNSET"
	labelHTTPStatusCode = "/http/status_code"
	labelErrorFlag      = "error"
)

// SpanIDHex formats a v1 span ID (fixed64) as the 16-char lowercase hex
// string used by OTLP and the sibling adapters.
func SpanIDHex(id uint64) string {
	return fmt.Sprintf("%016x", id)
}

// ParseSpanID parses a 16-char (or shorter) hex span ID back to the v1
// fixed64 form. The handler validates the input with ValidID first; the
// length check here rejects overlong IDs whose leading zeros would let them
// slip through ParseUint's 64-bit bound.
func ParseSpanID(s string) (uint64, error) {
	if len(s) > 16 {
		return 0, fmt.Errorf("span id longer than 16 hex chars: %q", s)
	}
	return strconv.ParseUint(s, 16, 64)
}

// mapSpan converts one v1 TraceSpan. includeAttributes controls whether the
// labels map is split and attached, mirroring the query cost knob in the
// adapter API.
func mapSpan(s *tracepb.TraceSpan, includeAttributes bool) Span {
	span := Span{
		SpanID:   SpanIDHex(s.GetSpanId()),
		Name:     s.GetName(),
		SpanKind: spanKind(s),
		Status:   statusFromLabels(s.GetLabels()),
	}
	if pid := s.GetParentSpanId(); pid != 0 {
		span.ParentSpanID = SpanIDHex(pid)
	}
	if ts := s.GetStartTime(); ts != nil {
		span.StartTime = ts.AsTime()
	}
	if ts := s.GetEndTime(); ts != nil {
		span.EndTime = ts.AsTime()
	}
	if !span.StartTime.IsZero() && !span.EndTime.IsZero() {
		span.DurationNanoseconds = span.EndTime.Sub(span.StartTime).Nanoseconds()
	}
	if includeAttributes && len(s.GetLabels()) > 0 {
		span.Attributes, span.ResourceAttributes = splitAttributes(s.GetLabels())
	}
	return span
}

// spanKind derives the span kind. The v1 enum only distinguishes RPC_SERVER
// and RPC_CLIENT; other OTel kinds (INTERNAL, PRODUCER, CONSUMER) arrive as
// SPAN_KIND_UNSPECIFIED, so fall back to the exporter's kind label before
// giving up.
func spanKind(s *tracepb.TraceSpan) string {
	switch s.GetKind() {
	case tracepb.TraceSpan_RPC_SERVER:
		return "SERVER"
	case tracepb.TraceSpan_RPC_CLIENT:
		return "CLIENT"
	}
	for _, key := range []string{"g.co/spankind", "/span/kind", "span.kind"} {
		if v := s.GetLabels()[key]; v != "" {
			return strings.ToUpper(v)
		}
	}
	return "INTERNAL"
}

// statusFromLabels maps Cloud Trace status labels onto the adapter's
// ok/error/unset convention. The OTel status written via BatchWriteSpans
// surfaces in v1 as the g.co/status/code label (google.rpc.Code, 0 == OK);
// HTTP status is the fallback signal.
func statusFromLabels(labels map[string]string) string {
	if v, ok := labels[labelStatusCode]; ok {
		if v == "0" {
			return "ok"
		}
		return "error"
	}
	switch strings.ToUpper(labels[labelOtelStatusCode]) {
	case "OK":
		return "ok"
	case "ERROR":
		return "error"
	}
	if strings.EqualFold(labels[labelErrorFlag], "true") {
		return "error"
	}
	if v, ok := labels[labelHTTPStatusCode]; ok {
		if code, err := strconv.Atoi(v); err == nil {
			if code >= 500 {
				return "error"
			}
			return "ok"
		}
	}
	return "unset"
}

// splitAttributes reconstructs the span-vs-resource attribute split that the
// exporter flattened into one labels map, using key prefixes. Unmatched keys
// are treated as span attributes.
func splitAttributes(labels map[string]string) (attrs, resAttrs map[string]interface{}) {
	attrs = make(map[string]interface{})
	resAttrs = make(map[string]interface{})
	for k, v := range labels {
		if isResourceAttribute(k) {
			resAttrs[k] = v
		} else {
			attrs[k] = v
		}
	}
	return attrs, resAttrs
}

func isResourceAttribute(key string) bool {
	for _, prefix := range resourceAttributePrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// summarizeTrace computes the per-trace summary from a COMPLETE-view trace.
// The root span is the one without a parent; when it is sampled away or
// outside the window the earliest span stands in, matching the sibling
// adapters' fallback.
func summarizeTrace(t *tracepb.Trace) TraceEntry {
	entry := TraceEntry{
		TraceID:   t.GetTraceId(),
		SpanCount: len(t.GetSpans()),
	}

	var root, earliest *tracepb.TraceSpan
	for _, s := range t.GetSpans() {
		start, end := s.GetStartTime().AsTime(), s.GetEndTime().AsTime()
		if s.GetStartTime() != nil {
			if entry.StartTime.IsZero() || start.Before(entry.StartTime) {
				entry.StartTime = start
			}
			if earliest == nil || start.Before(earliest.GetStartTime().AsTime()) {
				earliest = s
			}
		}
		if s.GetEndTime() != nil && end.After(entry.EndTime) {
			entry.EndTime = end
		}
		if s.GetParentSpanId() == 0 && root == nil {
			root = s
		}
		if !entry.HasErrors && statusFromLabels(s.GetLabels()) == "error" {
			entry.HasErrors = true
		}
	}

	if root == nil {
		root = earliest
	}
	if root != nil {
		entry.RootSpanID = SpanIDHex(root.GetSpanId())
		entry.RootSpanName = root.GetName()
		entry.RootSpanKind = spanKind(root)
		entry.TraceName = root.GetName()
	}
	if !entry.StartTime.IsZero() && !entry.EndTime.IsZero() {
		entry.DurationNs = entry.EndTime.Sub(entry.StartTime).Nanoseconds()
	}
	return entry
}
