// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package loganalytics wraps the Azure Monitor Query SDK (azlogs) and
// translates OpenChoreo log-query requests into KQL against ContainerLogV2.
package loganalytics

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

// azlogsAPI is the subset of azlogs.Client that we use. Defined as an
// interface so the unit tests can supply a fake; the real client satisfies
// it implicitly because it has these exact methods.
type azlogsAPI interface {
	QueryWorkspace(ctx context.Context, workspaceID string, body azlogs.QueryBody,
		options *azlogs.QueryWorkspaceOptions) (azlogs.QueryWorkspaceResponse, error)
}

// Client wraps azlogs.Client with the workspace ID, default query timeout,
// and logger. It exposes the small surface the handler actually needs.
type Client struct {
	api          azlogsAPI
	workspaceID  string
	queryTimeout time.Duration
	logger       *slog.Logger
}

// Config holds the static configuration for the Client.
type Config struct {
	// WorkspaceID is the Log Analytics workspace customerId (GUID), NOT the ARM ID.
	WorkspaceID string

	// QueryTimeout caps a single QueryWorkspace call. Default 30s.
	QueryTimeout time.Duration
}

// NewClient constructs a Client from a credential and config.
func NewClient(cred azcore.TokenCredential, cfg Config, logger *slog.Logger) (*Client, error) {
	if cfg.WorkspaceID == "" {
		return nil, errors.New("loganalytics: WorkspaceID is required")
	}
	if cfg.QueryTimeout == 0 {
		cfg.QueryTimeout = 30 * time.Second
	}
	api, err := azlogs.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("loganalytics: azlogs.NewClient: %w", err)
	}
	return &Client{
		api:          api,
		workspaceID:  cfg.WorkspaceID,
		queryTimeout: cfg.QueryTimeout,
		logger:       logger,
	}, nil
}

// Ping issues a near-zero-cost query against ContainerLogV2 to validate
// that credentials work and the workspace is reachable. Called once at
// boot. The pod crashes if this fails — matches the AWS adapter's pattern.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	// One-hour window starting an hour ago is enough to validate the
	// workspace exists and we can read from it. If the workspace is empty,
	// the response is still a valid 200 with zero rows.
	end := time.Now().UTC()
	start := end.Add(-1 * time.Hour)

	_, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(PingKQL()),
		Timespan: to.Ptr(azlogs.NewTimeInterval(start, end)),
	}, nil)
	if err != nil {
		return fmt.Errorf("loganalytics: ping query failed: %w", err)
	}
	return nil
}

// GetComponentLogs runs the component-logs KQL and maps the result into
// []ComponentLogEntry.
func (c *Client) GetComponentLogs(ctx context.Context, p ComponentLogsParams) (*ComponentLogsResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	kql := BuildComponentLogsKQL(p)

	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(kql),
		Timespan: to.Ptr(azlogs.NewTimeInterval(p.StartTime, p.EndTime)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("loganalytics: GetComponentLogs: %w", err)
	}

	entries, err := mapComponentRows(resp)
	if err != nil {
		return nil, err
	}

	return &ComponentLogsResult{
		Logs:       entries,
		TotalCount: len(entries),
		TookMs:     int(time.Since(startedAt).Milliseconds()),
	}, nil
}

// GetWorkflowLogs runs the workflow-logs KQL and maps the result.
func (c *Client) GetWorkflowLogs(ctx context.Context, p WorkflowLogsParams) (*WorkflowLogsResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	kql := BuildWorkflowLogsKQL(p)

	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(kql),
		Timespan: to.Ptr(azlogs.NewTimeInterval(p.StartTime, p.EndTime)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("loganalytics: GetWorkflowLogs: %w", err)
	}

	entries, err := mapWorkflowRows(resp)
	if err != nil {
		return nil, err
	}

	return &WorkflowLogsResult{
		Logs:       entries,
		TotalCount: len(entries),
		TookMs:     int(time.Since(startedAt).Milliseconds()),
	}, nil
}
