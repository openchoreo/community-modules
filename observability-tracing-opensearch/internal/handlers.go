// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-opensearch/internal/opensearch"
)

const (
	maxLimit    = 10000
	tracesIndex = "otel-traces-*"
)

// TracingHandler implements the generated StrictServerInterface.
type TracingHandler struct {
	client *opensearch.Client
	logger *slog.Logger
}

func NewTracingHandler(client *opensearch.Client, logger *slog.Logger) *TracingHandler {
	return &TracingHandler{
		client: client,
		logger: logger,
	}
}

// Ensure TracingHandler implements the interface at compile time.
var _ gen.StrictServerInterface = (*TracingHandler)(nil)

// Health implements the health check endpoint.
func (h *TracingHandler) Health(ctx context.Context, _ gen.HealthRequestObject) (gen.HealthResponseObject, error) {
	status := "healthy"
	return gen.Health200JSONResponse{Status: &status}, nil
}

// QueryTraces implements POST /api/v1alpha1/traces/query.
func (h *TracingHandler) QueryTraces(ctx context.Context, request gen.QueryTracesRequestObject) (gen.QueryTracesResponseObject, error) {
	if request.Body == nil {
		return gen.QueryTraces400JSONResponse{
			Title:  ptr(gen.BadRequest),
			Detail: ptr("request body is required"),
		}, nil
	}
	if strings.TrimSpace(request.Body.SearchScope.Namespace) == "" {
		return gen.QueryTraces400JSONResponse{
			Title:  ptr(gen.BadRequest),
			Detail: ptr("namespace is required"),
		}, nil
	}
	if request.Body.EndTime.Before(request.Body.StartTime) {
		return gen.QueryTraces400JSONResponse{
			Title:  ptr(gen.BadRequest),
			Detail: ptr("endTime must be >= startTime"),
		}, nil
	}

	params := toTracesRequestParams(request.Body)

	return h.queryTracesWithAggregation(ctx, params)
}

// QuerySpansForTrace implements POST /api/v1alpha1/traces/{traceId}/spans/query.
func (h *TracingHandler) QuerySpansForTrace(ctx context.Context, request gen.QuerySpansForTraceRequestObject) (gen.QuerySpansForTraceResponseObject, error) {
	if request.Body == nil {
		return gen.QuerySpansForTrace400JSONResponse{
			Title:  ptr(gen.BadRequest),
			Detail: ptr("request body is required"),
		}, nil
	}
	if strings.TrimSpace(request.Body.SearchScope.Namespace) == "" {
		return gen.QuerySpansForTrace400JSONResponse{
			Title:  ptr(gen.BadRequest),
			Detail: ptr("namespace is required"),
		}, nil
	}
	if request.Body.EndTime.Before(request.Body.StartTime) {
		return gen.QuerySpansForTrace400JSONResponse{
			Title:  ptr(gen.BadRequest),
			Detail: ptr("endTime must be >= startTime"),
		}, nil
	}

	params := toTracesRequestParams(request.Body)
	params.TraceID = request.TraceId

	query := opensearch.BuildTracesQuery(params)
	response, err := h.client.Search(ctx, []string{tracesIndex}, query)
	if err != nil {
		h.logger.Error("Failed to query spans", slog.Any("error", err))
		return gen.QuerySpansForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: ptr("internal server error"),
		}, nil
	}

	// Parse spans from hits
	spans := make([]opensearch.Span, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		span := opensearch.ParseSpanEntry(hit)
		spans = append(spans, span)
	}

	total := response.Hits.Total.Value
	tookMs := response.Took
	return gen.QuerySpansForTrace200JSONResponse(toSpansListResponse(spans, total, tookMs, params.IncludeAttributes)), nil
}

// GetSpanDetailsForTrace implements GET /api/v1alpha1/traces/{traceId}/spans/{spanId}.
func (h *TracingHandler) GetSpanDetailsForTrace(ctx context.Context, request gen.GetSpanDetailsForTraceRequestObject) (gen.GetSpanDetailsForTraceResponseObject, error) {
	query := opensearch.BuildSpanDetailsQuery(request.TraceId, request.SpanId)
	response, err := h.client.Search(ctx, []string{tracesIndex}, query)
	if err != nil {
		h.logger.Error("Failed to query span detail", slog.Any("error", err))
		return gen.GetSpanDetailsForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: ptr("internal server error"),
		}, nil
	}

	if len(response.Hits.Hits) == 0 {
		detail := "span not found"
		return gen.GetSpanDetailsForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: &detail,
		}, nil
	}

	span := opensearch.ParseSpanEntry(response.Hits.Hits[0])
	return gen.GetSpanDetailsForTrace200JSONResponse(toSpanDetailsResponse(&span)), nil
}

