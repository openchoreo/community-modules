// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openchoreo/observability-tracing-moesif-cloud/adaptor-api/gen"
	"github.com/openchoreo/observability-tracing-moesif-cloud/adaptor-api/internal/config"
	"github.com/openchoreo/observability-tracing-moesif-cloud/adaptor-api/internal/search"
)

// defaultSpanDetailsLookback bounds the search window for GetSpanDetailsForTrace,
// whose OpenAPI contract has no startTime/endTime parameters.
const defaultSpanDetailsLookback = 30 * 24 * time.Hour

// Handler implements gen.ServerInterface.
type Handler struct {
	searchClient *search.Client
}

func New(searchClient *search.Client) *Handler {
	return &Handler{searchClient: searchClient}
}

func (h *Handler) Health(c *gin.Context) {
	probe, err := h.searchClient.HealthProbe(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": err.Error()})
		return
	}
	if !probe.Status {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "upstream": probe})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy", "upstream": probe})
}

func (h *Handler) QueryTraces(c *gin.Context) {
	moesifAppID := c.GetHeader("X-Moesif-App-Id")
	moesifOrgID := c.GetHeader("X-Moesif-Org-Id")

	if h.searchClient.AuthMode() == config.AuthModeAPIKey {
		if moesifAppID == "" || moesifOrgID == "" {
			title := gen.ErrorResponseTitle("notFound")
			c.JSON(http.StatusNotFound, gen.ErrorResponse{Title: &title, Detail: strPtr("missing required X-Moesif-App-Id or X-Moesif-Org-Id header")})
			return
		}
	}

	var req gen.QueryTracesJSONRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		title := gen.BadRequest
		c.JSON(http.StatusBadRequest, gen.ErrorResponse{Title: &title, Detail: strPtr(err.Error())})
		return
	}

	sortOrder := "desc"
	if req.SortOrder != nil {
		sortOrder = string(*req.SortOrder)
	}

	limit := 100
	if req.Limit != nil {
		limit = *req.Limit
	}

	params := search.TraceSearchParams{
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Size:        limit,
		SortOrder:   sortOrder,
		MoesifAppID: moesifAppID,
		MoesifOrgID: moesifOrgID,
	}
	applyScope(&params, req.SearchScope)

	result, err := h.searchClient.SearchTraceEvents(c.Request.Context(), params)
	if err != nil {
		title := gen.InternalServerError
		c.JSON(http.StatusInternalServerError, gen.ErrorResponse{Title: &title, Detail: strPtr(err.Error())})
		return
	}

	traces := mapAggregationBucketsToTraces(result)

	c.JSON(http.StatusOK, gen.TracesListResponse{
		Traces: &traces,
		Total:  intPtr(len(traces)),
		TookMs: intPtr(result.Took),
	})
}

func (h *Handler) QuerySpansForTrace(c *gin.Context, traceId string) {
	moesifAppID := c.GetHeader("X-Moesif-App-Id")
	moesifOrgID := c.GetHeader("X-Moesif-Org-Id")

	if h.searchClient.AuthMode() == config.AuthModeAPIKey {
		if moesifAppID == "" || moesifOrgID == "" {
			title := gen.ErrorResponseTitle("notFound")
			c.JSON(http.StatusNotFound, gen.ErrorResponse{Title: &title, Detail: strPtr("missing required X-Moesif-App-Id or X-Moesif-Org-Id header")})
			return
		}
	}

	var req gen.QuerySpansForTraceJSONRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		title := gen.BadRequest
		c.JSON(http.StatusBadRequest, gen.ErrorResponse{Title: &title, Detail: strPtr(err.Error())})
		return
	}

	sortOrder := "desc"
	if req.SortOrder != nil {
		sortOrder = string(*req.SortOrder)
	}
	size := 100
	if req.Limit != nil {
		size = *req.Limit
	}

	params := search.TraceSearchParams{
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Size:        size,
		SortOrder:   sortOrder,
		TraceID:     traceId,
		MoesifAppID: moesifAppID,
		MoesifOrgID: moesifOrgID,
	}
	applyScope(&params, req.SearchScope)

	includeAttributes := req.IncludeAttributes != nil && *req.IncludeAttributes

	result, err := h.searchClient.SearchTraceSpans(c.Request.Context(), params)
	if err != nil {
		title := gen.InternalServerError
		c.JSON(http.StatusInternalServerError, gen.ErrorResponse{Title: &title, Detail: strPtr(err.Error())})
		return
	}

	spans := make([]traceSpanItem, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		rec := mapHitToSpanRecord(hit.Source)
		spans = append(spans, toSpanItem(rec, includeAttributes))
	}

	c.JSON(http.StatusOK, gen.TraceSpansListResponse{
		Spans:  &spans,
		Total:  intPtr(result.Hits.Total),
		TookMs: intPtr(result.Took),
	})
}

