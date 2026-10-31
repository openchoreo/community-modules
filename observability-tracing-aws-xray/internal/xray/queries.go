// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}

	filterExprs := traceFilterExpressions(params.Scope)
	var allSummaries []xraytypes.TraceSummary
	for _, filterExpr := range filterExprs {
		summaries, err := c.getTraceSummaries(ctx, params, filterExpr, limit)
		if err != nil {
			return nil, err
		}
		allSummaries = appendNewTraceSummaries(allSummaries, summaries)
	}
	if len(filterExprs) > 1 {
		sortTraceSummariesDesc(allSummaries)
	}

	// Every line's merged queries can return up to twice MaxQueryLimit traces.
	total := min(len(allSummaries), MaxQueryLimit)
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

// getTraceSummaries pages through the trace summaries matching filterExpr until it has read limit
// of them or there are no more.
func (c *Client) getTraceSummaries(ctx context.Context, params TracesQueryParams, filterExpr string, limit int) ([]xraytypes.TraceSummary, error) {
	input := &xray.GetTraceSummariesInput{
		StartTime:     aws.Time(params.StartTime),
		EndTime:       aws.Time(params.EndTime),
		TimeRangeType: xraytypes.TimeRangeTypeTraceId,
	}
	if filterExpr != "" {
		input.FilterExpression = aws.String(filterExpr)
	}

	var summaries []xraytypes.TraceSummary
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

		summaries = append(summaries, output.TraceSummaries...)

		if output.NextToken == nil || len(summaries) >= limit {
			break
		}
		nextToken = output.NextToken
	}
	return summaries, nil
}

// appendNewTraceSummaries appends the summaries of traces not already in to.
func appendNewTraceSummaries(to, summaries []xraytypes.TraceSummary) []xraytypes.TraceSummary {
	seen := make(map[string]bool, len(to))
	for _, s := range to {
		seen[aws.ToString(s.Id)] = true
	}
	for _, s := range summaries {
		if id := aws.ToString(s.Id); !seen[id] {
			seen[id] = true
			to = append(to, s)
		}
	}
	return to
}

// GetSpans queries X-Ray for the spans (segments + subsegments) of a given trace
// that match the search scope and the time range. BatchGetTraces accepts only
// trace IDs and no filter expression, so every span is checked here, before
// the limit and the total are applied. A trace with no matching span returns an
// empty list, the same as a trace that does not exist.
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

	spans := flattenTrace(output.Traces[0], params, params.IncludeAttributes)

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

// ErrSpanNotFound is returned by GetSpanDetail when the trace or the span does
// not exist, or when the span carries no OpenChoreo identity.
var ErrSpanNotFound = errors.New("span not found")

