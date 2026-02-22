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

// ComponentLogsParams holds parameters for component log queries.
type ComponentLogsParams struct {
	ComponentIDs  []string  `json:"componentIds,omitempty"`
	EnvironmentID string    `json:"environmentId"`
	ProjectID     string    `json:"projectId"`
	StartTime     time.Time `json:"startTime"`
	EndTime       time.Time `json:"endTime"`
	SearchPhrase  string    `json:"searchPhrase"`
	LogLevels     []string  `json:"logLevels"`
	Limit         int       `json:"limit"`
	SortOrder     string    `json:"sortOrder"`
}

// LogAlertParams holds parameters for creating log alerts
type LogAlertParams struct {
	Name           string `json:"name"`
	SearchPattern  string `json:"searchPattern"`
	ThresholdValue int    `json:"thresholdValue"`
	Duration       int    `json:"duration"`
	Frequency      int    `json:"frequency"`
}

// ComponentLogsEntry represents a parsed log entry
type ComponentLogsEntry struct {
	Timestamp     time.Time         `json:"timestamp"`
	Log           string            `json:"log"`
	LogLevel      string            `json:"logLevel"`
	ComponentID   string            `json:"componentId"`
	EnvironmentID string            `json:"environmentId"`
	ProjectID     string            `json:"projectId"`
	Namespace     string            `json:"namespace"`
	PodID         string            `json:"podId"`
	ContainerName string            `json:"containerName"`
	Labels        map[string]string `json:"labels"`
}

// ComponentLogsResult represents the result of a component log query
type ComponentLogsResult struct {
	Logs       []ComponentLogsEntry `json:"logs"`
	TotalCount int                    `json:"totalCount"`
	Took       int                    `json:"took"`
}

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
	url := fmt.Sprintf("%s/api/%s/_search", c.baseURL, c.org)

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

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body returned by OpenObserve", slog.Any("error", err))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("OpenObserve returned error",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		return nil, fmt.Errorf("openobserve returned status %d: %s", resp.StatusCode, string(body))
	}

	var openObserveResp OpenObserveResponse
	if err := json.Unmarshal(body, &openObserveResp); err != nil {
		c.logger.Error("Failed to unmarshal response from OpenObserve", slog.Any("error", err))
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &openObserveResp, nil
}

func (c *Client) GetComponentLogs(ctx context.Context, params ComponentLogsParams) (*ComponentLogsResult, error) {
	queryJSON, err := generateComponentLogsQuery(params, c.stream, c.logger)
	if err != nil {
		c.logger.Error("Failed to marshal query", slog.Any("error", err))
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	// Execute the search query
	openObserveResp, err := c.executeSearchQuery(ctx, queryJSON)
	if err != nil {
		return nil, err
	}

	// Convert to LogEntry format
	logs := make([]ComponentLogsEntry, 0, len(openObserveResp.Hits))
	for _, hit := range openObserveResp.Hits {
		// Extract timestamp
		timestamp := int64(0)
		if ts, ok := hit["_timestamp"].(float64); ok {
			timestamp = int64(ts)
		}
		entry := c.parseApplicationLogEntry(timestamp, hit)
		logs = append(logs, entry)
	}

	return &ComponentLogsResult{
		Logs:       logs,
		TotalCount: openObserveResp.Total,
		Took:       openObserveResp.Took,
	}, nil
}

// CreateAlert creates an alert in OpenObserve
func (c *Client) CreateAlert(ctx context.Context, params LogAlertParams) error {
	// Generate alert configuration JSON
	alertJSON, err := generateAlertConfig(params, c.stream, c.logger)
	if err != nil {
		c.logger.Error("Failed to generate alert config", slog.Any("error", err))
		return fmt.Errorf("failed to generate alert config: %w", err)
	}

	// Build the API endpoint
	url := fmt.Sprintf("%s/api/v2/%s/alerts", c.baseURL, c.org)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(alertJSON))
	if err != nil {
		c.logger.Error("Failed to create request", slog.Any("error", err))
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.token)

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to execute alert creation request", slog.Any("error", err))
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body", slog.Any("error", err))
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		c.logger.Error("OpenObserve returned error",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		return fmt.Errorf("openobserve returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// DeleteAlert deletes an alert from OpenObserve by name.
// It first looks up the alert ID by name using the list API, then deletes by ID.
func (c *Client) DeleteAlert(ctx context.Context, alertName string) error {
	// Look up the alert ID by name
	alertID, err := c.getAlertIDByName(ctx, alertName)
	if err != nil {
		return fmt.Errorf("failed to find alert %q: %w", alertName, err)
	}

	// Build the API endpoint
	url := fmt.Sprintf("%s/api/v2/%s/alerts/%s", c.baseURL, c.org, alertID)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		c.logger.Error("Failed to create request", slog.Any("error", err))
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.SetBasicAuth(c.user, c.token)

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to execute alert deletion request", slog.Any("error", err))
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body", slog.Any("error", err))
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		c.logger.Error("OpenObserve returned error",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		return fmt.Errorf("openobserve returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// getAlertIDByName looks up an alert's ID by its name using the v2 list alerts API.
func (c *Client) getAlertIDByName(ctx context.Context, name string) (string, error) {
	url := fmt.Sprintf("%s/api/v2/%s/alerts", c.baseURL, c.org)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.SetBasicAuth(c.user, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openobserve returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		List []struct {
			AlertID string `json:"alert_id"`
			Name    string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	for _, alert := range result.List {
		if alert.Name == name {
			return alert.AlertID, nil
		}
	}

	return "", fmt.Errorf("alert %q not found", name)
}

// parseApplicationLogEntry parses an application log from OpenObserve response
func (c *Client) parseApplicationLogEntry(timestamp int64, source map[string]interface{}) ComponentLogsEntry {
	entry := ComponentLogsEntry{
		Timestamp: time.UnixMicro(timestamp),
		Labels:    make(map[string]string),
	}

	// Parse fields with type assertions
	if log, ok := source["log"].(string); ok {
		entry.Log = log
	}
	if logLevel, ok := source["logLevel"].(string); ok {
		entry.LogLevel = logLevel
	}
	if componentID, ok := source["kubernetes_labels_openchoreo_dev_component_uid"].(string); ok {
		entry.ComponentID = componentID
	}
	if environmentID, ok := source["kubernetes_labels_openchoreo_dev_environment_uid"].(string); ok {
		entry.EnvironmentID = environmentID
	}
	if projectID, ok := source["kubernetes_labels_openchoreo_dev_project_uid"].(string); ok {
		entry.ProjectID = projectID
	}
	if namespace, ok := source["kubernetes_namespace_name"].(string); ok {
		entry.Namespace = namespace
	}
	if podID, ok := source["kubernetes_pod_id"].(string); ok {
		entry.PodID = podID
	}
	if containerName, ok := source["kubernetes_container_name"].(string); ok {
		entry.ContainerName = containerName
	}

	// Parse labels if present
	if labels, ok := source["labels"].(map[string]interface{}); ok {
		for k, v := range labels {
			if strVal, ok := v.(string); ok {
				entry.Labels[k] = strVal
			}
		}
	}

	return entry
}
