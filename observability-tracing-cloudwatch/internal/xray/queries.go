// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/xray"
	xraytypes "github.com/aws/aws-sdk-go-v2/service/xray/types"
)

// jsonNumberToNanos converts a JSON number representing epoch seconds
// directly to int64 nanoseconds by parsing the string representation.
// This avoids the precision loss that float64 intermediates cause for
// timestamps with more than ~15 significant digits. X-Ray segment documents
// can encode timestamps in either plain decimal or scientific notation.
func jsonNumberToNanos(n json.Number) int64 {
	s := strings.TrimSpace(n.String())
	if s == "" || s == "0" {
		return 0
	}

	negative := false
	if strings.HasPrefix(s, "-") {
		negative = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}

	exp := 0
	if e := strings.IndexAny(s, "eE"); e >= 0 {
		parsedExp, err := strconv.Atoi(s[e+1:])
		if err != nil {
			return 0
		}
		exp = parsedExp
		s = s[:e]
	}

	fracDigits := 0
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		fracDigits = len(s) - dot - 1
		s = s[:dot] + s[dot+1:]
	}

	s = strings.TrimLeft(s, "0")
	if s == "" {
		return 0
	}

	scale := exp - fracDigits + 9
	switch {
	case scale > 0:
		s += strings.Repeat("0", scale)
	case scale < 0:
		cut := -scale
		if cut >= len(s) {
			return 0
		}
		s = s[:len(s)-cut]
	}

	ns, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	if negative {
		return -ns
	}
	return ns
}

const MaxQueryLimit = 1000

// GetTraces queries X-Ray for trace summaries matching the given parameters.
func (c *Client) GetTraces(ctx context.Context, params TracesQueryParams) (*TracesResult, error) {
	startedAt := time.Now()

	filterExpr := buildFilterExpression(params.Scope)

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}

	input := &xray.GetTraceSummariesInput{
		StartTime:     aws.Time(params.StartTime),
		EndTime:       aws.Time(params.EndTime),
		TimeRangeType: xraytypes.TimeRangeTypeTraceId,
	}
	if filterExpr != "" {
		input.FilterExpression = aws.String(filterExpr)
	}

	var allSummaries []xraytypes.TraceSummary
	var nextToken *string

	for {
		if nextToken != nil {
			input.NextToken = nextToken
		}

		output, err := c.xray.GetTraceSummaries(ctx, input)
		if err != nil {
			c.logger.Error("Failed to get trace summaries from X-Ray", slog.Any("error", err))
			return nil, fmt.Errorf("failed to get trace summaries: %w", err)
		}

		allSummaries = append(allSummaries, output.TraceSummaries...)

		if output.NextToken == nil || len(allSummaries) >= limit {
			break
		}
		nextToken = output.NextToken
	}

	total := len(allSummaries)
	if len(allSummaries) > limit {
		allSummaries = allSummaries[:limit]
	}

	if params.SortOrder == "asc" {
		sortTraceSummariesAsc(allSummaries)
	}

	traces := make([]TraceEntry, 0, len(allSummaries))
	for _, summary := range allSummaries {
		entry := traceSummaryToEntry(summary)
		traces = append(traces, entry)
	}

	c.enrichTraceEntries(ctx, traces)

	tookMs := int(time.Since(startedAt).Milliseconds())
	return &TracesResult{
		Traces: traces,
		Total:  total,
		TookMs: tookMs,
	}, nil
}

// GetSpans queries X-Ray for all spans (segments + subsegments) of a given trace.
func (c *Client) GetSpans(ctx context.Context, params TracesQueryParams) (*SpansResult, error) {
	startedAt := time.Now()

	xrayTraceID := toXRayTraceID(params.TraceID)

	output, err := c.xray.BatchGetTraces(ctx, &xray.BatchGetTracesInput{
		TraceIds: []string{xrayTraceID},
	})
	if err != nil {
		c.logger.Error("Failed to batch get traces from X-Ray", slog.Any("error", err))
		return nil, fmt.Errorf("failed to batch get traces: %w", err)
	}

	if len(output.Traces) == 0 {
		return &SpansResult{
			Spans:  []SpanEntry{},
			Total:  0,
			TookMs: int(time.Since(startedAt).Milliseconds()),
		}, nil
	}

	spans := flattenTrace(output.Traces[0], params.IncludeAttributes)

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}

	total := len(spans)
	if len(spans) > limit {
		spans = spans[:limit]
	}

	return &SpansResult{
		Spans:  spans,
		Total:  total,
		TookMs: int(time.Since(startedAt).Milliseconds()),
	}, nil
}