// queryTracesWithAggregation uses a terms aggregation on traceId to return distinct traces.
func (h *TracingHandler) queryTracesWithAggregation(ctx context.Context, params opensearch.TracesRequestParams) (gen.QueryTracesResponseObject, error) {
	query := opensearch.BuildTracesAggregationQuery(params)
	response, err := h.client.SearchRaw(ctx, []string{tracesIndex}, query)
	if err != nil {
		h.logger.Error("Failed to execute traces aggregation search", slog.Any("error", err))
		return gen.QueryTraces500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: ptr("internal server error"),
		}, nil
	}

	aggResult, err := parseTracesAggregation(response.Aggregations)
	if err != nil {
		h.logger.Error("Failed to parse traces aggregation", slog.Any("error", err))
		return gen.QueryTraces500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: ptr("internal server error"),
		}, nil
	}

	traces := make([]traceEntry, 0, len(aggResult.Traces.Buckets))
	for _, bucket := range aggResult.Traces.Buckets {
		trace := buildTraceFromBucket(bucket)
		traces = append(traces, trace)
	}

	totalCount := aggResult.TraceCount.Value
	if totalCount > maxLimit {
		totalCount = maxLimit
	}

	return gen.QueryTraces200JSONResponse(toTracesListResponse(traces, totalCount, response.Took)), nil
}

// traceEntry is an internal representation of a trace for response building.
type traceEntry struct {
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

// parseTracesAggregation parses the raw aggregation JSON from the OpenSearch response.
func parseTracesAggregation(raw json.RawMessage) (*opensearch.TracesAggregationResult, error) {
	if len(raw) == 0 {
		return &opensearch.TracesAggregationResult{}, nil
	}
	var result opensearch.TracesAggregationResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal aggregation result: %w", err)
	}
	return &result, nil
}

// buildTraceFromBucket converts an aggregation bucket into a traceEntry.
func buildTraceFromBucket(bucket opensearch.TraceBucket) traceEntry {
	trace := traceEntry{
		TraceID:   bucket.Key,
		SpanCount: bucket.DocCount,
	}

	// Extract startTime from the earliest span (top_hits sorted by startTime asc)
	if len(bucket.EarliestSpan.Hits.Hits) > 0 {
		src := bucket.EarliestSpan.Hits.Hits[0].Source
		if src != nil {
			if ts, ok := src["startTime"].(string); ok {
				if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
					trace.StartTime = parsed
				}
			}
		}
	}

	// Extract root span info from the root_span filter aggregation (parentSpanId == "")
	// Falls back to the earliest span if no root span is found
	if len(bucket.RootSpan.Hit.Hits.Hits) > 0 {
		src := bucket.RootSpan.Hit.Hits.Hits[0].Source
		if src != nil {
			if spanID, ok := src["spanId"].(string); ok {
				trace.RootSpanID = spanID
			}
			if name, ok := src["name"].(string); ok {
				trace.RootSpanName = name
				trace.TraceName = name
			}
			if spanKind, ok := src["kind"].(string); ok {
				trace.RootSpanKind = spanKind
			}
		}
	} else if len(bucket.EarliestSpan.Hits.Hits) > 0 {
		src := bucket.EarliestSpan.Hits.Hits[0].Source
		if src != nil {
			if spanID, ok := src["spanId"].(string); ok {
				trace.RootSpanID = spanID
			}
			if name, ok := src["name"].(string); ok {
				trace.RootSpanName = name
				trace.TraceName = name
			}
			if spanKind, ok := src["kind"].(string); ok {
				trace.RootSpanKind = spanKind
			}
		}
	}

	// Extract endTime from the latest span (top_hits sorted by endTime desc)
	if len(bucket.LatestSpan.Hits.Hits) > 0 {
		src := bucket.LatestSpan.Hits.Hits[0].Source
		if src != nil {
			if ts, ok := src["endTime"].(string); ok {
				if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
					trace.EndTime = parsed
				}
			}
		}
	}

	// Calculate duration
	if !trace.StartTime.IsZero() && !trace.EndTime.IsZero() {
		trace.DurationNs = trace.EndTime.Sub(trace.StartTime).Nanoseconds()
	}

	// Determine trace status from error_span sub-aggregation
	trace.HasErrors = bucket.ErrorSpanCount.DocCount > 0

	return trace
}

