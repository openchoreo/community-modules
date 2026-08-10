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

	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/internal/config"
	"github.com/openchoreo/observability-logs-moesif-cloud/adaptor-api/internal/envresolver"
)

// eventsSource is the fixed set of fields requested from the Moesif search API.
var eventsSource = []string{
	"id", "_id", "org_id", "app_id", "user_id", "user.created",
	"company_id", "company.created", "anonymous_id", "identified_user_id",
	"event_type", "action_name", "weight", "direction", "duration_ms",
	"request.time", "request.uri", "request.route", "request.verb",
	"request.body", "request.ip_address", "request.user_agent.name",
	"response.status", "response.time",
	"insights.anomaly", "insights.event_anomaly",
	"request.graphql.operation_name", "request.graphql.definitions",
	"jsonrpc.request.method", "jsonrpc.request.params.call__method",
	"jsonrpc.response.error",
	"vtype_ids", "blocked_by",
	"span.id", "span.status", "span.parent_id", "trace_id",
	"log.severity.text", "resource",
}

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

// AuthMode returns the configured authentication mode.
func (c *Client) AuthMode() string {
	return c.authMode
}

// SearchEventsParams holds parameters for a search events query.
type SearchEventsParams struct {
	StartTime      time.Time
	EndTime        time.Time
	Size           int
	SortOrder      string // "asc" or "desc"
	LogLevels      []string
	SearchPhrase   string
	Namespace      string
	ProjectUID     string
	ComponentUID   string
	EnvironmentUID string
	WorkflowRun    string
	MoesifAppID    string
	MoesifOrgID    string
}

// SearchEventsResponse is the parsed response from the Moesif search events API.
type SearchEventsResponse struct {
	Took int        `json:"took"`
	Hits searchHits `json:"hits"`
}

type searchHits struct {
	Total int         `json:"total"`
	Hits  []SearchHit `json:"hits"`
}

// SearchHit is a single result from the search API.
type SearchHit struct {
	Source map[string]interface{} `json:"_source"`
}

// searchRequest is the JSON body sent to the Moesif search events endpoint.
type searchRequest struct {
	Size   int                      `json:"size"`
	Sort   []map[string]interface{} `json:"sort"`
	Source []string                 `json:"_source"`
	Query  *searchQuery             `json:"query,omitempty"`
}

type searchQuery struct {
	Bool searchBoolQuery `json:"bool"`
}

type searchBoolQuery struct {
	Must   []interface{} `json:"must,omitempty"`
	Filter []interface{} `json:"filter,omitempty"`
}

// SearchEvents queries the Moesif search events endpoint and returns the parsed response.
func (c *Client) SearchEvents(ctx context.Context, params SearchEventsParams) (*SearchEventsResponse, error) {
	body, err := buildSearchRequest(params)
	if err != nil {
		return nil, fmt.Errorf("failed to build search request: %w", err)
	}

	var path string
	q := url.Values{}
	switch c.authMode {
	case config.AuthModeAPIKey:
		path = fmt.Sprintf("/admin/%s/search/events", params.MoesifOrgID)
		q.Set("app_id", params.MoesifAppID)
	default:
		path = "/v1/search/~/search/events"
	}
	q.Set("from", params.StartTime.UTC().Format(time.RFC3339))
	q.Set("to", params.EndTime.UTC().Format(time.RFC3339))
	q.Set("week_starts_on", "1")

	fullPath := path + "?" + q.Encode()

	envToken, err := c.ResolveEnvToken(params.EnvironmentUID)
	if err != nil {
		return nil, fmt.Errorf("internal error: %w", err)
	}
	respBody, statusCode, err := c.do(ctx, http.MethodPost, fullPath, body, envToken)
	if err != nil {
		return nil, err
	}
	c.logger.Debug("search events API response", slog.Int("statusCode", statusCode), slog.String("body", string(respBody)))
	if statusCode < 200 || statusCode >= 300 {
		c.logger.Error("search events API returned error",
			slog.Int("statusCode", statusCode),
			slog.String("body", string(respBody)))
		return nil, fmt.Errorf("search API returned an error, status code: %d", statusCode)
	}

	var result SearchEventsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal search response: %w", err)
	}
	return &result, nil
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
func (c *Client) HealthProbe(ctx context.Context) (*HealthProbeResponse, error) {
	respBody, statusCode, err := c.do(ctx, http.MethodGet, "/health/probe", nil, "")
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("health probe returned an error, status code: %d", statusCode)
	}

	var result HealthProbeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal health probe response: %w", err)
	}
	return &result, nil
}

// do executes an authenticated HTTP request and returns the raw response body and status code.
// If bearerToken is non-empty, it is used as the token instead of the default.
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

func buildSearchRequest(params SearchEventsParams) ([]byte, error) {
	size := params.Size
	if size <= 0 {
		size = 100
	}

	sortOrder := params.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}

	req := searchRequest{
		Size: size,
		Sort: []map[string]interface{}{
			{
				"request.time": map[string]interface{}{
					"order":         sortOrder,
					"unmapped_type": "long",
				},
			},
		},
		Source: eventsSource,
	}

	var must []interface{}
	var filter []interface{}

	if len(params.LogLevels) > 0 {
		var shouldClauses []interface{}
		for _, level := range params.LogLevels {
			shouldClauses = append(shouldClauses, map[string]interface{}{
				"match_phrase": map[string]interface{}{
					"log.severity.text": level,
				},
			})
		}
		filter = append(filter, map[string]interface{}{
			"bool": map[string]interface{}{
				"should":               shouldClauses,
				"minimum_should_match": 1,
			},
		})
	}

	if params.Namespace != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/namespace": params.Namespace},
		})
	}
	if params.ProjectUID != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/project-uid": params.ProjectUID},
		})
	}
	if params.ComponentUID != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/component-uid": params.ComponentUID},
		})
	}
	if params.EnvironmentUID != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"resource.openchoreo.dev/environment-uid": params.EnvironmentUID},
		})
	}
	if params.WorkflowRun != "" {
		filter = append(filter, map[string]interface{}{
			"match_phrase": map[string]interface{}{"workflow_run_name": params.WorkflowRun},
		})
	}

	if len(must) > 0 || len(filter) > 0 {
		req.Query = &searchQuery{
			Bool: searchBoolQuery{
				Must:   must,
				Filter: filter,
			},
		}
	}

	return json.Marshal(req)
}
