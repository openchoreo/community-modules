// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opensearch

import (
	"bytes"
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

const alertsIndexName = "openchoreo-alerts"

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

// Search executes a search request against OpenSearch.
func (c *Client) Search(ctx context.Context, indices []string, query map[string]interface{}) (*SearchResponse, error) {
	c.logger.Debug("Executing search", "indices", indices)

	ignoreUnavailable := true
	resp, err := c.client.Search(ctx, &opensearchapi.SearchReq{
		Indices: indices,
		Body:    buildSearchBody(query),
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

// SearchMonitorByName searches alerting monitors by name using the Alerting plugin API.
func (c *Client) SearchMonitorByName(ctx context.Context, name string) (string, bool, error) {
	path := "/_plugins/_alerting/monitors/_search"
	nameJSON, err := json.Marshal(name)
	if err != nil {
		return "", false, fmt.Errorf("failed to marshal monitor name: %w", err)
	}
	queryBody := fmt.Sprintf(`{
		"query": {
				"match_phrase": {
						"monitor.name": %s
				}
		}
  }`, string(nameJSON))

	req, err := http.NewRequestWithContext(ctx, "POST", path, strings.NewReader(queryBody))
	if err != nil {
		return "", false, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")

	res, err := c.client.Client.Perform(req)
	if err != nil {
		return "", false, fmt.Errorf("monitor search request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("monitor search request failed with status: %d", res.StatusCode)
	}

	parsed, err := parseSearchResponse(res.Body)
	if err != nil {
		return "", false, fmt.Errorf("failed to parse monitor search response: %w", err)
	}

	if parsed.Hits.Total.Value == 0 || len(parsed.Hits.Hits) == 0 {
		return "", false, nil
	}
	if parsed.Hits.Hits[0].ID == "" {
		return "", false, fmt.Errorf("monitor search response missing _id field")
	}
	return parsed.Hits.Hits[0].ID, true, nil
}

// CreateMonitor creates a new alerting monitor using the Alerting plugin API.
func (c *Client) CreateMonitor(ctx context.Context, monitor map[string]interface{}) (string, int64, error) {
	body, err := json.Marshal(monitor)
	if err != nil {
		return "", 0, fmt.Errorf("failed to marshal monitor: %w", err)
	}
	c.logger.Debug("Creating monitor", "body", string(body))

	path := "/_plugins/_alerting/monitors"
	req, err := http.NewRequest("POST", path, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")

	res, err := c.client.Client.Perform(req)
	if err != nil {
		return "", 0, fmt.Errorf("monitor create request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(res.Body)
		c.logger.Error("Monitor create failed",
			"status", res.StatusCode,
			"response", string(bodyBytes))
		return "", 0, fmt.Errorf("monitor create request failed with status: %d, response: %s", res.StatusCode, string(bodyBytes))
	}

	type MonitorUpsertResponse struct {
		LastUpdateTime int64 `json:"last_update_time"`
	}
	var parsed struct {
		ID      string                `json:"_id"`
		Monitor MonitorUpsertResponse `json:"monitor"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return "", 0, fmt.Errorf("failed to parse monitor create response: %w", err)
	}

	c.logger.Debug("Monitor created", slog.String("id", parsed.ID), slog.Int64("last_update_time", parsed.Monitor.LastUpdateTime))
	return parsed.ID, parsed.Monitor.LastUpdateTime, nil
}

// GetMonitorByID retrieves an alerting monitor by ID using the Alerting plugin API.
func (c *Client) GetMonitorByID(ctx context.Context, monitorID string) (map[string]interface{}, error) {
	path := fmt.Sprintf("/_plugins/_alerting/monitors/%s", monitorID)
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")

	res, err := c.client.Client.Perform(req)
	if err != nil {
		return nil, fmt.Errorf("monitor get request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(res.Body)
		c.logger.Error("Monitor get failed",
			"status", res.StatusCode,
			"monitor_id", monitorID,
			"response", string(bodyBytes))
		return nil, fmt.Errorf("monitor get request failed with status: %d, response: %s", res.StatusCode, string(bodyBytes))
	}

	var response map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse monitor get response: %w", err)
	}

	if monitor, ok := response["monitor"].(map[string]interface{}); ok {
		return monitor, nil
	}

	return nil, fmt.Errorf("monitor object not found in response")
}

// UpdateMonitor updates an existing alerting monitor using the Alerting plugin API.
func (c *Client) UpdateMonitor(ctx context.Context, monitorID string, monitor map[string]interface{}) (int64, error) {
	body, err := json.Marshal(monitor)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal monitor: %w", err)
	}
	c.logger.Debug("Updating monitor", "monitor_id", monitorID, "body", string(body))

	path := fmt.Sprintf("/_plugins/_alerting/monitors/%s", monitorID)
	req, err := http.NewRequestWithContext(ctx, "PUT", path, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")

	res, err := c.client.Client.Perform(req)
	if err != nil {
		return 0, fmt.Errorf("monitor update request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(res.Body)
		c.logger.Error("Monitor update failed",
			"status", res.StatusCode,
			"monitor_id", monitorID,
			"response", string(bodyBytes))
		return 0, fmt.Errorf("monitor update request failed with status: %d, response: %s", res.StatusCode, string(bodyBytes))
	}

	type MonitorUpsertResponse struct {
		LastUpdateTime int64 `json:"last_update_time"`
	}
	var parsed struct {
		ID      string                `json:"_id"`
		Monitor MonitorUpsertResponse `json:"monitor"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("failed to parse monitor update response: %w", err)
	}

	c.logger.Debug("Monitor updated successfully",
		"monitor_id", monitorID,
		"last_update_time", parsed.Monitor.LastUpdateTime)
	return parsed.Monitor.LastUpdateTime, nil
}

// DeleteMonitor deletes an alerting monitor using the Alerting plugin API.
func (c *Client) DeleteMonitor(ctx context.Context, monitorID string) error {
	path := fmt.Sprintf("/_plugins/_alerting/monitors/%s", monitorID)
	req, err := http.NewRequestWithContext(ctx, "DELETE", path, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")

	res, err := c.client.Client.Perform(req)
	if err != nil {
		return fmt.Errorf("monitor delete request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		bodyBytes, _ := io.ReadAll(res.Body)
		c.logger.Error("Monitor delete failed",
			"status", res.StatusCode,
			"monitor_id", monitorID,
			"response", string(bodyBytes))
		return fmt.Errorf("monitor delete request failed with status: %d, response: %s", res.StatusCode, string(bodyBytes))
	}

	c.logger.Debug("Monitor deleted successfully", "monitor_id", monitorID)
	return nil
}

// WriteAlertEntry writes an alert entry to the openchoreo-alerts index.
func (c *Client) WriteAlertEntry(ctx context.Context, entry map[string]interface{}) (string, error) {
	body, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("failed to marshal alert entry: %w", err)
	}

	resp, err := c.client.Index(ctx, opensearchapi.IndexReq{
		Index: alertsIndexName,
		Body:  bytes.NewReader(body),
		Params: opensearchapi.IndexParams{
			Refresh: "true",
		},
	})
	if err != nil {
		c.logger.Error("Alert index request failed", "error", err)
		return "", fmt.Errorf("alert index request failed: %w", err)
	}

	c.logger.Debug("Alert entry written", "alert_id", resp.ID)
	return resp.ID, nil
}