// toTracesRequestParams converts the generated request body to internal query params.
func toTracesRequestParams(req *gen.TracesQueryRequest) opensearch.TracesRequestParams {
	params := opensearch.TracesRequestParams{
		StartTime: req.StartTime.Format(time.RFC3339Nano),
		EndTime:   req.EndTime.Format(time.RFC3339Nano),
	}
	if req.Limit != nil {
		params.Limit = *req.Limit
	}
	if params.Limit == 0 {
		params.Limit = 20
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
	}
	if params.SortOrder == "" {
		params.SortOrder = "desc"
	}
	if req.SearchScope.Project != nil {
		params.ProjectUID = *req.SearchScope.Project
	}
	if req.SearchScope.Component != nil {
		params.ComponentUIDs = []string{*req.SearchScope.Component}
	}
	if req.SearchScope.Environment != nil {
		params.EnvironmentUID = *req.SearchScope.Environment
	}
	if req.IncludeAttributes != nil {
		params.IncludeAttributes = *req.IncludeAttributes
	}
	params.Namespace = req.SearchScope.Namespace
	return params
}

// toTracesListResponse converts internal trace entries to the generated response model.
func toTracesListResponse(traces []traceEntry, total int, tookMs int) gen.TracesListResponse {
	apiTraces := make([]struct {
		DurationNs   *int64     `json:"durationNs,omitempty"`
		EndTime      *time.Time `json:"endTime,omitempty"`
		HasErrors    *bool      `json:"hasErrors,omitempty"`
		RootSpanId   *string    `json:"rootSpanId,omitempty"`
		RootSpanKind *string    `json:"rootSpanKind,omitempty"`
		RootSpanName *string    `json:"rootSpanName,omitempty"`
		SpanCount    *int       `json:"spanCount,omitempty"`
		StartTime    *time.Time `json:"startTime,omitempty"`
		TraceId      *string    `json:"traceId,omitempty"`
		TraceName    *string    `json:"traceName,omitempty"`
	}, 0, len(traces))

	for _, t := range traces {
		dur := t.DurationNs
		startTime := t.StartTime
		endTime := t.EndTime
		traceId := t.TraceID
		traceName := t.TraceName
		spanCount := t.SpanCount
		rootSpanId := t.RootSpanID
		rootSpanName := t.RootSpanName
		rootSpanKind := t.RootSpanKind
		hasErrors := t.HasErrors
		apiTraces = append(apiTraces, struct {
			DurationNs   *int64     `json:"durationNs,omitempty"`
			EndTime      *time.Time `json:"endTime,omitempty"`
			HasErrors    *bool      `json:"hasErrors,omitempty"`
			RootSpanId   *string    `json:"rootSpanId,omitempty"`
			RootSpanKind *string    `json:"rootSpanKind,omitempty"`
			RootSpanName *string    `json:"rootSpanName,omitempty"`
			SpanCount    *int       `json:"spanCount,omitempty"`
			StartTime    *time.Time `json:"startTime,omitempty"`
			TraceId      *string    `json:"traceId,omitempty"`
			TraceName    *string    `json:"traceName,omitempty"`
		}{
			DurationNs:   &dur,
			StartTime:    &startTime,
			EndTime:      &endTime,
			TraceId:      &traceId,
			TraceName:    &traceName,
			SpanCount:    &spanCount,
			RootSpanId:   &rootSpanId,
			RootSpanName: &rootSpanName,
			RootSpanKind: &rootSpanKind,
			HasErrors:    &hasErrors,
		})
	}

	return gen.TracesListResponse{
		Traces: &apiTraces,
		Total:  &total,
		TookMs: &tookMs,
	}
}

