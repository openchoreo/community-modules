// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openchoreo/observability-tracing-moesif-cloud/adaptor-api/internal/config"
	"github.com/openchoreo/observability-tracing-moesif-cloud/adaptor-api/internal/envresolver"
)

// Client is an HTTP client for the Moesif search API.
type Client struct {
	baseURL     string
	authMode    string
	httpClient  *http.Client
	logger      *slog.Logger
	envResolver *envresolver.Resolver
	envTokens   map[string]string // environment name -> bearer token
}

func NewClient(cfg *config.Config, logger *slog.Logger) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(cfg.SearchEndpoint, "/"),
		authMode:   cfg.AuthMode(),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
		envTokens:  cfg.EnvTokens,
	}
}

// SetEnvResolver sets the environment resolver for UID-to-name lookups.
func (c *Client) SetEnvResolver(r *envresolver.Resolver) {
	c.envResolver = r
}

// ResolveEnvUID returns the environment name for a given UID, or the UID itself if not found.
func (c *Client) ResolveEnvUID(uid string) string {
	if c.envResolver != nil {
		return c.envResolver.ResolveUID(uid)
	}
	return uid
}

// ResolveEnvToken resolves an environment UID to the corresponding token.
// For AuthModeAPIKey, it looks up "api_key" from the envTokens map.
// For AuthModeBearer, it resolves the environment name and returns the matching token.
// Returns the token and an error if not found.
func (c *Client) ResolveEnvToken(envUID string) (string, error) {
	if c.authMode == config.AuthModeAPIKey {
		if token, ok := c.envTokens["api_key"]; ok {
			return token, nil
		}
		c.logger.Error("api_key not found in token map")
		return "", fmt.Errorf("environment is not supported")
	}

	envName := c.ResolveEnvUID(envUID)
	if token, ok := c.envTokens[envName]; ok {
		return token, nil
	}
	c.logger.Error("no token found for environment",
		slog.String("envUID", envUID),
		slog.String("envName", envName))
	return "", fmt.Errorf("environment %q is not supported", envName)
}

// TraceSearchParams holds parameters for a trace/span search query.
type TraceSearchParams struct {
	StartTime   time.Time
	EndTime     time.Time
	Size        int
	SortOrder   string // "asc" or "desc"
	Namespace   string
	Project     string
	Component   string
	Environment string
	TraceID     string
	SpanID      string
	MoesifAppID string
	MoesifOrgID string
}

// SearchEventsResponse is the parsed response from the Moesif search events API.
type SearchEventsResponse struct {
	Took         int                 `json:"took"`
	Hits         searchHits          `json:"hits"`
	Aggregations *SearchAggregations `json:"aggregations,omitempty"`
}

type searchHits struct {
	Total int         `json:"total"`
	Hits  []SearchHit `json:"hits"`
}

// SearchHit is a single result from the search API.
type SearchHit struct {
	Source map[string]interface{} `json:"_source"`
}

// SearchAggregations holds the aggregation results from the search response.
type SearchAggregations struct {
	Traces TracesAggregation `json:"traces"`
}

type TracesAggregation struct {
	Buckets []TraceBucket `json:"buckets"`
}

type TraceBucket struct {
	Key       string      `json:"key"`
	DocCount  int         `json:"doc_count"`
	StartTime AggValue    `json:"startTime"`
	EndTime   AggValue    `json:"endTime"`
	RootSpan  RootSpanAgg `json:"rootSpan"`
}

type AggValue struct {
	Value         float64 `json:"value"`
	ValueAsString string  `json:"value_as_string"`
}

type RootSpanAgg struct {
	Hits RootSpanHits `json:"hits"`
}

type RootSpanHits struct {
	Hits []SearchHit `json:"hits"`
}

// HealthProbeResponse represents the response from the Moesif search /health/probe endpoint.
type HealthProbeResponse struct {
	Name   string `json:"name"`
	Status bool   `json:"status"`
	Region string `json:"region"`
	Health string `json:"health"`
	Build  string `json:"build"`
}

// HealthProbe calls the /health/probe endpoint on the Moesif search API.
// AuthMode returns the configured authentication mode.
func (c *Client) AuthMode() string {
	return c.authMode
}

