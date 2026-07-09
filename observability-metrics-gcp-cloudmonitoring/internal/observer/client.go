// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package observer forwards fired alerts to the OpenChoreo Observer.
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

// Client posts fired alerts to the Observer's alert webhook endpoint.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient constructs an Observer client. baseURL is the Observer's base URL;
// a trailing slash is trimmed.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type alertWebhookRequest struct {
	RuleName       string    `json:"ruleName"`
	RuleNamespace  string    `json:"ruleNamespace"`
	AlertValue     float64   `json:"alertValue"`
	AlertTimestamp time.Time `json:"alertTimestamp"`
}

// ForwardAlert POSTs a fired alert to the Observer's
// /api/v1alpha1/alerts/webhook endpoint.
func (c *Client) ForwardAlert(ctx context.Context, ruleName, ruleNamespace string, alertValue float64, alertTimestamp time.Time) error {
	body, err := json.Marshal(alertWebhookRequest{
		RuleName:       ruleName,
		RuleNamespace:  ruleNamespace,
		AlertValue:     alertValue,
		AlertTimestamp: alertTimestamp,
	})
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	url := c.baseURL + "/api/v1alpha1/alerts/webhook"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call observer webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("observer webhook returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}
