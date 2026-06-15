// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-tracing-azure-appinsights/internal/appinsights"
)

const (
	defaultLimit = 20
	maxLimit     = 10000
)

// tracesClient is the slice of appinsights.Client the handlers use; tests
// substitute a mock.
type tracesClient interface {
	QueryTraces(ctx context.Context, p appinsights.TracesParams) (*appinsights.TracesResult, error)
	QuerySpans(ctx context.Context, p appinsights.TracesParams) (*appinsights.SpansResult, error)
	GetSpanDetails(ctx context.Context, traceID, spanID string) (*appinsights.Span, error)
}

// TracingHandler implements the generated StrictServerInterface.
type TracingHandler struct {
	client tracesClient
	logger *slog.Logger
}

func NewTracingHandler(client tracesClient, logger *slog.Logger) *TracingHandler {
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

	params := toTracesParams(request.Body)

	result, err := h.client.QueryTraces(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query traces", slog.Any("error", err))
		return gen.QueryTraces500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: ptr("internal server error"),
		}, nil
	}

	return gen.QueryTraces200JSONResponse(toTracesListResponse(result)), nil
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
	if !appinsights.ValidID(request.TraceId) {
		return gen.QuerySpansForTrace400JSONResponse{
			Title:  ptr(gen.BadRequest),
			Detail: ptr("traceId must be a hex string"),
		}, nil
	}

	params := toTracesParams(request.Body)
	params.TraceID = request.TraceId

	result, err := h.client.QuerySpans(ctx, params)
	if err != nil {
		h.logger.Error("Failed to query spans", slog.Any("error", err))
		return gen.QuerySpansForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: ptr("internal server error"),
		}, nil
	}

	return gen.QuerySpansForTrace200JSONResponse(toSpansListResponse(result, params.IncludeAttributes)), nil
}

// GetSpanDetailsForTrace implements GET /api/v1alpha1/traces/{traceId}/spans/{spanId}.
func (h *TracingHandler) GetSpanDetailsForTrace(ctx context.Context, request gen.GetSpanDetailsForTraceRequestObject) (gen.GetSpanDetailsForTraceResponseObject, error) {
	if !appinsights.ValidID(request.TraceId) || !appinsights.ValidID(request.SpanId) {
		return gen.GetSpanDetailsForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: ptr("traceId and spanId must be hex strings"),
		}, nil
	}

	span, err := h.client.GetSpanDetails(ctx, request.TraceId, request.SpanId)
	if err != nil {
		h.logger.Error("Failed to query span detail", slog.Any("error", err))
		return gen.GetSpanDetailsForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: ptr("internal server error"),
		}, nil
	}
	if span == nil {
		detail := "span not found"
		return gen.GetSpanDetailsForTrace500JSONResponse{
			Title:  ptr(gen.InternalServerError),
			Detail: &detail,
		}, nil
	}

	return gen.GetSpanDetailsForTrace200JSONResponse(toSpanDetailsResponse(span)), nil
}

// toTracesParams converts the generated request body to internal query params.
func toTracesParams(req *gen.TracesQueryRequest) appinsights.TracesParams {
	params := appinsights.TracesParams{
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Namespace: req.SearchScope.Namespace,
	}
	if req.Limit != nil {
		params.Limit = *req.Limit
	}
	if params.Limit <= 0 {
		params.Limit = defaultLimit
	}
	if params.Limit > maxLimit {
		params.Limit = maxLimit
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
		params.ComponentUID = *req.SearchScope.Component
	}
	if req.SearchScope.Environment != nil {
		params.EnvironmentUID = *req.SearchScope.Environment
	}
	if req.IncludeAttributes != nil {
		params.IncludeAttributes = *req.IncludeAttributes
	}
	return params
}

// toTracesListResponse converts the query result to the generated response model.
func toTracesListResponse(result *appinsights.TracesResult) gen.TracesListResponse {
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
		Total:  &result.Total,
		TookMs: &result.TookMs,
	}
}

// toSpansListResponse converts internal spans to the generated response model.
func toSpansListResponse(result *appinsights.SpansResult, includeAttributes bool) gen.TraceSpansListResponse {
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
	}, 0, len(result.Spans))

	for _, s := range result.Spans {
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
		Total:  &result.Total,
		TookMs: &result.TookMs,
	}
}

// toSpanDetailsResponse converts an internal span to the generated response model.
func toSpanDetailsResponse(span *appinsights.Span) gen.TraceSpanDetailsResponse {
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
