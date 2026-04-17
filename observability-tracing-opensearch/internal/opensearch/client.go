// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// Client wraps the OpenSearch client with logging.
type Client struct {
	client             *opensearchapi.Client
	logger             *slog.Logger
	address            string
	insecureSkipVerify bool
}

// NewClient creates a new OpenSearch client with the provided configuration.
func NewClient(address, username, password string, insecureSkipVerify bool, logger *slog.Logger) (*Client, error) {
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{address},
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // G402: Using self-signed cert
				},
			},
			Username: username,
			Password: password,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenSearch client: %w", err)
	}

	return &Client{
		client:             client,
		logger:             logger,
		address:            address,
		insecureSkipVerify: insecureSkipVerify,
	}, nil
}

// CheckHealth performs a health check against the OpenSearch cluster.
func (c *Client) CheckHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "/_cluster/health", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	res, err := c.client.Client.Perform(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(res.Body)
		return fmt.Errorf("health check failed with status %d: %s", res.StatusCode, string(bodyBytes))
	}

	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&health); err != nil {
		return fmt.Errorf("failed to parse health response: %w", err)
	}

	if health.Status != "green" && health.Status != "yellow" {
		return fmt.Errorf("cluster health status is %q", health.Status)
	}

	c.logger.Info("OpenSearch cluster health check passed", slog.String("status", health.Status))
	return nil
}

// Search executes a search request against OpenSearch and returns parsed hits.
func (c *Client) Search(ctx context.Context, indices []string, query map[string]interface{}) (*SearchResponse, error) {
	c.logger.Debug("Executing search", "indices", indices)

	if c.logger.Enabled(ctx, slog.LevelDebug) {
		queryJSON, err := json.MarshalIndent(query, "", "  ")
		if err == nil {
			fmt.Println("OpenSearch Query:")
			fmt.Println(string(queryJSON))
		}
	}

	body, err := buildSearchBody(query)
	if err != nil {
		return nil, err
	}

	ignoreUnavailable := true
	resp, err := c.client.Search(ctx, &opensearchapi.SearchReq{
		Indices: indices,
		Body:    body,
		Params: opensearchapi.SearchParams{
			IgnoreUnavailable: &ignoreUnavailable,
		},
	})
	if err != nil {
		c.logger.Error("Search request failed", "error", err)
		return nil, fmt.Errorf("search request failed: %w", err)
	}

	response := &SearchResponse{
		Took:     resp.Took,
		TimedOut: resp.Timeout,
	}
	response.Hits.Total.Value = resp.Hits.Total.Value
	response.Hits.Total.Relation = resp.Hits.Total.Relation

	for _, h := range resp.Hits.Hits {
		var source map[string]interface{}
		if err := json.Unmarshal(h.Source, &source); err != nil {
			c.logger.Warn("Failed to unmarshal hit source", "hit_id", h.ID, "error", err)
			continue
		}
		hit := Hit{
			ID:     h.ID,
			Source: source,
		}
		score := float64(h.Score)
		hit.Score = &score
		response.Hits.Hits = append(response.Hits.Hits, hit)
	}

	c.logger.Debug("Search completed",
		"total_hits", response.Hits.Total.Value,
		"returned_hits", len(response.Hits.Hits))

	return response, nil
}

// SearchRaw executes a search request and returns the raw response body,
// which is needed for parsing aggregation results.
func (c *Client) SearchRaw(ctx context.Context, indices []string, query map[string]interface{}) (*SearchResponse, error) {
	c.logger.Debug("Executing raw search with aggregations", "indices", indices)

	if c.logger.Enabled(ctx, slog.LevelDebug) {
		queryJSON, err := json.MarshalIndent(query, "", "  ")
		if err == nil {
			fmt.Println("OpenSearch Query:")
			fmt.Println(string(queryJSON))
		}
	}

	if len(indices) == 0 {
		return nil, fmt.Errorf("at least one index must be specified")
	}

	body, err := buildSearchBody(query)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "/"+strings.Join(indices, ",")+"/_search?ignore_unavailable=true", body)
	if err != nil {
		return nil, fmt.Errorf("failed to create search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.client.Client.Perform(req)
	if err != nil {
		c.logger.Error("Raw search request failed", "error", err)
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("search request failed with status %d: %s", res.StatusCode, string(bodyBytes))
	}

	var response SearchResponse
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %w", err)
	}

	c.logger.Debug("Raw search completed",
		"total_hits", response.Hits.Total.Value,
		"has_aggregations", len(response.Aggregations) > 0)

	return &response, nil
}