// GetSpanDetail queries X-Ray for a specific span within a trace.
func (c *Client) GetSpanDetail(ctx context.Context, params TracesQueryParams) (*SpanDetailResult, error) {
	xrayTraceID := toXRayTraceID(params.TraceID)

	output, err := c.xray.BatchGetTraces(ctx, &xray.BatchGetTracesInput{
		TraceIds: []string{xrayTraceID},
	})
	if err != nil {
		c.logger.Error("Failed to batch get traces from X-Ray", slog.Any("error", err))
		return nil, fmt.Errorf("failed to batch get traces: %w", err)
	}

	if len(output.Traces) == 0 {
		return nil, fmt.Errorf("trace not found: traceId=%s", params.TraceID)
	}

	detail, err := findSpanDetail(output.Traces[0], params.SpanID)
	if err != nil {
		return nil, err
	}

	return &SpanDetailResult{Span: *detail}, nil
}

const batchGetTracesMaxIDs = 5

// enrichTraceEntries calls BatchGetTraces to fill in precise durations and span
// counts that the GetTraceSummaries API does not provide at full precision.
func (c *Client) enrichTraceEntries(ctx context.Context, traces []TraceEntry) {
	if len(traces) == 0 {
		return
	}

	traceIndex := make(map[string]int, len(traces))
	var ids []string
	for i, t := range traces {
		xid := toXRayTraceID(t.TraceID)
		ids = append(ids, xid)
		traceIndex[xid] = i
	}

	for start := 0; start < len(ids); start += batchGetTracesMaxIDs {
		end := start + batchGetTracesMaxIDs
		if end > len(ids) {
			end = len(ids)
		}

		out, err := c.xray.BatchGetTraces(ctx, &xray.BatchGetTracesInput{
			TraceIds: ids[start:end],
		})
		if err != nil {
			c.logger.Warn("Failed to enrich trace entries", slog.Any("error", err))
			return
		}

		for _, t := range out.Traces {
			if t.Id == nil {
				continue
			}
			idx, ok := traceIndex[*t.Id]
			if !ok {
				continue
			}
			dur, count, minStartNs, maxEndNs, hasErrors := computeTraceSummary(t)
			traces[idx].DurationNs = dur
			traces[idx].SpanCount = count
			traces[idx].HasErrors = traces[idx].HasErrors || hasErrors
			if minStartNs > 0 {
				traces[idx].StartTime = time.Unix(0, minStartNs)
			}
			if maxEndNs > 0 {
				traces[idx].EndTime = time.Unix(0, maxEndNs)
			}
		}
	}
}

// computeTraceSummary parses all segments in a trace to find the precise
// duration (max end - min start across all segments and subsegments), total
// span count, and whether any segment/subsegment is marked as an error.
func computeTraceSummary(trace xraytypes.Trace) (durationNs int64, spanCount int, minStartNs int64, maxEndNs int64, hasErrors bool) {
	for _, segment := range trace.Segments {
		if segment.Document == nil {
			continue
		}
		var seg xraySegment
		if err := json.Unmarshal([]byte(*segment.Document), &seg); err != nil {
			continue
		}

		segMin, segMax := segmentTimeRangeNanos(&seg)
		if minStartNs == 0 || segMin < minStartNs {
			minStartNs = segMin
		}
		if segMax > maxEndNs {
			maxEndNs = segMax
		}

		spanCount += countSpans(&seg)
		hasErrors = hasErrors || segmentHasErrors(&seg)
	}

	if maxEndNs > minStartNs {
		durationNs = maxEndNs - minStartNs
	}
	return
}

// segmentTimeRangeNanos returns the earliest start and latest end in
// nanoseconds across a segment and all its subsegments recursively.
func segmentTimeRangeNanos(seg *xraySegment) (minStartNs, maxEndNs int64) {
	minStartNs = jsonNumberToNanos(seg.StartTime)
	maxEndNs = jsonNumberToNanos(seg.EndTime)
	for i := range seg.Subsegments {
		subMin, subMax := segmentTimeRangeNanos(&seg.Subsegments[i])
		if subMin < minStartNs {
			minStartNs = subMin
		}
		if subMax > maxEndNs {
			maxEndNs = subMax
		}
	}
	return
}

// countSpans counts a segment plus all its subsegments recursively.
func countSpans(seg *xraySegment) int {
	count := 1
	for i := range seg.Subsegments {
		count += countSpans(&seg.Subsegments[i])
	}
	return count
}