func (c *Client) HealthProbe(ctx context.Context) (*HealthProbeResponse, error) {
	respBody, statusCode, err := c.do(ctx, http.MethodGet, "/health/probe", nil, "")
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("health probe returned status %d: %s", statusCode, string(respBody))
	}

	var result HealthProbeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal health probe response: %w", err)
	}
	return &result, nil
}

// SearchTraceEvents queries the Moesif search events endpoint for span/trace
// data and returns the parsed response.
func (c *Client) SearchTraceEvents(ctx context.Context, params TraceSearchParams) (*SearchEventsResponse, error) {
	body, err := buildSearchRequest(params)
	if err != nil {
		return nil, fmt.Errorf("failed to build search request: %w", err)
	}

	var path string
	q := url.Values{}
	switch c.authMode {
	case config.AuthModeBearer:
		path = "/v1/search/~/search/events"
	default:
		path = fmt.Sprintf("/admin/%s/search/events", params.MoesifOrgID)
		q.Set("app_id", params.MoesifAppID)
	}
	q.Set("from", params.StartTime.UTC().Format(time.RFC3339))
	q.Set("to", params.EndTime.UTC().Format(time.RFC3339))
	q.Set("week_starts_on", "1")

	fullPath := path + "?" + q.Encode()

	envToken, err := c.ResolveEnvToken(params.Environment)
	if err != nil {
		return nil, fmt.Errorf("internal error: %w", err)
	}
	respBody, statusCode, err := c.do(ctx, http.MethodPost, fullPath, body, envToken)

	c.logger.Info("search events API response",
		slog.Int("statusCode", statusCode),
		slog.String("responseBody", string(respBody)))

	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		c.logger.Error("search events API returned error",
			slog.Int("statusCode", statusCode),
			slog.String("body", string(respBody)))
		return nil, fmt.Errorf("search API returned status %d: %s", statusCode, string(respBody))
	}

	var result SearchEventsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal search response: %w", err)
	}
	return &result, nil
}

// SearchTraceSpans queries the Moesif search events endpoint for individual spans
// belonging to a trace and returns the parsed response.
func (c *Client) SearchTraceSpans(ctx context.Context, params TraceSearchParams) (*SearchEventsResponse, error) {
	body, err := buildSpansSearchRequest(params)
	if err != nil {
		return nil, fmt.Errorf("failed to build spans search request: %w", err)
	}

	var path string
	q := url.Values{}

	switch c.authMode {
	case config.AuthModeBearer:
		path = "/v1/search/~/search/events"
	default:
		path = fmt.Sprintf("/admin/%s/search/events", params.MoesifOrgID)
		q.Set("app_id", params.MoesifAppID)
	}
	q.Set("from", params.StartTime.UTC().Format(time.RFC3339))
	q.Set("to", params.EndTime.UTC().Format(time.RFC3339))
	q.Set("week_starts_on", "1")
	fullPath := path + "?" + q.Encode()

	envToken, err := c.ResolveEnvToken(params.Environment)
	if err != nil {
		return nil, fmt.Errorf("internal error: %w", err)
	}
	respBody, statusCode, err := c.do(ctx, http.MethodPost, fullPath, body, envToken)

	c.logger.Info("search spans API response",
		slog.Int("statusCode", statusCode),
		slog.String("responseBody", string(respBody)))

	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		c.logger.Error("search spans API returned error",
			slog.Int("statusCode", statusCode),
			slog.String("body", string(respBody)))
		return nil, fmt.Errorf("search API returned status %d: %s", statusCode, string(respBody))
	}

	var result SearchEventsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal search response: %w", err)
	}
	return &result, nil
}