func (h *Handler) GetSpanDetailsForTrace(c *gin.Context, traceId string, spanId string) {
	moesifAppID := c.GetHeader("X-Moesif-App-Id")
	moesifOrgID := c.GetHeader("X-Moesif-Org-Id")

	if h.searchClient.AuthMode() == config.AuthModeAPIKey {
		if moesifAppID == "" || moesifOrgID == "" {
			title := gen.ErrorResponseTitle("notFound")
			c.JSON(http.StatusNotFound, gen.ErrorResponse{Title: &title, Detail: strPtr("missing required X-Moesif-App-Id or X-Moesif-Org-Id header")})
			return
		}
	}

	now := time.Now()
	params := search.TraceSearchParams{
		StartTime:   now.Add(-defaultSpanDetailsLookback),
		EndTime:     now,
		Size:        1,
		TraceID:     traceId,
		SpanID:      spanId,
		MoesifAppID: moesifAppID,
		MoesifOrgID: moesifOrgID,
	}

	result, err := h.searchClient.SearchTraceSpanById(c.Request.Context(), params)
	if err != nil {
		title := gen.InternalServerError
		c.JSON(http.StatusInternalServerError, gen.ErrorResponse{Title: &title, Detail: strPtr(err.Error())})
		return
	}

	if len(result.Hits.Hits) == 0 {
		c.JSON(http.StatusNotFound, gen.ErrorResponse{Detail: strPtr("span not found")})
		return
	}

	rec := mapHitToSpanRecord(result.Hits.Hits[0].Source)
	item := toSpanItem(rec, true)

	c.JSON(http.StatusOK, gen.TraceSpanDetailsResponse{
		SpanId:             item.SpanId,
		SpanName:           item.SpanName,
		SpanKind:           item.SpanKind,
		StartTime:          item.StartTime,
		EndTime:            item.EndTime,
		DurationNs:         item.DurationNs,
		ParentSpanId:       item.ParentSpanId,
		Status:             item.Status,
		Attributes:         item.Attributes,
		ResourceAttributes: item.ResourceAttributes,
	})
}

// applyScope copies the ComponentSearchScope fields into the search params.
func applyScope(params *search.TraceSearchParams, scope gen.ComponentSearchScope) {
	params.Namespace = scope.Namespace
	if scope.Project != nil {
		params.Project = *scope.Project
	}
	if scope.Component != nil {
		params.Component = *scope.Component
	}
	if scope.Environment != nil {
		params.Environment = *scope.Environment
	}
}

// traceSpanItem matches the anonymous item type generated for
// TraceSpansListResponse.Spans / TracesListResponse.Traces.
type traceSpanItem = struct {
	Attributes         *map[string]interface{} `json:"attributes,omitempty"`
	DurationNs         *int64                  `json:"durationNs,omitempty"`
	EndTime            *time.Time              `json:"endTime,omitempty"`
	ParentSpanId       *string                 `json:"parentSpanId,omitempty"`
	ResourceAttributes *map[string]interface{} `json:"resourceAttributes,omitempty"`
	SpanId             *string                 `json:"spanId,omitempty"`
	SpanKind           *string                 `json:"spanKind,omitempty"`
	SpanName           *string                 `json:"spanName,omitempty"`
	StartTime          *time.Time              `json:"startTime,omitempty"`
	Status             *gen.SpanStatus         `json:"status,omitempty"`
}

// spanRecord is the parsed representation of a single span extracted from a
// Moesif search hit. Field extraction assumes span data is nested under a
// "span" object and the trace ID under "trace_id" on the event; verify these
// paths against a real Moesif trace event payload.
type spanRecord struct {
	TraceID       string
	SpanID        string
	SpanName      string
	SpanKind      string
	ParentSpanID  string
	StatusCode    string
	StatusMessage string
	StartTime     *time.Time
	EndTime       *time.Time
	DurationNs    int64
	Attributes    map[string]interface{}
	Resource      map[string]interface{}
}