// segmentHasErrors reports whether a segment tree contains any error marker.
func segmentHasErrors(seg *xraySegment) bool {
	if seg.Fault || seg.Error || seg.Throttle {
		return true
	}
	for i := range seg.Subsegments {
		if segmentHasErrors(&seg.Subsegments[i]) {
			return true
		}
	}
	return false
}

// buildFilterExpression constructs an X-Ray filter expression from the search scope.
// X-Ray annotation keys preserve dots and convert slashes/hyphens to underscores.
// Keys containing dots must use annotation[key] filter syntax.
func buildFilterExpression(scope Scope) string {
	var parts []string

	if scope.Namespace != "" {
		parts = append(parts, fmt.Sprintf("annotation[openchoreo.dev_namespace] = %q", scope.Namespace))
	}
	if scope.ProjectID != "" {
		parts = append(parts, fmt.Sprintf("annotation[openchoreo.dev_project_uid] = %q", scope.ProjectID))
	}
	if scope.ComponentID != "" {
		parts = append(parts, fmt.Sprintf("annotation[openchoreo.dev_component_uid] = %q", scope.ComponentID))
	}
	if scope.EnvironmentID != "" {
		parts = append(parts, fmt.Sprintf("annotation[openchoreo.dev_environment_uid] = %q", scope.EnvironmentID))
	}

	return strings.Join(parts, " AND ")
}

// toXRayTraceID converts a plain OTLP trace ID (32 hex chars) to the X-Ray format
// (1-{8 hex timestamp}-{24 hex random}). If the ID is already in X-Ray format, it is
// returned as-is.
func toXRayTraceID(traceID string) string {
	if strings.HasPrefix(traceID, "1-") {
		return traceID
	}
	traceID = strings.TrimSpace(traceID)
	if len(traceID) == 32 {
		return fmt.Sprintf("1-%s-%s", traceID[:8], traceID[8:])
	}
	return traceID
}

// fromXRayTraceID converts an X-Ray trace ID back to a plain 32-char hex string.
func fromXRayTraceID(xrayID string) string {
	if !strings.HasPrefix(xrayID, "1-") {
		return xrayID
	}
	parts := strings.SplitN(xrayID, "-", 3)
	if len(parts) == 3 {
		return parts[1] + parts[2]
	}
	return xrayID
}

// traceSummaryToEntry converts an X-Ray TraceSummary to our TraceEntry model.
func traceSummaryToEntry(summary xraytypes.TraceSummary) TraceEntry {
	entry := TraceEntry{}

	if summary.Id != nil {
		entry.TraceID = fromXRayTraceID(*summary.Id)
	}
	if summary.Duration != nil {
		entry.DurationNs = int64(*summary.Duration * 1e9)
	}
	if summary.HasFault != nil && *summary.HasFault {
		entry.HasErrors = true
	}
	if summary.HasError != nil && *summary.HasError {
		entry.HasErrors = true
	}
	if summary.HasThrottle != nil && *summary.HasThrottle {
		entry.HasErrors = true
	}

	if summary.EntryPoint != nil {
		if summary.EntryPoint.Name != nil {
			entry.RootSpanName = *summary.EntryPoint.Name
			entry.TraceName = *summary.EntryPoint.Name
		}
	}

	if summary.StartTime != nil {
		entry.StartTime = *summary.StartTime
		if entry.DurationNs > 0 {
			entry.EndTime = summary.StartTime.Add(time.Duration(entry.DurationNs))
		}
	}

	return entry
}

// extractStartTimeFromXRayID parses the timestamp portion from an X-Ray trace ID.
// Format: 1-{8 hex epoch}-{24 hex random}
func extractStartTimeFromXRayID(id *string) time.Time {
	if id == nil {
		return time.Time{}
	}
	parts := strings.SplitN(*id, "-", 3)
	if len(parts) != 3 || len(parts[1]) != 8 {
		return time.Time{}
	}
	var epoch int64
	_, err := fmt.Sscanf(parts[1], "%x", &epoch)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(epoch, 0)
}

// sortTraceSummariesAsc sorts trace summaries by start time ascending (in-place).
// X-Ray returns summaries in descending order by default.
func sortTraceSummariesAsc(summaries []xraytypes.TraceSummary) {
	for i, j := 0, len(summaries)-1; i < j; i, j = i+1, j-1 {
		summaries[i], summaries[j] = summaries[j], summaries[i]
	}
}

