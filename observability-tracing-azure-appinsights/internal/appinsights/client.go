// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package appinsights

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

// spanDetailsLookback bounds the single-span lookup, whose API carries no
// time range. Matches the default workspace retention.
const spanDetailsLookback = 30 * 24 * time.Hour

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
	WorkspaceID  string
	QueryTimeout time.Duration
}

func NewClient(cred azcore.TokenCredential, cfg Config, logger *slog.Logger) (*Client, error) {
	if cfg.WorkspaceID == "" {
		return nil, errors.New("appinsights: WorkspaceID is required")
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 30 * time.Second
	}
	api, err := azlogs.NewClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("appinsights: azlogs.NewClient: %w", err)
	}
	return &Client{
		api:          api,
		workspaceID:  cfg.WorkspaceID,
		queryTimeout: cfg.QueryTimeout,
		logger:       logger,
	}, nil
}

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
		return fmt.Errorf("appinsights: ping query failed: %w", err)
	}
	return nil
}

func (c *Client) QueryTraces(ctx context.Context, p TracesParams) (*TracesResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(BuildTracesListKQL(p)),
		Timespan: to.Ptr(azlogs.NewTimeInterval(p.StartTime, p.EndTime)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("appinsights: QueryTraces: %w", err)
	}

	traces, err := mapTraceRows(resp)
	if err != nil {
		return nil, err
	}
	return &TracesResult{
		Traces: traces,
		Total:  len(traces),
		TookMs: int(time.Since(startedAt).Milliseconds()),
	}, nil
}

// QuerySpans runs the spans-of-one-trace query. p.TraceID must be set.
func (c *Client) QuerySpans(ctx context.Context, p TracesParams) (*SpansResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(BuildSpansKQL(p)),
		Timespan: to.Ptr(azlogs.NewTimeInterval(p.StartTime, p.EndTime)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("appinsights: QuerySpans: %w", err)
	}

	spans, err := mapSpanRows(resp)
	if err != nil {
		return nil, err
	}
	return &SpansResult{
		Spans:  spans,
		Total:  len(spans),
		TookMs: int(time.Since(startedAt).Milliseconds()),
	}, nil
}

// GetSpanDetails looks up one span by trace and span ID.
func (c *Client) GetSpanDetails(ctx context.Context, traceID, spanID string) (*Span, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	end := time.Now().UTC()
	start := end.Add(-spanDetailsLookback)

	resp, err := c.api.QueryWorkspace(ctx, c.workspaceID, azlogs.QueryBody{
		Query:    to.Ptr(BuildSpanDetailsKQL(traceID, spanID)),
		Timespan: to.Ptr(azlogs.NewTimeInterval(start, end)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("appinsights: GetSpanDetails: %w", err)
	}

	spans, err := mapSpanRows(resp)
	if err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		return nil, nil
	}
	return &spans[0], nil
}
