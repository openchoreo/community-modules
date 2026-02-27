// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-openobserve/internal/openobserve"
)

// TracingHandler implements the generated StrictServerInterface.
type TracingHandler struct {
	client *openobserve.Client
	logger *slog.Logger
}

func NewTracingHandler(client *openobserve.Client, logger *slog.Logger) *TracingHandler {
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
	params := toTracesQueryParams(request.Body)

	result, err := h.client.GetTraces(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query traces", slog.Any("error", err))
		detail := err.Error()
		return gen.QueryTraces500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: &detail,
		}, nil
	}

	return gen.QueryTraces200JSONResponse(toTracesListResponse(result)), nil
}

// QuerySpansForTrace implements POST /api/v1alpha1/traces/{traceId}/spans/query.
func (h *TracingHandler) QuerySpansForTrace(ctx context.Context, request gen.QuerySpansForTraceRequestObject) (gen.QuerySpansForTraceResponseObject, error) {
	params := toTracesQueryParams(request.Body)
	params.TraceID = request.TraceId

	result, err := h.client.GetSpans(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query spans", slog.Any("error", err))
		detail := err.Error()
		return gen.QuerySpansForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: &detail,
		}, nil
	}

	return gen.QuerySpansForTrace200JSONResponse(toSpansListResponse(result)), nil
}

// GetSpanDetailsForTrace implements GET /api/v1alpha1/traces/{traceId}/spans/{spanId}.
func (h *TracingHandler) GetSpanDetailsForTrace(ctx context.Context, request gen.GetSpanDetailsForTraceRequestObject) (gen.GetSpanDetailsForTraceResponseObject, error) {
	params := openobserve.TracesQueryParams{
		TraceID: request.TraceId,
		SpanID:  request.SpanId,
	}

	result, err := h.client.GetSpanDetail(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query span detail", slog.Any("error", err))
		detail := err.Error()
		return gen.GetSpanDetailsForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: &detail,
		}, nil
	}

	return gen.GetSpanDetailsForTrace200JSONResponse(toSpanDetailsResponse(&result.Span)), nil
}

// toTracesQueryParams converts the generated request body to internal query params.
func toTracesQueryParams(req *gen.TracesQueryRequest) openobserve.TracesQueryParams {
	params := openobserve.TracesQueryParams{
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Scope: openobserve.Scope{
			Namespace: req.SearchScope.Namespace,
		},
	}
	if req.Limit != nil {
		params.Limit = *req.Limit
	}
	if req.Sort != nil {
		params.Sort = string(*req.Sort)
	}
	if req.SearchScope.Project != nil {
		params.Scope.ProjectID = *req.SearchScope.Project
	}
	if req.SearchScope.Component != nil {
		params.Scope.ComponentID = *req.SearchScope.Component
	}
	if req.SearchScope.Environment != nil {
		params.Scope.EnvironmentID = *req.SearchScope.Environment
	}
	return params
}

// toTracesListResponse converts the internal result to the generated response model.
func toTracesListResponse(result *openobserve.TracesResult) gen.TracesListResponse {
	traces := make([]struct {
		DurationNs   *float32   `json:"durationNs,omitempty"`
		EndTime      *time.Time `json:"endTime,omitempty"`
		RootSpanId   *string    `json:"rootSpanId,omitempty"`
		RootSpanKind *string    `json:"rootSpanKind,omitempty"`
		RootSpanName *string    `json:"rootSpanName,omitempty"`
		SpanCount    *int       `json:"spanCount,omitempty"`
		StartTime    *time.Time `json:"startTime,omitempty"`
		TraceId      *string    `json:"traceId,omitempty"`
		TraceName    *string    `json:"traceName,omitempty"`
	}, 0, len(result.Traces))

	for _, t := range result.Traces {
		dur := float32(t.DurationNs)
		startTime := t.StartTime
		endTime := t.EndTime
		traceId := t.TraceID
		traceName := t.TraceName
		spanCount := t.SpanCount
		rootSpanId := t.RootSpanID
		rootSpanName := t.RootSpanName
		rootSpanKind := t.RootSpanKind
		traces = append(traces, struct {
			DurationNs   *float32   `json:"durationNs,omitempty"`
			EndTime      *time.Time `json:"endTime,omitempty"`
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
		})
	}

	total := result.Total
	tookMs := result.TookMs
	return gen.TracesListResponse{
		Traces: &traces,
		Total:  &total,
		TookMs: &tookMs,
	}
}

