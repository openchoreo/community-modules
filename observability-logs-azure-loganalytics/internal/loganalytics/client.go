// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

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

// QueryAPI is the slice of the Log Analytics query surface this client uses.
type QueryAPI interface {
	QueryWorkspace(ctx context.Context, workspaceID string, body azlogs.QueryBody,
		options *azlogs.QueryWorkspaceOptions) (azlogs.QueryWorkspaceResponse, error)
}

type Client struct {
	api             QueryAPI
	workspaceID     string
	queryTimeout    time.Duration
	eventsTable     string
	eventsScopeName string
	logger          *slog.Logger
}

type Config struct {
	WorkspaceID  string
	QueryTimeout time.Duration

	// EventsTable is the table Kubernetes events are read from. It is written
	// into queries as an identifier, so callers must validate it. Default
	// DefaultEventsTable.
	EventsTable string

	// EventsScopeName is the instrumentation scope that marks a record in
	// EventsTable as a Kubernetes event. Default DefaultEventsScopeName.
	EventsScopeName string
}

func NewClient(cred azcore.TokenCredential, cfg Config, logger *slog.Logger) (*Client, error) {
	if cfg.WorkspaceID == "" {
		return nil, errors.New("loganalytics: WorkspaceID is required")
	}
	api, err := azlogs.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("loganalytics: azlogs.NewClient: %w", err)
	}
	return NewClientWithQueryAPI(api, cfg, logger), nil
}

// NewClientWithQueryAPI builds a client over a caller-supplied query API.
func NewClientWithQueryAPI(api QueryAPI, cfg Config, logger *slog.Logger) *Client {
	if cfg.QueryTimeout == 0 {
		cfg.QueryTimeout = 30 * time.Second
	}
	if cfg.EventsTable == "" {
		cfg.EventsTable = DefaultEventsTable
	}
	if cfg.EventsScopeName == "" {
		cfg.EventsScopeName = DefaultEventsScopeName
	}
	if logger == nil {
		// The query paths log on degraded results, and an exported constructor
		// makes a nil logger easy to pass by accident.
		logger = slog.New(slog.DiscardHandler)
	}
	return &Client{
		api:             api,
		workspaceID:     cfg.WorkspaceID,
		queryTimeout:    cfg.QueryTimeout,
		eventsTable:     cfg.EventsTable,
		eventsScopeName: cfg.EventsScopeName,
		logger:          logger,
	}
}

// Ping issues a near-zero-cost query against ContainerLogV2 to validate
// that credentials work and the workspace is reachable. Called once at
// boot. The pod crashes if this fails
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

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