// toSpansListResponse converts internal spans to the generated response model.
func toSpansListResponse(spans []opensearch.Span, total int, tookMs int, includeAttributes bool) gen.TraceSpansListResponse {
	apiSpans := make([]struct {
		Attributes         *map[string]interface{}                `json:"attributes,omitempty"`
		DurationNs         *int64                                 `json:"durationNs,omitempty"`
		EndTime            *time.Time                             `json:"endTime,omitempty"`
		ParentSpanId       *string                                `json:"parentSpanId,omitempty"`
		ResourceAttributes *map[string]interface{}                `json:"resourceAttributes,omitempty"`
		SpanId             *string                                `json:"spanId,omitempty"`
		SpanKind           *string                                `json:"spanKind,omitempty"`
		SpanName           *string                                `json:"spanName,omitempty"`
		StartTime          *time.Time                             `json:"startTime,omitempty"`
		Status             *gen.TraceSpansListResponseSpansStatus `json:"status,omitempty"`
	}, 0, len(spans))

	for _, s := range spans {
		dur := s.DurationNanoseconds
		startTime := s.StartTime
		endTime := s.EndTime
		spanId := s.SpanID
		spanName := s.Name
		spanKind := s.SpanKind
		parentSpanId := s.ParentSpanID
		status := gen.TraceSpansListResponseSpansStatus(s.Status)
		entry := struct {
			Attributes         *map[string]interface{}                `json:"attributes,omitempty"`
			DurationNs         *int64                                 `json:"durationNs,omitempty"`
			EndTime            *time.Time                             `json:"endTime,omitempty"`
			ParentSpanId       *string                                `json:"parentSpanId,omitempty"`
			ResourceAttributes *map[string]interface{}                `json:"resourceAttributes,omitempty"`
			SpanId             *string                                `json:"spanId,omitempty"`
			SpanKind           *string                                `json:"spanKind,omitempty"`
			SpanName           *string                                `json:"spanName,omitempty"`
			StartTime          *time.Time                             `json:"startTime,omitempty"`
			Status             *gen.TraceSpansListResponseSpansStatus `json:"status,omitempty"`
		}{
			DurationNs:   &dur,
			StartTime:    &startTime,
			EndTime:      &endTime,
			SpanId:       &spanId,
			SpanName:     &spanName,
			SpanKind:     &spanKind,
			ParentSpanId: &parentSpanId,
			Status:       &status,
		}
		if includeAttributes {
			if s.Attributes != nil {
				entry.Attributes = &s.Attributes
			}
			if s.ResourceAttributes != nil {
				entry.ResourceAttributes = &s.ResourceAttributes
			}
		}
		apiSpans = append(apiSpans, entry)
	}

	return gen.TraceSpansListResponse{
		Spans:  &apiSpans,
		Total:  &total,
		TookMs: &tookMs,
	}
}

// toSpanDetailsResponse converts an internal span to the generated response model.
func toSpanDetailsResponse(span *opensearch.Span) gen.TraceSpanDetailsResponse {
	dur := span.DurationNanoseconds
	startTime := span.StartTime
	endTime := span.EndTime

	attrs := make([]struct {
		Key   *string `json:"key,omitempty"`
		Value *string `json:"value,omitempty"`
	}, 0, len(span.Attributes))
	for k, v := range span.Attributes {
		key := k
		value := fmt.Sprintf("%v", v)
		attrs = append(attrs, struct {
			Key   *string `json:"key,omitempty"`
			Value *string `json:"value,omitempty"`
		}{Key: &key, Value: &value})
	}

	resAttrs := make([]struct {
		Key   *string `json:"key,omitempty"`
		Value *string `json:"value,omitempty"`
	}, 0, len(span.ResourceAttributes))
	for k, v := range span.ResourceAttributes {
		key := k
		value := fmt.Sprintf("%v", v)
		resAttrs = append(resAttrs, struct {
			Key   *string `json:"key,omitempty"`
			Value *string `json:"value,omitempty"`
		}{Key: &key, Value: &value})
	}

	return gen.TraceSpanDetailsResponse{
		SpanId:             &span.SpanID,
		SpanName:           &span.Name,
		SpanKind:           &span.SpanKind,
		StartTime:          &startTime,
		EndTime:            &endTime,
		DurationNs:         &dur,
		ParentSpanId:       &span.ParentSpanID,
		Status:             ptr(gen.TraceSpanDetailsResponseStatus(span.Status)),
		Attributes:         &attrs,
		ResourceAttributes: &resAttrs,
	}
}

func ptr[T any](v T) *T {
	return &v
}
