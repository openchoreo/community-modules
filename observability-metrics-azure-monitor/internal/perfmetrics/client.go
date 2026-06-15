// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package perfmetrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
)

type azlogsAPI interface {
	QueryWorkspace(ctx context.Context, workspaceID string, body azlogs.QueryBody,
		options *azlogs.QueryWorkspaceOptions) (azlogs.QueryWorkspaceResponse, error)
}
type Client struct {
	api          azlogsAPI
	workspaceID  string
	queryTimeout time.Duration
	logger       *slog.Logger
}
type Config struct {
	// WorkspaceID is the Log Analytics workspace customerId GUID.
	WorkspaceID  string
	QueryTimeout time.Duration
}

func NewClient(cred azcore.TokenCredential, cfg Config, logger *slog.Logger) (*Client, error) {
	if cfg.WorkspaceID == "" {
		return nil, errors.New("perfmetrics: WorkspaceID is required")
	}
	if cfg.QueryTimeout < 0 {
		return nil, errors.New("perfmetrics: QueryTimeout must be >= 0")
	}
	if cfg.QueryTimeout == 0 {
		cfg.QueryTimeout = 30 * time.Second
	}
	api, err := azlogs.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("perfmetrics: azlogs.NewClient: %w", err)
	}
	return &Client{
		api:          api,
		workspaceID:  cfg.WorkspaceID,
		queryTimeout: cfg.QueryTimeout,
		logger:       logger,
	}, nil
}

func NewClientWithAPI(api azlogsAPI, cfg Config, logger *slog.Logger) *Client {
	qt := cfg.QueryTimeout
	if qt <= 0 {
		qt = 30 * time.Second
	}
	return &Client{
		api:          api,
		workspaceID:  cfg.WorkspaceID,
		queryTimeout: qt,
		logger:       logger,
	}
}

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	end := time.Now().UTC()
	start := end.Add(-1 * time.Hour)

	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(PingKQL()),
		Timespan: to.Ptr(azlogs.NewTimeInterval(start, end)),
	}, nil)
	if err != nil {
		return fmt.Errorf("perfmetrics: ping query failed: %w", err)
	}
	if rowCount(resp) == 0 && c.logger != nil {
		c.logger.Warn("Perf table returned no K8SContainer rows at boot; " +
			"verify Container Insights performance collection is enabled in the DCR")
	}
	return nil
}

func (c *Client) GetResourceMetrics(ctx context.Context, p MetricsQueryParams) (*ResourceMetricsResult, error) {
	if p.StartTime.IsZero() || p.EndTime.IsZero() {
		return nil, errors.New("startTime and endTime are required")
	}
	if !p.EndTime.After(p.StartTime) {
		return nil, fmt.Errorf("endTime (%s) must be after startTime (%s)", p.EndTime, p.StartTime)
	}

	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	kql := BuildResourceMetricsKQL(p)
	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(kql),
		Timespan: to.Ptr(azlogs.NewTimeInterval(p.StartTime, p.EndTime)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("perfmetrics: GetResourceMetrics: %w", err)
	}
	return mapResourceRows(resp)
}

func rowCount(resp azlogs.QueryWorkspaceResponse) int {
	if len(resp.Tables) == 0 {
		return 0
	}
	return len(resp.Tables[0].Rows)
}