// toSpansListResponse converts the internal result to the generated response model.
func toSpansListResponse(result *openobserve.SpansResult) gen.TraceSpansListResponse {
	spans := make([]struct {
		DurationNs   *float32   `json:"durationNs,omitempty"`
		EndTime      *time.Time `json:"endTime,omitempty"`
		ParentSpanId *string    `json:"parentSpanId,omitempty"`
		SpanId       *string    `json:"spanId,omitempty"`
		SpanKind     *string    `json:"spanKind,omitempty"`
		SpanName     *string    `json:"spanName,omitempty"`
		StartTime    *time.Time `json:"startTime,omitempty"`
	}, 0, len(result.Spans))

	for _, s := range result.Spans {
		dur := float32(s.DurationNs)
		startTime := s.StartTime
		endTime := s.EndTime
		spanId := s.SpanID
		spanName := s.SpanName
		spanKind := s.SpanKind
		parentSpanId := s.ParentSpanID
		spans = append(spans, struct {
			DurationNs   *float32   `json:"durationNs,omitempty"`
			EndTime      *time.Time `json:"endTime,omitempty"`
			ParentSpanId *string    `json:"parentSpanId,omitempty"`
			SpanId       *string    `json:"spanId,omitempty"`
			SpanKind     *string    `json:"spanKind,omitempty"`
			SpanName     *string    `json:"spanName,omitempty"`
			StartTime    *time.Time `json:"startTime,omitempty"`
		}{
			DurationNs:   &dur,
			StartTime:    &startTime,
			EndTime:      &endTime,
			SpanId:       &spanId,
			SpanName:     &spanName,
			SpanKind:     &spanKind,
			ParentSpanId: &parentSpanId,
		})
	}

	total := result.Total
	tookMs := result.TookMs
	return gen.TraceSpansListResponse{
		Spans:  &spans,
		Total:  &total,
		TookMs: &tookMs,
	}
}

// toSpanDetailsResponse converts the internal span detail to the generated response model.
func toSpanDetailsResponse(span *openobserve.SpanDetail) gen.TraceSpanDetailsResponse {
	dur := float32(span.DurationNs)
	startTime := span.StartTime
	endTime := span.EndTime

	attrs := make([]struct {
		Key   *string `json:"key,omitempty"`
		Value *string `json:"value,omitempty"`
	}, 0, len(span.Attributes))
	for _, a := range span.Attributes {
		key := a.Key
		value := a.Value
		attrs = append(attrs, struct {
			Key   *string `json:"key,omitempty"`
			Value *string `json:"value,omitempty"`
		}{Key: &key, Value: &value})
	}

	resAttrs := make([]struct {
		Key   *string `json:"key,omitempty"`
		Value *string `json:"value,omitempty"`
	}, 0, len(span.ResourceAttributes))
	for _, a := range span.ResourceAttributes {
		key := a.Key
		value := a.Value
		resAttrs = append(resAttrs, struct {
			Key   *string `json:"key,omitempty"`
			Value *string `json:"value,omitempty"`
		}{Key: &key, Value: &value})
	}

	return gen.TraceSpanDetailsResponse{
		SpanId:             &span.SpanID,
		SpanName:           &span.SpanName,
		SpanKind:           &span.SpanKind,
		StartTime:          &startTime,
		EndTime:            &endTime,
		DurationNs:         &dur,
		ParentSpanId:       &span.ParentSpanID,
		Attributes:         &attrs,
		ResourceAttributes: &resAttrs,
	}
}

func ptr[T any](v T) *T {
	return &v
}
