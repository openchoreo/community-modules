// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package opencost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(baseURL string, logger *slog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			// Stepped allocation queries make OpenCost run a Prometheus query per
			// bucket, which can take tens of seconds over wide windows.
			Timeout: 120 * time.Second,
		},
		logger: logger,
	}
}

// QueryAllocations calls OpenCost's /allocation/compute endpoint and returns the
// allocation sets (one per step, or a single accumulated set when no step is set).
func (c *Client) QueryAllocations(ctx context.Context, q AllocationQuery) ([]map[string]Allocation, error) {
	params := url.Values{}
	params.Set("window", BuildWindow(q.Start, q.End))
	params.Set("aggregate", "pod")
	params.Set("filter", BuildFilter(q.Namespace, q.EnvironmentUID, q.ProjectUID, q.ComponentUID))
	if q.Step != "" {
		params.Set("step", q.Step)
		params.Set("accumulate", "false")
	} else {
		params.Set("accumulate", "true")
	}

	endpoint := c.baseURL + "/allocation/compute?" + params.Encode()
	c.logger.Debug("querying OpenCost allocations", slog.String("url", endpoint))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call OpenCost allocation API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenCost response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OpenCost allocation API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed AllocationResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode OpenCost response: %w", err)
	}

	return parsed.Data, nil
}