// GetSpanDetail queries X-Ray for a specific span within a trace. The request
// carries no search scope, so the caller authorizes on the OpenChoreo resource
// attributes of the returned span. A span without an openchoreo.dev/namespace
// resource attribute is reported as ErrSpanNotFound, so only OpenChoreo spans
// are served from the X-Ray account.
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
		return nil, ErrSpanNotFound
	}

	detail := findSpanDetail(output.Traces[0], params.SpanID)
	if detail == nil {
		return nil, ErrSpanNotFound
	}
	if ns, _ := detail.ResourceAttributes[resourceNamespace].(string); ns == "" {
		return nil, ErrSpanNotFound
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

// OpenChoreo span attributes indexed as X-Ray annotations by the awsxray
// exporter's indexed_attributes. X-Ray annotation keys preserve dots and
// convert slashes/hyphens to underscores, so these are the keys that appear
// both in filter expressions and on a segment document's "annotations" object.
const (
	annotationNamespace      = "openchoreo.dev_namespace"
	annotationProjectUID     = "openchoreo.dev_project_uid"
	annotationComponentUID   = "openchoreo.dev_component_uid"
	annotationEnvironmentUID = "openchoreo.dev_environment_uid"
	annotationReleaseLineUID = "openchoreo.dev_release_line_uid"
	// annotationReleaseLineComponentUID is the component UID on a release line's spans, which
	// don't carry annotationComponentUID.
	annotationReleaseLineComponentUID = "openchoreo.dev_release_line_component_uid"
)

// OpenChoreo resource attribute keys, as the collector's k8sattributes
// processor sets them from the workload pod labels.
const (
	resourceNamespace               = "openchoreo.dev/namespace"
	resourceProjectUID              = "openchoreo.dev/project-uid"
	resourceComponentUID            = "openchoreo.dev/component-uid"
	resourceEnvironmentUID          = "openchoreo.dev/environment-uid"
	resourceReleaseLineUID          = "openchoreo.dev/release-line-uid"
	resourceReleaseLineComponentUID = "openchoreo.dev/release-line-component-uid"
)

// identityKeys pairs each indexed annotation with the resource attribute the
// collector copied it from. Together they are the OpenChoreo identity of a span.
// A release line's spans carry the release line keys in place of the component UID.
var identityKeys = []struct {
	annotation string
	resource   string
}{
	{annotationNamespace, resourceNamespace},
	{annotationProjectUID, resourceProjectUID},
	{annotationComponentUID, resourceComponentUID},
	{annotationEnvironmentUID, resourceEnvironmentUID},
	{annotationReleaseLineUID, resourceReleaseLineUID},
	{annotationReleaseLineComponentUID, resourceReleaseLineComponentUID},
}

// otelResourceMetadataPrefix is the prefix the awsxray exporter gives the
// resource attributes it stores under a segment's metadata.default.
const otelResourceMetadataPrefix = "otel.resource."

// buildFilterExpression constructs an X-Ray filter expression from the search scope.
// Keys containing dots must use annotation[key] filter syntax.
func buildFilterExpression(scope Scope) string {
	return filterExpression(scope, annotationComponentUID)
}

// filterExpression builds the filter expression of the scope, matching its component UID on
// componentAnnotation. A release line matches on its UID alone, in place of the component.
func filterExpression(scope Scope, componentAnnotation string) string {
	var parts []string

	if scope.Namespace != "" {
		parts = append(parts, fmt.Sprintf("annotation[%s] = %q", annotationNamespace, scope.Namespace))
	}
	if scope.ProjectID != "" {
		parts = append(parts, fmt.Sprintf("annotation[%s] = %q", annotationProjectUID, scope.ProjectID))
	}
	if scope.ReleaseLineID != "" {
		parts = append(parts, fmt.Sprintf("annotation[%s] = %q", annotationReleaseLineUID, scope.ReleaseLineID))
	} else if scope.ComponentID != "" {
		parts = append(parts, fmt.Sprintf("annotation[%s] = %q", componentAnnotation, scope.ComponentID))
	}
	if scope.EnvironmentID != "" {
		parts = append(parts, fmt.Sprintf("annotation[%s] = %q", annotationEnvironmentUID, scope.EnvironmentID))
	}

	return strings.Join(parts, " AND ")
}

// traceFilterExpressions returns the filter expressions whose traces together match the scope. A
// filter expression tests a whole trace, which may hold spans of other release lines, so the main
// line matches on the component UID annotation alone, which only its spans carry. Every line adds
// an expression on the release line component UID annotation, since filter expressions document no
// grouping to OR the two in one.
func traceFilterExpressions(scope Scope) []string {
	exprs := []string{buildFilterExpression(scope)}
	if scope.ReleaseLineID == "" && scope.ComponentID != "" && scope.AllReleaseLines {
		exprs = append(exprs, filterExpression(scope, annotationReleaseLineComponentUID))
	}
	return exprs
}

// matchesScope reports whether a span's own annotations satisfy the search
// scope. The namespace is always required, so an unscoped request matches
// nothing. The project, component and environment UIDs constrain the match
// only when they are set on the scope.
func matchesScope(annotations map[string]interface{}, scope Scope) bool {
	if scope.Namespace == "" {
		return false
	}
	return annotationEquals(annotations, annotationNamespace, scope.Namespace) &&
		annotationEquals(annotations, annotationProjectUID, scope.ProjectID) &&
		matchesReleaseLine(annotations, scope) &&
		annotationEquals(annotations, annotationEnvironmentUID, scope.EnvironmentID)
}

// matchesReleaseLine reports whether a span is on the scope's lines of its component. The
// component UID alone keeps its main line, whose spans carry no release line UID. A release line's
// spans carry the component UID under another annotation than the main line's, so AllReleaseLines
// matches either and covers every line. One release line matches on its UID alone. Without a
// component or a line every span matches.
func matchesReleaseLine(annotations map[string]interface{}, scope Scope) bool {
	switch {
	case scope.ReleaseLineID != "":
		return annotationEquals(annotations, annotationReleaseLineUID, scope.ReleaseLineID)
	case scope.ComponentID == "":
		return true
	case scope.AllReleaseLines:
		return annotationEquals(annotations, annotationComponentUID, scope.ComponentID) ||
			annotationEquals(annotations, annotationReleaseLineComponentUID, scope.ComponentID)
	}
	_, onReleaseLine := annotations[annotationReleaseLineUID]
	return annotationEquals(annotations, annotationComponentUID, scope.ComponentID) && !onReleaseLine
}

// annotationEquals reports whether the annotation matches want. An empty want
// is treated as "not scoped" and always matches. A missing or non-string
// annotation value never matches a scoped field.
func annotationEquals(annotations map[string]interface{}, key, want string) bool {
	if want == "" {
		return true
	}
	got, ok := annotations[key].(string)
	return ok && got == want
}

// spanIdentity reverse-maps a span's OpenChoreo annotations to resource
// attribute keys (openchoreo.dev_namespace to openchoreo.dev/namespace,
// openchoreo.dev_project_uid to openchoreo.dev/project-uid, and so on).
// Missing, empty and non-string values are left out.
func spanIdentity(annotations map[string]interface{}) map[string]string {
	identity := make(map[string]string, len(identityKeys))
	for _, k := range identityKeys {
		if v, ok := annotations[k.annotation].(string); ok && v != "" {
			identity[k.resource] = v
		}
	}
	return identity
}

// completeIdentity reports whether an identity carries the namespace, the
// project and environment UIDs and the UIDs of its line: the component UID on
// the main line, or the release line and its component's UIDs on a release line.
// Only a complete identity pins a span to a single component line in a single
// environment, so only a complete identity can have names attributed to it.
func completeIdentity(id map[string]string) bool {
	for _, k := range []string{resourceNamespace, resourceProjectUID, resourceEnvironmentUID} {
		if id[k] == "" {
			return false
		}
	}
	return id[resourceComponentUID] != "" ||
		(id[resourceReleaseLineUID] != "" && id[resourceReleaseLineComponentUID] != "")
}

// sameIdentity reports whether two complete identities agree on the namespace
// and all their UIDs. An incomplete identity never matches.
func sameIdentity(a, b map[string]string) bool {
	if !completeIdentity(a) || !completeIdentity(b) {
		return false
	}
	for _, k := range identityKeys {
		if a[k.resource] != b[k.resource] {
			return false
		}
	}
	return true
}

// segmentResource is the OTel resource of a top-level segment document. The
// awsxray exporter stores resource attributes only on segments, never on
// subsegments, so the subsegments nested in a segment can take their resource
// names from here, but only when they share the segment's identity.
type segmentResource struct {
	// identity is the segment's own OpenChoreo identity from its annotations,
	// the same values the span list filters on.
	identity map[string]string
	// attrs holds the resource attributes lifted from the segment metadata
	// with bare keys, or nil when they cannot be attributed to identity.
	attrs map[string]interface{}
}

// newSegmentResource lifts the resource attributes the exporter wrote into
// metadata.default."otel.resource.<key>" of a top-level segment. The lifted
// attributes are kept only when the segment's annotations carry a complete
// identity and the lifted metadata carries the same complete identity, so
// resource names are never attached to an identity they were not exported with.
func newSegmentResource(seg *xraySegment) segmentResource {
	res := segmentResource{identity: spanIdentity(seg.Annotations)}
	if !completeIdentity(res.identity) {
		return res
	}

	defaults, _ := seg.Metadata["default"].(map[string]interface{})
	lifted := make(map[string]interface{})
	for k, v := range defaults {
		if key, ok := strings.CutPrefix(k, otelResourceMetadataPrefix); ok && key != "" {
			lifted[key] = v
		}
	}

	for _, k := range identityKeys {
		if v, _ := lifted[k.resource].(string); v != res.identity[k.resource] {
			return res
		}
	}

	res.attrs = lifted
	return res
}

// spanResourceAttributes builds the resource attributes of a span. Every span
// carries its own OpenChoreo identity from its annotations. The enclosing
// segment's lifted resource (the OpenChoreo names, k8s.* and service.*
// attributes) is added only when the span's complete identity equals the
// segment's: a subsegment can belong to another service than the segment X-Ray
// nests it under, and must not inherit that service's names. Names that cannot
// be established stay absent, which fails closed for scoped callers.
func spanResourceAttributes(seg *xraySegment, enclosing segmentResource) map[string]interface{} {
	attrs := make(map[string]interface{})

	identity := spanIdentity(seg.Annotations)
	if enclosing.attrs != nil && sameIdentity(identity, enclosing.identity) {
		for k, v := range enclosing.attrs {
			attrs[k] = v
		}
	}
	for k, v := range buildResourceAttributes(seg) {
		attrs[k] = v
	}
	for k, v := range identity {
		attrs[k] = v
	}

	return attrs
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

// sortTraceSummariesDesc sorts trace summaries by start time descending (in-place), the order
// X-Ray returns them in.
func sortTraceSummariesDesc(summaries []xraytypes.TraceSummary) {
	sort.SliceStable(summaries, func(i, j int) bool {
		return traceSummaryStartTime(summaries[i]).After(traceSummaryStartTime(summaries[j]))
	})
}

// traceSummaryStartTime returns the start time of a trace summary, or of its trace ID without one.
func traceSummaryStartTime(summary xraytypes.TraceSummary) time.Time {
	if summary.StartTime != nil {
		return *summary.StartTime
	}
	return extractStartTimeFromXRayID(summary.Id)
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
	// Cause is either a cause object or a plain string holding the ID of the
	// segment that carries the cause, so it is kept raw and decoded lazily.
	Cause json.RawMessage `json:"cause"`
}

// xrayCause is the object form of an X-Ray segment `cause` field. The AWS X-Ray
// exporter in the OpenTelemetry Collector writes the OTel span status message
// (or the exception recorded on the span) into the first exception entry.
type xrayCause struct {
	Exceptions []struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"exceptions"`
}

// flattenTrace extracts the segments and subsegments of an X-Ray trace that
// match the scope and time range of params into a flat list of SpanEntry. Each
// span is judged on its own annotations and timestamps, independent of
// includeAttributes and of the segment it is nested in.
func flattenTrace(trace xraytypes.Trace, params TracesQueryParams, includeAttributes bool) []SpanEntry {
	spans := []SpanEntry{}

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

		flattenSegment(&seg, "", traceID, params, includeAttributes, newSegmentResource(&seg), false, &spans)
	}

	return spans
}

// flattenSegment recursively flattens a segment and its subsegments, keeping
// only the spans that match the scope and time range of params. The
// subsegments of a span that does not match are still visited, since each is
// judged on its own annotations and timestamps.
// enclosing is the resource of the top-level segment document the span is in.
// isSubsegment is false only for the top-level segment documents of a trace.
func flattenSegment(seg *xraySegment, parentID string, traceID string, params TracesQueryParams, includeAttributes bool, enclosing segmentResource, isSubsegment bool, spans *[]SpanEntry) {
	if matchesScope(seg.Annotations, params.Scope) && withinTimeRange(seg, params.StartTime, params.EndTime) {
		*spans = append(*spans, segmentToSpanEntry(seg, parentID, includeAttributes, enclosing, isSubsegment))
	}

	for i := range seg.Subsegments {
		flattenSegment(&seg.Subsegments[i], seg.ID, traceID, params, includeAttributes, enclosing, true, spans)
	}
}

// withinTimeRange reports whether a span starts at or after start and ends at
// or before end, the same bounds the OpenSearch adapter applies to span
// documents. A zero bound is treated as unbounded. An in-progress segment or
// subsegment has no end_time, so it cannot be shown to end before an upper
// bound and is excluded whenever one is set.
func withinTimeRange(seg *xraySegment, start, end time.Time) bool {
	if !start.IsZero() && jsonNumberToNanos(seg.StartTime) < start.UnixNano() {
		return false
	}
	if !end.IsZero() {
		endNs := jsonNumberToNanos(seg.EndTime)
		if endNs == 0 || endNs > end.UnixNano() {
			return false
		}
	}
	return true
}

// segmentToSpanEntry converts an X-Ray segment or subsegment to a SpanEntry.
func segmentToSpanEntry(seg *xraySegment, parentID string, includeAttributes bool, enclosing segmentResource, isSubsegment bool) SpanEntry {
	entry := SpanEntry{
		SpanID:        seg.ID,
		SpanName:      seg.Name,
		ParentSpanID:  parentID,
		Status:        segmentStatus(seg),
		StatusMessage: segmentStatusMessage(seg),
		SpanKind:      segmentKind(seg, isSubsegment),
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
		entry.ResourceAttributes = spanResourceAttributes(seg, enclosing)
	}

	return entry
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

// segmentStatusMessage derives the human-readable status description from the
// X-Ray segment `cause`. The AWS X-Ray exporter stores the OTel span status
// message there as the first exception message, so that message is preferred and
// the exception type is used only when no message was recorded. Returns "" when
// the segment carries no cause object, which is the case for successful spans
// and for errors that were flagged without a description.
func segmentStatusMessage(seg *xraySegment) string {
	if len(seg.Cause) == 0 {
		return ""
	}

	var cause xrayCause
	if err := json.Unmarshal(seg.Cause, &cause); err != nil {
		// `cause` is a string referencing another segment's cause; no message here.
		return ""
	}

	for _, ex := range cause.Exceptions {
		if ex.Message != "" {
			return ex.Message
		}
		if ex.Type != "" {
			return ex.Type
		}
	}

	return ""
}

// segmentKind derives span kind from X-Ray segment origin or type.
// isSubsegment tells the caller's structural position apart from the document's
// own `type` field: subsegments nested inside a parent segment document — which
// is what BatchGetTraces returns — carry no `type`, so relying on that field
// alone would classify every span as SERVER.
func segmentKind(seg *xraySegment, isSubsegment bool) string {
	if seg.Origin != "" {
		switch {
		case strings.Contains(seg.Origin, "EC2") || strings.Contains(seg.Origin, "ECS") || strings.Contains(seg.Origin, "EKS") || strings.Contains(seg.Origin, "ElasticBeanstalk"):
			return "SERVER"
		case strings.Contains(seg.Origin, "Lambda"):
			return "SERVER"
		}
	}
	if isSubsegment || seg.Type == "subsegment" {
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

// findSpanDetail locates a specific span by ID in a trace and returns its full
// detail, or nil when the trace holds no span with that ID.
func findSpanDetail(trace xraytypes.Trace, spanID string) *SpanDetail {
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

		if detail := findInSegment(&seg, "", traceID, spanID, newSegmentResource(&seg), false); detail != nil {
			return detail
		}
	}

	return nil
}

// findInSegment recursively searches for a span by ID in a segment tree.
// enclosing is the resource of the top-level segment document being searched.
// isSubsegment is false only for the top-level segment documents of a trace.
func findInSegment(seg *xraySegment, parentID string, traceID string, spanID string, enclosing segmentResource, isSubsegment bool) *SpanDetail {
	effectiveParentID := parentID
	if seg.ParentID != "" {
		effectiveParentID = seg.ParentID
	}

	if seg.ID == spanID {
		return segmentToSpanDetail(seg, effectiveParentID, enclosing, isSubsegment)
	}

	for i := range seg.Subsegments {
		if detail := findInSegment(&seg.Subsegments[i], seg.ID, traceID, spanID, enclosing, true); detail != nil {
			return detail
		}
	}

	return nil
}

// segmentToSpanDetail converts an X-Ray segment to a full SpanDetail with all attributes.
// enclosing is the resource of the top-level segment document the span is in.
func segmentToSpanDetail(seg *xraySegment, parentID string, enclosing segmentResource, isSubsegment bool) *SpanDetail {
	startNs := jsonNumberToNanos(seg.StartTime)
	endNs := jsonNumberToNanos(seg.EndTime)

	detail := &SpanDetail{
		SpanID:        seg.ID,
		SpanName:      seg.Name,
		SpanKind:      segmentKind(seg, isSubsegment),
		StartTime:     time.Unix(0, startNs),
		EndTime:       time.Unix(0, endNs),
		DurationNs:    endNs - startNs,
		ParentSpanID:  parentID,
		Status:        segmentStatus(seg),
		StatusMessage: segmentStatusMessage(seg),
	}

	detail.Attributes = buildSpanAttributes(seg)

	// Span details carry the segment metadata in addition to the resource
	// attributes returned by the spans-list endpoint.
	resAttrs := spanResourceAttributes(seg, enclosing)
	for k, v := range flattenMap("metadata", seg.Metadata) {
		resAttrs[k] = v
	}
	detail.ResourceAttributes = resAttrs

	return detail
}