// SearchTraceSpanById queries the Moesif search events endpoint for a single span
// identified by trace ID and span ID.
func (c *Client) SearchTraceSpanById(ctx context.Context, params TraceSearchParams) (*SearchEventsResponse, error) {
	body, err := buildSpansSearchRequest(params)
	if err != nil {
		return nil, fmt.Errorf("failed to build span-by-id search request: %w", err)
	}

	var path string
	q := url.Values{}
	switch c.authMode {
	case config.AuthModeBearer:
		path = "/v1/search/~/search/events"
	default:
		path = fmt.Sprintf("/admin/%s/search/events", params.MoesifOrgID)
		q.Set("app_id", params.MoesifAppID)
	}
	q.Set("from", params.StartTime.UTC().Format(time.RFC3339))
	q.Set("to", params.EndTime.UTC().Format(time.RFC3339))
	q.Set("week_starts_on", "1")
	fullPath := path + "?" + q.Encode()

	envToken, err := c.ResolveEnvToken(params.Environment)
	if err != nil {
		return nil, fmt.Errorf("internal error: %w", err)
	}
	respBody, statusCode, err := c.do(ctx, http.MethodPost, fullPath, body, envToken)

	c.logger.Info("search span by id API response",
		slog.Int("statusCode", statusCode),
		slog.String("responseBody", string(respBody)))

	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		c.logger.Error("search span by id API returned error",
			slog.Int("statusCode", statusCode),
			slog.String("body", string(respBody)))
		return nil, fmt.Errorf("search API returned status %d: %s", statusCode, string(respBody))
	}

	var result SearchEventsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal search response: %w", err)
	}
	return &result, nil
}

// do executes an authenticated HTTP request and returns the raw response body and status code.
// If tokenOverride is non-empty, it is used as the Bearer token instead of the default.
func (c *Client) do(ctx context.Context, method, path string, body []byte, bearerToken string) ([]byte, int, error) {
	u := c.baseURL + path

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewBuffer(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	switch c.authMode {
	case config.AuthModeAPIKey:
		req.Header.Set("X-Api-Token", bearerToken)
	case config.AuthModeBearer:
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("search API request failed",
			slog.String("method", method),
			slog.String("path", path),
			slog.Any("error", err))
		return nil, 0, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

func buildSearchRequest(params TraceSearchParams) ([]byte, error) {
	size := params.Size
	if size <= 0 {
		size = 100
	}

	sortOrder := params.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}

	// Always require trace_id.raw to exist.
	filter := []interface{}{
		map[string]interface{}{
			"exists": map[string]interface{}{
				"field": "trace_id.raw",
			},
		},
	}

	if params.Namespace != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/namespace": params.Namespace},
		})
	}
	if params.Project != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/project-uid": params.Project},
		})
	}
	if params.Component != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/component-uid": params.Component},
		})
	}
	if params.Environment != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/environment-uid": params.Environment},
		})
	}
	if params.TraceID != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"trace_id": params.TraceID},
		})
	}
	if params.SpanID != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"span.id": params.SpanID},
		})
	}

	req := map[string]interface{}{
		"size": 0,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": filter,
			},
		},
		"aggs": map[string]interface{}{
			"traces": map[string]interface{}{
				"terms": map[string]interface{}{
					"field": "trace_id.raw",
					"size":  size,
					"order": map[string]interface{}{
						"startTime": sortOrder,
					},
				},
				"aggs": map[string]interface{}{
					"startTime": map[string]interface{}{
						"min": map[string]interface{}{
							"field": "request.time",
						},
					},
					"endTime": map[string]interface{}{
						"max": map[string]interface{}{
							"field": "request.time",
						},
					},
					"rootSpan": map[string]interface{}{
						"top_hits": map[string]interface{}{
							"size": 1,
							"sort": []map[string]interface{}{
								{
									"request.time": map[string]interface{}{
										"order": "asc",
									},
								},
							},
							"_source": []string{
								"span.id",
								"action_name",
								"span.kind",
							},
						},
					},
				},
			},
		},
	}

	return json.Marshal(req)
}

func buildSpansSearchRequest(params TraceSearchParams) ([]byte, error) {
	size := params.Size
	if size <= 0 {
		size = 10
	}

	var filter []interface{}

	if params.Namespace != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/namespace": params.Namespace},
		})
	}
	if params.Project != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/project-uid": params.Project},
		})
	}
	if params.Component != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/component-uid": params.Component},
		})
	}
	if params.Environment != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/environment-uid": params.Environment},
		})
	}
	if params.TraceID != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"trace_id": params.TraceID},
		})
	}
	if params.SpanID != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"span.id": params.SpanID},
		})
	}

	req := map[string]interface{}{
		"size": size,
	}

	if len(filter) > 0 {
		req["query"] = map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": filter,
			},
		}
	}

	return json.Marshal(req)
}
