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

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// QueryResourceMetrics fetches resource usage/request/limit time series for a
// component from the Observer. The caller's JWT is forwarded as a Bearer token
// so the Observer can authenticate the request.
func (c *Client) QueryResourceMetrics(
	ctx context.Context,
	token string,
	scope ComponentSearchScope,
	start, end time.Time,
	step string,
) (*ResourceMetrics, error) {
	payload := MetricsQueryRequest{
		Metric:      "resource",
		StartTime:   start,
		EndTime:     end,
		Step:        step,
		SearchScope: scope,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metrics query: %w", err)
	}

	endpoint := c.baseURL + "/api/v1/metrics/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call observer metrics API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read observer response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("observer metrics API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var metrics ResourceMetrics
	if err := json.Unmarshal(respBody, &metrics); err != nil {
		return nil, fmt.Errorf("failed to decode observer response: %w", err)
	}

	return &metrics, nil
}
