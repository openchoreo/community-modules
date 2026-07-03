// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package cloudlogging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"cloud.google.com/go/logging"
	"cloud.google.com/go/logging/logadmin"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type entryLister interface {
	Entries(ctx context.Context, opts ...logadmin.EntriesOption) *logadmin.EntryIterator
	Close() error
}

type Client struct {
	api          entryLister
	projectID    string
	queryTimeout time.Duration
	logger       *slog.Logger
}

type Config struct {
	ProjectID    string
	QueryTimeout time.Duration
}

// NewClient builds a Client backed by a real logadmin client. Credentials are
// resolved through Application Default Credentials
func NewClient(ctx context.Context, cfg Config, logger *slog.Logger, opts ...option.ClientOption) (*Client, error) {
	if cfg.ProjectID == "" {
		return nil, errors.New("cloudlogging: ProjectID is required")
	}
	if cfg.QueryTimeout == 0 {
		cfg.QueryTimeout = 30 * time.Second
	}
	api, err := logadmin.NewClient(ctx, cfg.ProjectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("cloudlogging: logadmin.NewClient: %w", err)
	}
	return &Client{
		api:          api,
		projectID:    cfg.ProjectID,
		queryTimeout: cfg.QueryTimeout,
		logger:       logger,
	}, nil
}

// Close releases the underlying client.
func (c *Client) Close() error {
	if c.api == nil {
		return nil
	}
	return c.api.Close()
}

// Ping issues a near-zero-cost query to validate that credentials work and the
// project's log store is reachable.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	end := time.Now().UTC()
	start := end.Add(-1 * time.Hour)
	filter := fmt.Sprintf("resource.type=%s\n%s",
		quote(k8sContainerResource),
		joinTimeRange(start, end),
	)

	it := c.api.Entries(ctx, logadmin.Filter(filter), logadmin.NewestFirst(), logadmin.PageSize(1))
	if _, err := it.Next(); err != nil && !errors.Is(err, iterator.Done) {
		return fmt.Errorf("cloudlogging: ping query failed: %w", err)
	}
	return nil
}

// GetComponentLogs runs a component-scoped query and returns the projected
// entries.
func (c *Client) GetComponentLogs(ctx context.Context, p ComponentLogsParams) (*ComponentLogsResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	filter := BuildComponentLogsFilter(p)

	entries, err := collect(ctx, c, filter, p.SortOrder, p.Limit, func(e *logging.Entry) (ComponentLogEntry, bool) {
		return mapComponentEntry(e), true
	})
	if err != nil {
		return nil, fmt.Errorf("cloudlogging: GetComponentLogs: %w", err)
	}

	return &ComponentLogsResult{
		Logs:       entries,
		TotalCount: len(entries),
		TookMs:     int(time.Since(startedAt).Milliseconds()),
	}, nil
}

// GetWorkflowLogs runs a workflow-scoped query and returns the projected
// entries.
func (c *Client) GetWorkflowLogs(ctx context.Context, p WorkflowLogsParams) (*WorkflowLogsResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()

	startedAt := time.Now()
	filter := BuildWorkflowLogsFilter(p)

	entries, err := collect(ctx, c, filter, p.SortOrder, p.Limit, func(e *logging.Entry) (WorkflowLogEntry, bool) {
		return mapWorkflowEntry(e), true
	})
	if err != nil {
		return nil, fmt.Errorf("cloudlogging: GetWorkflowLogs: %w", err)
	}

	return &WorkflowLogsResult{
		Logs:       entries,
		TotalCount: len(entries),
		TookMs:     int(time.Since(startedAt).Milliseconds()),
	}, nil
}

func collect[T any](ctx context.Context, c *Client, filter string, order SortOrder, limit int, fn func(*logging.Entry) (T, bool)) ([]T, error) {
	opts := []logadmin.EntriesOption{logadmin.Filter(filter)}
	if order != SortAsc {
		opts = append(opts, logadmin.NewestFirst())
	}

	it := c.api.Entries(ctx, opts...)
	out := make([]T, 0, limit)
	for limit <= 0 || len(out) < limit {
		entry, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		if mapped, keep := fn(entry); keep {
			out = append(out, mapped)
		}
	}
	return out, nil
}

func joinTimeRange(start, end time.Time) string {
	return strings.Join(timeRangeClauses(start, end), "\n")
}
