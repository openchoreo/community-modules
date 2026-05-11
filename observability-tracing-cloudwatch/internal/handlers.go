// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-cloudwatch/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-cloudwatch/internal/xray"
)

type TracingHandler struct {
	client *xray.Client
	logger *slog.Logger
}

func NewTracingHandler(client *xray.Client, logger *slog.Logger) *TracingHandler {
	return &TracingHandler{
		client: client,
		logger: logger,
	}
}

var _ gen.StrictServerInterface = (*TracingHandler)(nil)

func (h *TracingHandler) Health(ctx context.Context, _ gen.HealthRequestObject) (gen.HealthResponseObject, error) {
	status := "healthy"
	return gen.Health200JSONResponse{Status: &status}, nil
}

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

func (h *TracingHandler) GetSpanDetailsForTrace(ctx context.Context, request gen.GetSpanDetailsForTraceRequestObject) (gen.GetSpanDetailsForTraceResponseObject, error) {
	params := xray.TracesQueryParams{
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

func toTracesQueryParams(req *gen.TracesQueryRequest) xray.TracesQueryParams {
	params := xray.TracesQueryParams{
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Scope: xray.Scope{
			Namespace: req.SearchScope.Namespace,
		},
	}
	if req.Limit != nil {
		params.Limit = *req.Limit
	}
	if req.SortOrder != nil {
		params.SortOrder = string(*req.SortOrder)
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
	if req.IncludeAttributes != nil {
		params.IncludeAttributes = *req.IncludeAttributes
	}
	return params
}

func toTracesListResponse(result *xray.TracesResult) gen.TracesListResponse {
	traces := make([]struct {
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
	}, 0, len(result.Traces))

	for _, t := range result.Traces {
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
		traces = append(traces, struct {
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

	total := result.Total
	tookMs := result.TookMs
	return gen.TracesListResponse{
		Traces: &traces,
		Total:  &total,
		TookMs: &tookMs,
	}
}

func toSpansListResponse(result *xray.SpansResult) gen.TraceSpansListResponse {
	spans := make([]struct {
		Attributes         *map[string]interface{}                 `json:"attributes,omitempty"`
		DurationNs         *int64                                  `json:"durationNs,omitempty"`
		EndTime            *time.Time                              `json:"endTime,omitempty"`
		ParentSpanId       *string                                 `json:"parentSpanId,omitempty"`
		ResourceAttributes *map[string]interface{}                 `json:"resourceAttributes,omitempty"`
		SpanId             *string                                 `json:"spanId,omitempty"`
		SpanKind           *string                                 `json:"spanKind,omitempty"`
		SpanName           *string                                 `json:"spanName,omitempty"`
		StartTime          *time.Time                              `json:"startTime,omitempty"`
		Status             *gen.TraceSpansListResponseSpansStatus  `json:"status,omitempty"`
	}, 0, len(result.Spans))

	for _, s := range result.Spans {
		dur := s.DurationNs
		startTime := s.StartTime
		endTime := s.EndTime
		spanId := s.SpanID
		spanName := s.SpanName
		spanKind := s.SpanKind
		parentSpanId := s.ParentSpanID
		status := gen.TraceSpansListResponseSpansStatus(s.Status)

		entry := struct {
			Attributes         *map[string]interface{}                 `json:"attributes,omitempty"`
			DurationNs         *int64                                  `json:"durationNs,omitempty"`
			EndTime            *time.Time                              `json:"endTime,omitempty"`
			ParentSpanId       *string                                 `json:"parentSpanId,omitempty"`
			ResourceAttributes *map[string]interface{}                 `json:"resourceAttributes,omitempty"`
			SpanId             *string                                 `json:"spanId,omitempty"`
			SpanKind           *string                                 `json:"spanKind,omitempty"`
			SpanName           *string                                 `json:"spanName,omitempty"`
			StartTime          *time.Time                              `json:"startTime,omitempty"`
			Status             *gen.TraceSpansListResponseSpansStatus  `json:"status,omitempty"`
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

		if s.Attributes != nil && len(s.Attributes) > 0 {
			entry.Attributes = &s.Attributes
		}
		if s.ResourceAttributes != nil && len(s.ResourceAttributes) > 0 {
			entry.ResourceAttributes = &s.ResourceAttributes
		}

		spans = append(spans, entry)
	}

	total := result.Total
	tookMs := result.TookMs
	return gen.TraceSpansListResponse{
		Spans:  &spans,
		Total:  &total,
		TookMs: &tookMs,
	}
}

func toSpanDetailsResponse(span *xray.SpanDetail) gen.TraceSpanDetailsResponse {
	dur := span.DurationNs
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
		Status:             ptr(gen.TraceSpanDetailsResponseStatus(span.Status)),
		Attributes:         &attrs,
		ResourceAttributes: &resAttrs,
	}
}

func ptr[T any](v T) *T {
	return &v
}

