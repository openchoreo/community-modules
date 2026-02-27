// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package openobserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Scope holds the filtering scope for trace queries.
type Scope struct {
	Namespace     string `json:"namespace"`
	ProjectID     string `json:"projectId"`
	ComponentID   string `json:"componentId"`
	EnvironmentID string `json:"environmentId"`
}

// TracesQueryParams holds parameters for trace queries.
type TracesQueryParams struct {
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
	Limit     int       `json:"limit"`
	Sort      string    `json:"sort"`
	Scope     Scope     `json:"scope"`
	TraceID   string    `json:"-"`
	SpanID    string    `json:"-"`
}

// TraceEntry represents a trace in the traces list response
type TraceEntry struct {
	TraceID      string    `json:"traceId"`
	TraceName    string    `json:"traceName"`
	SpanCount    int       `json:"spanCount"`
	RootSpanID   string    `json:"rootSpanId"`
	RootSpanName string    `json:"rootSpanName"`
	RootSpanKind string    `json:"rootSpanKind"`
	StartTime    time.Time `json:"startTime"`
	EndTime      time.Time `json:"endTime"`
	DurationNs   int64     `json:"durationNs"`
}

// TracesResult represents the response when listing traces
type TracesResult struct {
	Traces []TraceEntry `json:"traces"`
	Total  int          `json:"total"`
	TookMs int          `json:"tookMs"`
}

// SpanEntry represents a span in the spans list response
type SpanEntry struct {
	SpanID       string    `json:"spanId"`
	SpanName     string    `json:"spanName"`
	SpanKind     string    `json:"spanKind"`
	StartTime    time.Time `json:"startTime"`
	EndTime      time.Time `json:"endTime"`
	DurationNs   int64     `json:"durationNs"`
	ParentSpanID string    `json:"parentSpanId"`
}

// SpansResult represents the response when listing spans for a trace
type SpansResult struct {
	Spans  []SpanEntry `json:"spans"`
	Total  int         `json:"total"`
	TookMs int         `json:"tookMs"`
}

// SpanAttribute represents a key-value attribute on a span
type SpanAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SpanDetail represents a single span with full attributes
type SpanDetail struct {
	SpanID             string          `json:"spanId"`
	SpanName           string          `json:"spanName"`
	SpanKind           string          `json:"spanKind"`
	StartTime          time.Time       `json:"startTime"`
	EndTime            time.Time       `json:"endTime"`
	DurationNs         int64           `json:"durationNs"`
	ParentSpanID       string          `json:"parentSpanId"`
	Attributes         []SpanAttribute `json:"attributes"`
	ResourceAttributes []SpanAttribute `json:"resourceAttributes"`
}

// SpanDetailResult represents the response when fetching a single span
type SpanDetailResult struct {
	Span SpanDetail `json:"span"`
}

// OpenObserveResponse represents the raw response from OpenObserve search API
type OpenObserveResponse struct {
	Took  int                      `json:"took"`
	Hits  []map[string]interface{} `json:"hits"`
	Total int                      `json:"total"`
}