func mapHitToSpanRecord(source map[string]interface{}) spanRecord {
	rec := spanRecord{}

	req, _ := source["request"].(map[string]interface{})
	resp, _ := source["response"].(map[string]interface{})

	if req != nil {
		if rt, ok := req["time"].(string); ok {
			rec.StartTime = parseTime(rt)
		}
	}
	if resp != nil {
		if rt, ok := resp["time"].(string); ok {
			rec.EndTime = parseTime(rt)
		}
	}

	if dm, ok := source["duration_ms"].(float64); ok {
		rec.DurationNs = int64(dm * 1e6)
	}
	if rec.EndTime == nil && rec.StartTime != nil && rec.DurationNs > 0 {
		end := rec.StartTime.Add(time.Duration(rec.DurationNs))
		rec.EndTime = &end
	}
	if rec.DurationNs == 0 && rec.StartTime != nil && rec.EndTime != nil {
		rec.DurationNs = rec.EndTime.Sub(*rec.StartTime).Nanoseconds()
	}

	rec.TraceID, _ = source["trace_id"].(string)
	rec.SpanName, _ = source["action_name"].(string)

	if span, ok := source["span"].(map[string]interface{}); ok {
		rec.SpanID, _ = span["id"].(string)
		rec.SpanKind, _ = span["kind"].(string)
		rec.ParentSpanID, _ = span["parent_id"].(string)
		rec.StatusCode, _ = span["status"].(string)
		rec.StatusMessage, _ = span["status_message"].(string)
		rec.Attributes, _ = span["attributes"].(map[string]interface{})
	}

	rec.Resource, _ = source["resource"].(map[string]interface{})

	// If resource attributes are stored as flat dot-notation keys (e.g. "resource.openchoreo.dev/namespace")
	// at the top level of _source, collect them into the Resource map.
	if rec.Resource == nil {
		rec.Resource = make(map[string]interface{})
	}
	for k, v := range source {
		if strings.HasPrefix(k, "resource.") {
			rec.Resource[strings.TrimPrefix(k, "resource.")] = v
		}
	}
	if len(rec.Resource) == 0 {
		rec.Resource = nil
	}

	return rec
}

func parseTime(s string) *time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func toSpanItem(rec spanRecord, includeAttributes bool) traceSpanItem {
	item := traceSpanItem{
		SpanId:       strPtrOrNil(rec.SpanID),
		SpanName:     strPtrOrNil(rec.SpanName),
		SpanKind:     strPtrOrNil(rec.SpanKind),
		StartTime:    rec.StartTime,
		EndTime:      rec.EndTime,
		ParentSpanId: strPtrOrNil(rec.ParentSpanID),
	}
	if rec.DurationNs != 0 {
		item.DurationNs = int64Ptr(rec.DurationNs)
	}
	if rec.StatusCode != "" {
		code := mapSpanStatusCode(rec.StatusCode)
		item.Status = &gen.SpanStatus{Code: &code, Message: strPtrOrNil(rec.StatusMessage)}
	}
	if includeAttributes {
		if rec.Attributes != nil {
			item.Attributes = &rec.Attributes
		}
	}
	if rec.Resource != nil {
		item.ResourceAttributes = &rec.Resource
	}
	return item
}

func mapSpanStatusCode(raw string) gen.SpanStatusCode {
	switch strings.ToLower(raw) {
	case "ok", "status_code_ok":
		return gen.Ok
	case "error", "status_code_error":
		return gen.Error
	default:
		return gen.Unset
	}
}

// mapAggregationBucketsToTraces converts aggregation buckets from the search
// response into trace summary items.
func mapAggregationBucketsToTraces(result *search.SearchEventsResponse) []struct {
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
} {
	type traceItem = struct {
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
	}

	if result.Aggregations == nil {
		return []traceItem{}
	}

	buckets := result.Aggregations.Traces.Buckets
	items := make([]traceItem, 0, len(buckets))

	for _, bucket := range buckets {
		traceID := bucket.Key

		// Parse start/end times from aggregation values (epoch millis).
		startMs := int64(bucket.StartTime.Value)
		endMs := int64(bucket.EndTime.Value)
		start := time.UnixMilli(startMs).UTC()
		end := time.UnixMilli(endMs).UTC()

		durationNs := end.Sub(start).Nanoseconds()

		// Extract root span info from top_hits.
		var rootSpanID, rootSpanName, rootSpanKind string
		if len(bucket.RootSpan.Hits.Hits) > 0 {
			src := bucket.RootSpan.Hits.Hits[0].Source
			if span, ok := src["span"].(map[string]interface{}); ok {
				rootSpanID, _ = span["id"].(string)
				rootSpanKind, _ = span["kind"].(string)
			}
			if name, ok := src["action_name"].(string); ok {
				rootSpanName = name
			}
		}

		hasErrors := false
		item := traceItem{
			TraceId:      strPtrOrNil(traceID),
			TraceName:    strPtrOrNil(rootSpanName),
			SpanCount:    intPtr(bucket.DocCount),
			RootSpanId:   strPtrOrNil(rootSpanID),
			RootSpanName: strPtrOrNil(rootSpanName),
			RootSpanKind: strPtrOrNil(rootSpanKind),
			StartTime:    &start,
			EndTime:      &end,
			DurationNs:   int64Ptr(durationNs),
			HasErrors:    &hasErrors,
		}
		items = append(items, item)
	}

	return items
}

func strPtr(s string) *string { return &s }
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func intPtr(i int) *int       { return &i }
func int64Ptr(i int64) *int64 { return &i }
