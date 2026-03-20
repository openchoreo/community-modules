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

// extractLogLevel extracts log level from log content using common patterns.
func extractLogLevel(log string) string {
	upper := strings.ToUpper(log)

	levels := []string{"ERROR", "FATAL", "SEVERE", "WARN", "WARNING", "INFO", "DEBUG"}
	for _, level := range levels {
		if strings.Contains(upper, level) {
			if level == "WARNING" {
				return "WARN"
			}
			return level
		}
	}

	return "INFO"
}

// ComponentLogsParams holds parameters for component log queries.
type ComponentLogsParams struct {
	Namespace     string    `json:"namespace"`
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

// WorkflowLogsParams holds parameters for workflow log queries.
type WorkflowLogsParams struct {
	Namespace       string    `json:"namespace"`
	WorkflowRunName string    `json:"workflowRunName"`
	StartTime       time.Time `json:"startTime"`
	EndTime         time.Time `json:"endTime"`
	SearchPhrase    string    `json:"searchPhrase"`
	LogLevels       []string  `json:"logLevels"`
	Limit           int       `json:"limit"`
	SortOrder       string    `json:"sortOrder"`
}

// LogAlertParams holds parameters for creating log alerts.
type LogAlertParams struct {
	Name           *string `json:"name"`
	Namespace      string  `json:"namespace"`
	ProjectUID     string  `json:"projectUid"`
	EnvironmentUID string  `json:"environmentUid"`
	ComponentUID   string  `json:"componentUid"`
	SearchPattern  string  `json:"searchPattern"`
	Operator       string  `json:"operator"`
	ThresholdValue float32 `json:"thresholdValue"`
	Window         string  `json:"window"`
	Interval       string  `json:"interval"`
	Enabled        *bool   `json:"enabled"`
}

// ComponentLogsEntry represents a parsed log entry.
type ComponentLogsEntry struct {
	Timestamp       time.Time `json:"timestamp"`
	Log             string    `json:"log"`
	LogLevel        string    `json:"logLevel"`
	ComponentUID    string    `json:"componentUid"`
	ComponentName   string    `json:"componentName"`
	EnvironmentUID  string    `json:"environmentUid"`
	EnvironmentName string    `json:"environmentName"`
	ProjectUID      string    `json:"projectUid"`
	ProjectName     string    `json:"projectName"`
	Namespace       string    `json:"namespace"`
	PodName         string    `json:"podName"`
	PodNamespace    string    `json:"podNamespace"`
	ContainerName   string    `json:"containerName"`
}

// ComponentLogsResult represents the result of a component log query.
type ComponentLogsResult struct {
	Logs       []ComponentLogsEntry `json:"logs"`
	TotalCount int                  `json:"totalCount"`
	Took       int                  `json:"took"`
}

// WorkflowLogsEntry represents a parsed workflow log entry.
type WorkflowLogsEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Log       string                 `json:"log"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// WorkflowLogsResult represents the result of a workflow log query.
type WorkflowLogsResult struct {
	Logs       []WorkflowLogsEntry `json:"logs"`
	TotalCount int                 `json:"totalCount"`
	Took       int                 `json:"took"`
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

// extractTotalCount extracts the total count from a count query response.
// The response is expected to have hits[0].total as the count value.
func extractTotalCount(resp *OpenObserveResponse) int {
	if len(resp.Hits) > 0 {
		if total, ok := resp.Hits[0]["total"]; ok {
			if v, ok := total.(float64); ok {
				return int(v)
			}
		}
	}
	return 0
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

	// Execute a separate count query to get the true total number of matching logs
	countQueryJSON, err := generateComponentLogsCountQuery(params, c.stream, c.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to generate component logs count query: %w", err)
	}
	countResp, err := c.executeSearchQuery(ctx, countQueryJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to execute component logs count query: %w", err)
	}

	return &ComponentLogsResult{
		Logs:       logs,
		TotalCount: extractTotalCount(countResp),
		Took:       openObserveResp.Took,
	}, nil
}

// GetWorkflowLogs queries OpenObserve for workflow logs filtered by workflow run name.
func (c *Client) GetWorkflowLogs(ctx context.Context, params WorkflowLogsParams) (*WorkflowLogsResult, error) {
	queryJSON, err := generateWorkflowLogsQuery(params, c.stream, c.logger)
	if err != nil {
		c.logger.Error("Failed to marshal query", slog.Any("error", err))
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	openObserveResp, err := c.executeSearchQuery(ctx, queryJSON)
	if err != nil {
		return nil, err
	}

	logs := make([]WorkflowLogsEntry, 0, len(openObserveResp.Hits))
	for _, hit := range openObserveResp.Hits {
		timestamp := int64(0)
		if ts, ok := hit["_timestamp"].(float64); ok {
			timestamp = int64(ts)
		}
		entry := parseWorkflowLogEntry(timestamp, hit)
		logs = append(logs, entry)
	}

	// Execute a separate count query to get the true total number of matching workflow logs
	countQueryJSON, err := generateWorkflowLogsCountQuery(params, c.stream, c.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to generate workflow logs count query: %w", err)
	}
	countResp, err := c.executeSearchQuery(ctx, countQueryJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to execute workflow logs count query: %w", err)
	}

	return &WorkflowLogsResult{
		Logs:       logs,
		TotalCount: extractTotalCount(countResp),
		Took:       openObserveResp.Took,
	}, nil
}

// parseWorkflowLogEntry parses a workflow log from OpenObserve response.
func parseWorkflowLogEntry(timestamp int64, source map[string]interface{}) WorkflowLogsEntry {
	entry := WorkflowLogsEntry{
		Timestamp: time.UnixMicro(timestamp),
		Metadata:  make(map[string]interface{}),
	}

	if log, ok := source["log"].(string); ok {
		entry.Log = log
	}

	// Copy all fields except internal ones into metadata
	for k, v := range source {
		if k == "log" || k == "_timestamp" {
			continue
		}
		entry.Metadata[k] = v
	}

	return entry
}

// CreateAlert creates an alert in OpenObserve and returns the backend alert ID.
func (c *Client) CreateAlert(ctx context.Context, params LogAlertParams) (string, error) {
	// Generate alert configuration JSON
	alertJSON, err := generateAlertConfig(params, c.stream, c.logger)
	if err != nil {
		c.logger.Error("Failed to generate alert config", slog.Any("error", err))
		return "", fmt.Errorf("failed to generate alert config: %w", err)
	}

	// Build the API endpoint
	url := fmt.Sprintf("%s/api/v2/%s/alerts", c.baseURL, c.org)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(alertJSON))
	if err != nil {
		c.logger.Error("Failed to create request", slog.Any("error", err))
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.token)

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to execute alert creation request", slog.Any("error", err))
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body", slog.Any("error", err))
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		c.logger.Error("OpenObserve returned error",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		return "", fmt.Errorf("openobserve returned status %d: %s", resp.StatusCode, string(body))
	}

	// Try to extract id from response
	var createResp struct {
		AlertID string `json:"id"`
	}
	if err := json.Unmarshal(body, &createResp); err == nil && createResp.AlertID != "" {
		return createResp.AlertID, nil
	}

	return "", fmt.Errorf("openobserve create alert response missing id")
}

// DeleteAlert deletes an alert from OpenObserve by name and returns the backend alert ID.
// It first looks up the alert ID by name using the list API, then deletes by ID.
func (c *Client) DeleteAlert(ctx context.Context, alertName string) (string, error) {
	// Look up the alert ID by name
	alertID, err := c.getAlertIDByName(ctx, alertName)
	if err != nil {
		return "", fmt.Errorf("failed to find alert %q: %w", alertName, err)
	}

	// Build the API endpoint
	url := fmt.Sprintf("%s/api/v2/%s/alerts/%s", c.baseURL, c.org, alertID)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		c.logger.Error("Failed to create request", slog.Any("error", err))
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.SetBasicAuth(c.user, c.token)

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to execute alert deletion request", slog.Any("error", err))
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body", slog.Any("error", err))
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		c.logger.Error("OpenObserve returned error",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		return "", fmt.Errorf("openobserve returned status %d: %s", resp.StatusCode, string(body))
	}

	return alertID, nil
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

// AlertDetail represents the parsed details of an OpenObserve alert.
type AlertDetail struct {
	Name           string
	Enabled        bool
	SQL            string
	Operator       string
	Threshold      float64
	Period         int
	Frequency      int
	FrequencyType  string
	Namespace      string
	ProjectUID     string
	EnvironmentUID string
	ComponentUID   string
}

// UpdateAlert updates an alert in OpenObserve by name and returns the alert ID.
// It first looks up the alert ID by name, then updates it with the provided config.
func (c *Client) UpdateAlert(ctx context.Context, alertName string, params LogAlertParams) (string, error) {
	// Look up the alert ID by name
	alertID, err := c.getAlertIDByName(ctx, alertName)
	if err != nil {
		return "", fmt.Errorf("failed to find alert %q: %w", alertName, err)
	}

	// Ensure the canonical alertName from the path is used, not the one from the request body
	params.Name = &alertName

	// Generate alert configuration JSON
	alertJSON, err := generateAlertConfig(params, c.stream, c.logger)
	if err != nil {
		c.logger.Error("Failed to generate alert config", slog.Any("error", err))
		return "", fmt.Errorf("failed to generate alert config: %w", err)
	}

	// Build the API endpoint
	url := fmt.Sprintf("%s/api/v2/%s/alerts/%s", c.baseURL, c.org, alertID)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer(alertJSON))
	if err != nil {
		c.logger.Error("Failed to create request", slog.Any("error", err))
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.token)

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to execute alert update request", slog.Any("error", err))
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body", slog.Any("error", err))
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		c.logger.Error("OpenObserve returned error",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		return "", fmt.Errorf("openobserve returned status %d: %s", resp.StatusCode, string(body))
	}

	return alertID, nil
}

// GetAlert retrieves an alert from OpenObserve by name and returns its details.
// It first looks up the alert ID by name using the list API, then fetches the
// full alert details (including context_attributes) using the individual get API.
func (c *Client) GetAlert(ctx context.Context, alertName string) (*AlertDetail, error) {
	alertID, err := c.getAlertIDByName(ctx, alertName)
	if err != nil {
		return nil, fmt.Errorf("failed to find alert %q: %w", alertName, err)
	}

	url := fmt.Sprintf("%s/api/v2/%s/alerts/%s", c.baseURL, c.org, alertID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		c.logger.Error("Failed to create request", slog.Any("error", err))
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.SetBasicAuth(c.user, c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Failed to execute get alert request", slog.Any("error", err))
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Failed to read response body", slog.Any("error", err))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.Error("OpenObserve returned error",
			slog.Int("statusCode", resp.StatusCode),
			slog.String("body", string(body)))
		return nil, fmt.Errorf("openobserve returned status %d: %s", resp.StatusCode, string(body))
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	detail := &AlertDetail{}

	if name, ok := raw["name"].(string); ok {
		detail.Name = name
	}
	if enabled, ok := raw["enabled"].(bool); ok {
		detail.Enabled = enabled
	}

	if qc, ok := raw["query_condition"].(map[string]interface{}); ok {
		if sql, ok := qc["sql"].(string); ok {
			detail.SQL = sql
		}
	}

	if tc, ok := raw["trigger_condition"].(map[string]interface{}); ok {
		if op, ok := tc["operator"].(string); ok {
			detail.Operator = op
		}
		if threshold, ok := tc["threshold"].(float64); ok {
			detail.Threshold = threshold
		}
		if period, ok := tc["period"].(float64); ok {
			detail.Period = int(period)
		}
		if freq, ok := tc["frequency"].(float64); ok {
			detail.Frequency = int(freq)
		}
		if ft, ok := tc["frequency_type"].(string); ok {
			detail.FrequencyType = ft
		}
	}

	if ca, ok := raw["context_attributes"].(map[string]interface{}); ok {
		if v, ok := ca["namespace"].(string); ok {
			detail.Namespace = v
		}
		if v, ok := ca["projectUid"].(string); ok {
			detail.ProjectUID = v
		}
		if v, ok := ca["environmentUid"].(string); ok {
			detail.EnvironmentUID = v
		}
		if v, ok := ca["componentUid"].(string); ok {
			detail.ComponentUID = v
		}
	}

	return detail, nil
}

// parseApplicationLogEntry parses an application log from OpenObserve response
func (c *Client) parseApplicationLogEntry(timestamp int64, source map[string]interface{}) ComponentLogsEntry {
	entry := ComponentLogsEntry{
		Timestamp: time.UnixMicro(timestamp),
	}

	// Parse fields with type assertions
	if log, ok := source["log"].(string); ok {
		entry.Log = log
	}
	if logLevel, ok := source["logLevel"].(string); ok && strings.TrimSpace(logLevel) != "" {
		entry.LogLevel = strings.TrimSpace(logLevel)
	} else {
		entry.LogLevel = extractLogLevel(entry.Log)
	}
	if v, ok := source["kubernetes_labels_openchoreo_dev_component_uid"].(string); ok {
		entry.ComponentUID = v
	}
	if v, ok := source["kubernetes_labels_openchoreo_dev_component"].(string); ok {
		entry.ComponentName = v
	}
	if v, ok := source["kubernetes_labels_openchoreo_dev_environment_uid"].(string); ok {
		entry.EnvironmentUID = v
	}
	if v, ok := source["kubernetes_labels_openchoreo_dev_environment"].(string); ok {
		entry.EnvironmentName = v
	}
	if v, ok := source["kubernetes_labels_openchoreo_dev_project_uid"].(string); ok {
		entry.ProjectUID = v
	}
	if v, ok := source["kubernetes_labels_openchoreo_dev_project"].(string); ok {
		entry.ProjectName = v
	}
	if v, ok := source["kubernetes_labels_openchoreo_dev_namespace"].(string); ok {
		entry.Namespace = v
	}
	if v, ok := source["kubernetes_pod_name"].(string); ok {
		entry.PodName = v
	}
	if v, ok := source["kubernetes_namespace_name"].(string); ok {
		entry.PodNamespace = v
	}
	if v, ok := source["kubernetes_container_name"].(string); ok {
		entry.ContainerName = v
	}

	return entry
}
