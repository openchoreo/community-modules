// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package observer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is an HTTP client for forwarding alerts to the Observer API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Observer client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type alertWebhookRequest struct {
	RuleName       string    `json:"ruleName"`
	RuleNamespace  string    `json:"ruleNamespace"`
	AlertValue     float64   `json:"alertValue"`
	AlertTimestamp time.Time `json:"alertTimestamp"`
}

// ForwardAlert sends an alert to the Observer webhook API.
func (c *Client) ForwardAlert(
	ctx context.Context,
	ruleName string,
	ruleNamespace string,
	alertValue float64,
	alertTimestamp time.Time,
) error {
	payload := alertWebhookRequest{
		RuleName:       ruleName,
		RuleNamespace:  ruleNamespace,
		AlertValue:     alertValue,
		AlertTimestamp: alertTimestamp,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	url := c.baseURL + "/api/v1alpha1/alerts/webhook"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call observer webhook endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("observer webhook endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	return nil
}