type Client struct {
	baseURL    string
	org        string
	stream     string
	user       string
	token      string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(baseURL, org, stream, user, token string, logger *slog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		org:     org,
		stream:  stream,
		user:    user,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// executeSearchQuery executes a search query against OpenObserve and returns the parsed response
func (c *Client) executeSearchQuery(ctx context.Context, queryJSON []byte) (*OpenObserveResponse, error) {
	url := fmt.Sprintf("%s/api/%s/_search?type=traces", c.baseURL, c.org)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(queryJSON))
	if err != nil {
		c.logger.Error("Failed to create request", slog.Any("error", err))
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to execute search request against OpenObserve", slog.Any("error", err))
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body returned by OpenObserve", slog.Any("error", err))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("OpenObserve returned error",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		return nil, fmt.Errorf("openobserve returned status %d: response body omitted", resp.StatusCode)
	}

	var openObserveResp OpenObserveResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&openObserveResp); err != nil {
		c.logger.Error("Failed to unmarshal response from OpenObserve", slog.Any("error", err))
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &openObserveResp, nil
}

// GetTraces queries OpenObserve for a list of traces using the search API.
// It fetches individual spans, groups them by trace_id, and identifies the root span
// (the span with no parent) per trace to populate rootSpanId, rootSpanName, and rootSpanKind.
func (c *Client) GetTraces(ctx context.Context, params TracesQueryParams) (*TracesResult, error) {
	queryJSON, err := generateTracesListQuery(params, c.stream, c.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to generate traces query: %w", err)
	}

	openObserveResp, err := c.executeSearchQuery(ctx, queryJSON)
	if err != nil {
		return nil, err
	}

	// Group spans by trace_id and build trace entries
	type traceAgg struct {
		entry    TraceEntry
		minStart int64
		maxEnd   int64
	}
	traceMap := make(map[string]*traceAgg)
	traceOrder := make([]string, 0)

	for _, hit := range openObserveResp.Hits {
		traceID, _ := hit["trace_id"].(string)
		if traceID == "" {
			continue
		}

		agg, exists := traceMap[traceID]
		if !exists {
			agg = &traceAgg{}
			agg.entry.TraceID = traceID
			traceMap[traceID] = agg
			traceOrder = append(traceOrder, traceID)
		}

		agg.entry.SpanCount++

		var startTime, endTime int64
		if v, ok := hit["start_time"].(json.Number); ok {
			startTime, _ = v.Int64()
			if agg.minStart == 0 || startTime < agg.minStart {
				agg.minStart = startTime
			}
		}
		if v, ok := hit["end_time"].(json.Number); ok {
			endTime, _ = v.Int64()
			if endTime > agg.maxEnd {
				agg.maxEnd = endTime
			}
		}

		// Identify root span: the span with no parent
		parentSpanID, _ := hit["reference_parent_span_id"].(string)
		if parentSpanID == "" {
			if v, ok := hit["span_id"].(string); ok {
				agg.entry.RootSpanID = v
			}
			if v, ok := hit["operation_name"].(string); ok {
				agg.entry.RootSpanName = v
			}
			if v, ok := hit["span_kind"].(string); ok {
				agg.entry.RootSpanKind = v
			}
		}
	}

	traces := make([]TraceEntry, 0, len(traceOrder))
	for _, traceID := range traceOrder {
		agg := traceMap[traceID]
		agg.entry.StartTime = time.Unix(0, agg.minStart)
		agg.entry.EndTime = time.Unix(0, agg.maxEnd)
		agg.entry.DurationNs = agg.maxEnd - agg.minStart
		agg.entry.TraceName = agg.entry.RootSpanName
		traces = append(traces, agg.entry)
	}

	return &TracesResult{
		Traces: traces,
		Total:  len(traces),
		TookMs: openObserveResp.Took,
	}, nil
}

// GetSpans queries OpenObserve for a list of spans belonging to the given traceId.
func (c *Client) GetSpans(ctx context.Context, params TracesQueryParams) (*SpansResult, error) {
	queryJSON, err := generateSpansListQuery(params, c.stream, c.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to generate spans query: %w", err)
	}

	openObserveResp, err := c.executeSearchQuery(ctx, queryJSON)
	if err != nil {
		return nil, err
	}

	spans := make([]SpanEntry, 0, len(openObserveResp.Hits))
	for _, hit := range openObserveResp.Hits {
		entry := parseSpanEntry(hit)
		spans = append(spans, entry)
	}

	return &SpansResult{
		Spans:  spans,
		Total:  openObserveResp.Total,
		TookMs: openObserveResp.Took,
	}, nil
}

// GetSpanDetail queries OpenObserve for a single span identified by traceId and spanId.
func (c *Client) GetSpanDetail(ctx context.Context, params TracesQueryParams) (*SpanDetailResult, error) {
	queryJSON, err := generateSpanDetailQuery(params, c.stream, c.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to generate span detail query: %w", err)
	}

	openObserveResp, err := c.executeSearchQuery(ctx, queryJSON)
	if err != nil {
		return nil, err
	}

	if len(openObserveResp.Hits) == 0 {
		return nil, fmt.Errorf("span not found: traceId=%s, spanId=%s", params.TraceID, params.SpanID)
	}

	span := parseSpanDetail(openObserveResp.Hits[0])

	return &SpanDetailResult{
		Span: span,
	}, nil
}

// parseSpanEntry converts a raw OpenObserve hit into a SpanEntry
func parseSpanEntry(hit map[string]interface{}) SpanEntry {
	entry := SpanEntry{}

	if v, ok := hit["span_id"].(string); ok {
		entry.SpanID = v
	}
	if v, ok := hit["operation_name"].(string); ok {
		entry.SpanName = v
	}
	if v, ok := hit["span_kind"].(string); ok {
		entry.SpanKind = v
	}
	if v, ok := hit["start_time"].(json.Number); ok {
		n, _ := v.Int64()
		entry.StartTime = time.Unix(0, n)
	}
	if v, ok := hit["end_time"].(json.Number); ok {
		n, _ := v.Int64()
		entry.EndTime = time.Unix(0, n)
	}
	if v, ok := hit["duration"].(json.Number); ok {
		entry.DurationNs, _ = v.Int64()
	}
	if v, ok := hit["reference_parent_span_id"].(string); ok {
		entry.ParentSpanID = v
	}

	return entry
}

// internalFields contains field keys that are mapped to SpanDetail struct fields
// and should be excluded from the attributes list. Expand this slice to exclude
// additional fields in the future.
var internalFields = []string{
	"_timestamp",
	"span_id",
	"operation_name",
	"span_kind",
	"start_time",
	"end_time",
	"duration",
	"parent_span_id",
	"reference_parent_span_id",
	"trace_id",
}

// parseSpanDetail converts a raw OpenObserve hit into a SpanDetail with attributes
func parseSpanDetail(hit map[string]interface{}) SpanDetail {
	detail := SpanDetail{}

	if v, ok := hit["span_id"].(string); ok {
		detail.SpanID = v
	}
	if v, ok := hit["operation_name"].(string); ok {
		detail.SpanName = v
	}
	if v, ok := hit["span_kind"].(string); ok {
		detail.SpanKind = v
	}
	if v, ok := hit["start_time"].(json.Number); ok {
		n, _ := v.Int64()
		detail.StartTime = time.Unix(0, n)
	}
	if v, ok := hit["end_time"].(json.Number); ok {
		n, _ := v.Int64()
		detail.EndTime = time.Unix(0, n)
	}
	if v, ok := hit["duration"].(json.Number); ok {
		detail.DurationNs, _ = v.Int64()
	}
	if v, ok := hit["reference_parent_span_id"].(string); ok {
		detail.ParentSpanID = v
	}

	excludeFields := make(map[string]bool, len(internalFields))
	for _, f := range internalFields {
		excludeFields[f] = true
	}

	attributes := make([]SpanAttribute, 0)
	resourceAttributes := make([]SpanAttribute, 0)
	for key, value := range hit {
		if excludeFields[key] {
			continue
		}
		attr := SpanAttribute{
			Key:   key,
			Value: fmt.Sprintf("%v", value),
		}
		if strings.HasPrefix(key, "service") || strings.HasPrefix(key, "resource") {
			resourceAttributes = append(resourceAttributes, attr)
		} else {
			attributes = append(attributes, attr)
		}
	}
	detail.Attributes = attributes
	detail.ResourceAttributes = resourceAttributes

	return detail
}