// xraySegment represents a parsed X-Ray segment document.
type xraySegment struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	TraceID     string                 `json:"trace_id"`
	StartTime   json.Number            `json:"start_time"`
	EndTime     json.Number            `json:"end_time"`
	ParentID    string                 `json:"parent_id"`
	Fault       bool                   `json:"fault"`
	Error       bool                   `json:"error"`
	Throttle    bool                   `json:"throttle"`
	Origin      string                 `json:"origin"`
	Type        string                 `json:"type"`
	Annotations map[string]interface{} `json:"annotations"`
	Metadata    map[string]interface{} `json:"metadata"`
	HTTP        map[string]interface{} `json:"http"`
	SQL         map[string]interface{} `json:"sql"`
	AWS         map[string]interface{} `json:"aws"`
	Subsegments []xraySegment          `json:"subsegments"`
	Namespace   string                 `json:"namespace"`
}

// flattenTrace extracts all segments and subsegments from an X-Ray trace into a flat list of SpanEntry.
func flattenTrace(trace xraytypes.Trace, includeAttributes bool) []SpanEntry {
	var spans []SpanEntry

	traceID := ""
	if trace.Id != nil {
		traceID = fromXRayTraceID(*trace.Id)
	}

	for _, segment := range trace.Segments {
		if segment.Document == nil {
			continue
		}

		var seg xraySegment
		if err := json.Unmarshal([]byte(*segment.Document), &seg); err != nil {
			continue
		}

		flattenSegment(&seg, "", traceID, includeAttributes, &spans)
	}

	return spans
}

// flattenSegment recursively flattens a segment and its subsegments.
func flattenSegment(seg *xraySegment, parentID string, traceID string, includeAttributes bool, spans *[]SpanEntry) {
	entry := SpanEntry{
		SpanID:       seg.ID,
		SpanName:     seg.Name,
		ParentSpanID: parentID,
		Status:       segmentStatus(seg),
		SpanKind:     segmentKind(seg),
	}

	if seg.ParentID != "" {
		entry.ParentSpanID = seg.ParentID
	}

	startNs := jsonNumberToNanos(seg.StartTime)
	endNs := jsonNumberToNanos(seg.EndTime)
	entry.StartTime = time.Unix(0, startNs)
	entry.EndTime = time.Unix(0, endNs)
	entry.DurationNs = endNs - startNs

	if includeAttributes {
		entry.Attributes = buildSpanAttributes(seg)
		entry.ResourceAttributes = buildResourceAttributes(seg)
	}

	*spans = append(*spans, entry)

	for i := range seg.Subsegments {
		flattenSegment(&seg.Subsegments[i], seg.ID, traceID, includeAttributes, spans)
	}
}

// segmentStatus derives span status from X-Ray segment fields.
func segmentStatus(seg *xraySegment) string {
	if seg.Fault || seg.Error || seg.Throttle {
		return "error"
	}
	if jsonNumberToNanos(seg.EndTime) > 0 {
		return "ok"
	}
	return "unset"
}

// segmentKind derives span kind from X-Ray segment origin or type.
func segmentKind(seg *xraySegment) string {
	if seg.Origin != "" {
		switch {
		case strings.Contains(seg.Origin, "EC2") || strings.Contains(seg.Origin, "ECS") || strings.Contains(seg.Origin, "EKS") || strings.Contains(seg.Origin, "ElasticBeanstalk"):
			return "SERVER"
		case strings.Contains(seg.Origin, "Lambda"):
			return "SERVER"
		}
	}
	if seg.Type == "subsegment" {
		if seg.Namespace == "remote" || seg.Namespace == "aws" {
			return "CLIENT"
		}
		return "INTERNAL"
	}
	return "SERVER"
}

// buildSpanAttributes collects annotations, http, sql, and other segment fields as span attributes.
func buildSpanAttributes(seg *xraySegment) map[string]interface{} {
	attrs := make(map[string]interface{})

	for k, v := range seg.Annotations {
		attrs["annotation."+k] = v
	}
	for k, v := range flattenMap("http", seg.HTTP) {
		attrs[k] = v
	}
	for k, v := range flattenMap("sql", seg.SQL) {
		attrs[k] = v
	}

	if seg.Namespace != "" {
		attrs["xray.namespace"] = seg.Namespace
	}
	if seg.Origin != "" {
		attrs["xray.origin"] = seg.Origin
	}

	return attrs
}

