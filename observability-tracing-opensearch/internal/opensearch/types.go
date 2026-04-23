// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// SearchResponse represents the response from an OpenSearch search query.
type SearchResponse struct {
	Hits struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		Hits []Hit `json:"hits"`
	} `json:"hits"`
	Aggregations json.RawMessage `json:"aggregations,omitempty"`
	Took         int             `json:"took"`
	TimedOut     bool            `json:"timed_out"`
}

// TracesAggregationResult represents the parsed aggregation response for traces queries.
type TracesAggregationResult struct {
	TraceCount struct {
		Value int `json:"value"`
	} `json:"trace_count"`
	Traces struct {
		Buckets []TraceBucket `json:"buckets"`
	} `json:"traces"`
}

// TraceBucket represents a single trace bucket from the terms aggregation.
type TraceBucket struct {
	Key            string             `json:"key"`
	DocCount       int                `json:"doc_count"`
	EarliestSpan   AggTopHitsValue    `json:"earliest_span"`
	RootSpan       AggFilteredTopHits `json:"root_span"`
	LatestSpan     AggTopHitsValue    `json:"latest_span"`
	ErrorSpanCount AggFilteredTopHits `json:"error_span_count"`
}

// AggTopHitsValue represents a top_hits aggregation result.
type AggTopHitsValue struct {
	Hits struct {
		Hits []Hit `json:"hits"`
	} `json:"hits"`
}

// AggFilteredTopHits represents a filter aggregation with a nested top_hits sub-aggregation.
type AggFilteredTopHits struct {
	DocCount int             `json:"doc_count"`
	Hit      AggTopHitsValue `json:"hit"`
}

// Hit represents a single search result hit.
type Hit struct {
	ID     string                 `json:"_id"`
	Source map[string]interface{} `json:"_source"`
	Score  *float64               `json:"_score"`
}

// Span represents a parsed span entry from OpenSearch.
type Span struct {
	DurationNanoseconds    int64                  `json:"durationNanoseconds"`
	EndTime                time.Time              `json:"endTime"`
	Name                   string                 `json:"name"`
	OpenChoreoComponentUID string                 `json:"openChoreoComponentUid"`
	OpenChoreoProjectUID   string                 `json:"openChoreoProjectUid"`
	ParentSpanID           string                 `json:"parentSpanId"`
	SpanID                 string                 `json:"spanId"`
	SpanKind               string                 `json:"spanKind"`
	StartTime              time.Time              `json:"startTime"`
	Status                 string                 `json:"status,omitempty"`
	Attributes             map[string]interface{} `json:"attributes,omitempty"`
	ResourceAttributes     map[string]interface{} `json:"resourceAttributes,omitempty"`
}

// TracesRequestParams holds request body parameters for traces.
type TracesRequestParams struct {
	ComponentUIDs     []string `json:"componentUids,omitempty"`
	EndTime           string   `json:"endTime"`
	EnvironmentUID    string   `json:"environmentUid,omitempty"`
	IncludeAttributes bool     `json:"includeAttributes,omitempty"`
	Limit             int      `json:"limit,omitempty"`
	Namespace         string   `json:"namespace"`
	ProjectUID        string   `json:"projectUid"`
	SortOrder         string   `json:"sortOrder,omitempty"`
	StartTime         string   `json:"startTime"`
	TraceID           string   `json:"traceId,omitempty"`
}

// buildSearchBody converts a query map to an io.Reader for the search request.
func buildSearchBody(query map[string]interface{}) (io.Reader, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search query: %w", err)
	}
	return strings.NewReader(string(body)), nil
}
