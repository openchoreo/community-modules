// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

func mapTraceRows(resp azlogs.QueryWorkspaceResponse) ([]TraceEntry, error) {
	rows, idx, err := primaryTable(resp)
	if err != nil {
		return nil, err
	}

	out := make([]TraceEntry, 0, len(rows))
	for _, row := range rows {
		entry := TraceEntry{
			TraceID:      rowString(row, idx, "TraceId"),
			SpanCount:    rowInt(row, idx, "SpanCount"),
			RootSpanID:   rowString(row, idx, "RootSpanId"),
			RootSpanName: rowString(row, idx, "RootSpanName"),
			RootSpanKind: rowString(row, idx, "RootSpanKind"),
			StartTime:    rowTime(row, idx, "StartTime"),
			EndTime:      rowTime(row, idx, "EndTime"),
			HasErrors:    rowInt(row, idx, "ErrorCount") > 0,
		}
		entry.TraceName = entry.RootSpanName
		if !entry.StartTime.IsZero() && !entry.EndTime.IsZero() {
			entry.DurationNs = entry.EndTime.Sub(entry.StartTime).Nanoseconds()
		}
		out = append(out, entry)
	}
	return out, nil
}

func mapSpanRows(resp azlogs.QueryWorkspaceResponse) ([]Span, error) {
	rows, idx, err := primaryTable(resp)
	if err != nil {
		return nil, err
	}

	out := make([]Span, 0, len(rows))
	for _, row := range rows {
		span := Span{
			SpanID:       rowString(row, idx, "SpanId"),
			Name:         rowString(row, idx, "Name"),
			SpanKind:     rowString(row, idx, "SpanKind"),
			ParentSpanID: rowString(row, idx, "ParentSpanId"),
			StartTime:    rowTime(row, idx, "TimeGenerated"),
			EndTime:      rowTime(row, idx, "SpanEnd"),
			Status:       statusFromSuccess(row, idx),
		}
		// DurationMs is float milliseconds; ns precision below 1ms survives
		// the multiplication because the column keeps fractional ms.
		span.DurationNanoseconds = int64(rowFloat(row, idx, "DurationMs") * 1e6)

		if props := rowDynamic(row, idx, "Properties"); props != nil {
			span.Attributes, span.ResourceAttributes = splitAttributes(props)
		}
		// Measurements carry numeric span attributes and can be present even
		// when Properties is empty, so merge them independently.
		if m := rowDynamic(row, idx, "Measurements"); m != nil {
			if span.Attributes == nil {
				span.Attributes = make(map[string]interface{}, len(m))
			}
			for k, v := range m {
				span.Attributes[k] = v
			}
		}
		out = append(out, span)
	}
	return out, nil
}

// splitAttributes reconstructs the span-vs-resource attribute split that the
// azuremonitor exporter flattened into one Properties bag, using key
// prefixes. Unmatched keys are treated as span attributes.
func splitAttributes(props map[string]interface{}) (attrs, resAttrs map[string]interface{}) {
	attrs = make(map[string]interface{})
	resAttrs = make(map[string]interface{})
	for k, v := range props {
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

// statusFromSuccess maps the App Insights Success column onto the adapter
// API status enum ("ok" / "error" / "unset").
func statusFromSuccess(row azlogs.Row, idx map[string]int) string {
	i, ok := idx["Success"]
	if !ok || i >= len(row) || row[i] == nil {
		return "unset"
	}
	switch v := row[i].(type) {
	case bool:
		if v {
			return "ok"
		}
		return "error"
	case string:
		if strings.EqualFold(v, "true") {
			return "ok"
		}
		if strings.EqualFold(v, "false") {
			return "error"
		}
		return "unset"
	default:
		return "unset"
	}
}

func primaryTable(resp azlogs.QueryWorkspaceResponse) ([]azlogs.Row, map[string]int, error) {
	if len(resp.Tables) == 0 {
		return nil, nil, errors.New("appinsights: response had no tables")
	}
	table := resp.Tables[0]
	idx, err := buildColumnIndex(table.Columns)
	if err != nil {
		return nil, nil, err
	}
	return table.Rows, idx, nil
}

func buildColumnIndex(cols []azlogs.Column) (map[string]int, error) {
	if len(cols) == 0 {
		return nil, errors.New("appinsights: response had no columns")
	}
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		if c.Name == nil {
			return nil, fmt.Errorf("appinsights: column %d has no name", i)
		}
		idx[*c.Name] = i
	}
	return idx, nil
}

// rowString returns the string value at the named column, or "" if the row
// is too short or the cell is nil/non-string.
func rowString(row azlogs.Row, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i >= len(row) {
		return ""
	}
	switch v := row[i].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// rowInt parses an integer cell; azlogs may decode counts as float64,
// json.Number, or strings depending on column type.
func rowInt(row azlogs.Row, idx map[string]int, name string) int {
	return int(rowFloat(row, idx, name))
}

func rowFloat(row azlogs.Row, idx map[string]int, name string) float64 {
	i, ok := idx[name]
	if !ok || i >= len(row) || row[i] == nil {
		return 0
	}
	switch v := row[i].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	default:
		return 0
	}
}

// rowTime parses an ISO-8601 timestamp cell. azlogs returns datetime values
// as RFC3339Nano strings. Returns the zero time on any failure.
func rowTime(row azlogs.Row, idx map[string]int, name string) time.Time {
	s := rowString(row, idx, name)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// rowDynamic parses a dynamic (JSON) cell. The query API returns dynamic
// columns as JSON-encoded strings; decoded maps are also handled in case the
// SDK changes representation.
func rowDynamic(row azlogs.Row, idx map[string]int, name string) map[string]interface{} {
	i, ok := idx[name]
	if !ok || i >= len(row) || row[i] == nil {
		return nil
	}
	switch v := row[i].(type) {
	case map[string]interface{}:
		return v
	case string:
		if v == "" {
			return nil
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return nil
		}
		return m
	default:
		return nil
	}
}