// buildResourceAttributes collects AWS-related and origin fields as resource attributes.
func buildResourceAttributes(seg *xraySegment) map[string]interface{} {
	attrs := make(map[string]interface{})

	for k, v := range flattenMap("aws", seg.AWS) {
		attrs[k] = v
	}
	if seg.Origin != "" {
		attrs["cloud.platform"] = seg.Origin
	}

	return attrs
}

// flattenMap flattens a nested map with a prefix for attribute keys.
func flattenMap(prefix string, m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		key := prefix + "." + k
		if nested, ok := v.(map[string]interface{}); ok {
			for nk, nv := range flattenMap(key, nested) {
				result[nk] = nv
			}
		} else {
			result[key] = v
		}
	}
	return result
}

// findSpanDetail locates a specific span by ID in a trace and returns its full detail.
func findSpanDetail(trace xraytypes.Trace, spanID string) (*SpanDetail, error) {
	traceID := ""
	if trace.Id != nil {
		traceID = fromXRayTraceID(*trace.Id)
	}

	for _, segment := range trace.Segments {
		if segment.Document == nil {
			continue
		}

		var seg xraySegment
		if err := json.Unmarshal([]byte(*segment.Document), &seg); err != nil {
			continue
		}

		if detail := findInSegment(&seg, "", traceID, spanID); detail != nil {
			return detail, nil
		}
	}

	return nil, fmt.Errorf("span not found: traceId=%s, spanId=%s", traceID, spanID)
}

// findInSegment recursively searches for a span by ID in a segment tree.
func findInSegment(seg *xraySegment, parentID string, traceID string, spanID string) *SpanDetail {
	effectiveParentID := parentID
	if seg.ParentID != "" {
		effectiveParentID = seg.ParentID
	}

	if seg.ID == spanID {
		return segmentToSpanDetail(seg, effectiveParentID)
	}

	for i := range seg.Subsegments {
		if detail := findInSegment(&seg.Subsegments[i], seg.ID, traceID, spanID); detail != nil {
			return detail
		}
	}

	return nil
}

// segmentToSpanDetail converts an X-Ray segment to a full SpanDetail with all attributes.
func segmentToSpanDetail(seg *xraySegment, parentID string) *SpanDetail {
	startNs := jsonNumberToNanos(seg.StartTime)
	endNs := jsonNumberToNanos(seg.EndTime)

	detail := &SpanDetail{
		SpanID:       seg.ID,
		SpanName:     seg.Name,
		SpanKind:     segmentKind(seg),
		StartTime:    time.Unix(0, startNs),
		EndTime:      time.Unix(0, endNs),
		DurationNs:   endNs - startNs,
		ParentSpanID: parentID,
		Status:       segmentStatus(seg),
	}

	attrs := make([]SpanAttribute, 0)
	for k, v := range seg.Annotations {
		attrs = append(attrs, SpanAttribute{Key: "annotation." + k, Value: fmt.Sprintf("%v", v)})
	}
	for k, v := range flattenMap("http", seg.HTTP) {
		attrs = append(attrs, SpanAttribute{Key: k, Value: fmt.Sprintf("%v", v)})
	}
	for k, v := range flattenMap("sql", seg.SQL) {
		attrs = append(attrs, SpanAttribute{Key: k, Value: fmt.Sprintf("%v", v)})
	}
	if seg.Namespace != "" {
		attrs = append(attrs, SpanAttribute{Key: "xray.namespace", Value: seg.Namespace})
	}
	if seg.Origin != "" {
		attrs = append(attrs, SpanAttribute{Key: "xray.origin", Value: seg.Origin})
	}
	detail.Attributes = attrs

	resAttrs := make([]SpanAttribute, 0)
	for k, v := range flattenMap("aws", seg.AWS) {
		resAttrs = append(resAttrs, SpanAttribute{Key: k, Value: fmt.Sprintf("%v", v)})
	}
	if seg.Origin != "" {
		resAttrs = append(resAttrs, SpanAttribute{Key: "cloud.platform", Value: seg.Origin})
	}

	if seg.Metadata != nil {
		for ns, v := range seg.Metadata {
			if nsMap, ok := v.(map[string]interface{}); ok {
				for k, mv := range nsMap {
					resAttrs = append(resAttrs, SpanAttribute{Key: fmt.Sprintf("metadata.%s.%s", ns, k), Value: fmt.Sprintf("%v", mv)})
				}
			}
		}
	}
	detail.ResourceAttributes = resAttrs

	return detail
}
